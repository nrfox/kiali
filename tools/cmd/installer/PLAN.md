# ACM Installer Plan

This document describes the implementation plan for adding ACM (Advanced Cluster
Management) support to the Go installer. The scope is the **ACM operator and
MultiClusterHub only** — no observability (no MinIO, no Thanos, no
MultiClusterObservability), no Istio, no Kiali, no test apps. The source of
truth for expected behavior is `hack/install-acm.sh`.

---

## What to build

Add an `acm` subcommand to the existing `installer` binary:

```
installer acm install    # Install ACM operator + MultiClusterHub
installer acm uninstall  # Remove all ACM components
installer acm status     # Show ACM status
```

Add a new package at `tools/cmd/installer/acm/acm.go`.

---

## Package: `tools/cmd/installer/acm`

### Config struct

```go
type Config struct {
    Channel     string        // OLM channel, default "release-2.15"
    KubeContext string        // kubectl --context value, empty = current context
    Namespace   string        // ACM namespace, default "open-cluster-management"
    Timeout     time.Duration // default 1200s
}

func NewConfig() *Config { /* fill in above defaults */ }
```

### Public API

```go
func Install(ctx context.Context, cfg *Config, logger *zerolog.Logger) error
func Uninstall(ctx context.Context, cfg *Config, logger *zerolog.Logger) error
func Status(ctx context.Context, cfg *Config, logger *zerolog.Logger) error
```

All functions are idempotent: running them twice must produce the same result.

### kubectl helper

Write a package-level helper that wraps `command.Command` to prepend
`--context <ctx>` when `KubeContext` is non-empty. All `kubectl` calls in the
package must go through this helper. This matches the pattern used by the
`kiali` package.

```go
func kubectl(cfg *Config, args ...string) *command.Cmd {
    if cfg.KubeContext != "" {
        args = append([]string{"--context", cfg.KubeContext}, args...)
    }
    return command.Command("kubectl", args...)
}
```

Use `command.ServerSideApply` when `KubeContext` is set. When it may be empty,
use a local wrapper that conditionally includes `--context`:

```go
func serverSideApply(cfg *Config, yaml string) error {
    if cfg.KubeContext != "" {
        return command.ServerSideApply(cfg.KubeContext, yaml)
    }
    return command.Command("kubectl",
        "apply", "--server-side",
        "--field-manager=kiali-installer", "--force-conflicts", "-f", "-").
        WithInput(strings.NewReader(yaml)).Run()
}
```

---

## `Install` function — step by step

### Step 1: Check prerequisites

Check cluster connectivity and cluster-admin access:

```
kubectl auth can-i create namespaces --all-namespaces
```

Return an error if this fails.

### Step 2: Check if ACM is already installed

Skip install if `kubectl get namespace <Namespace>` exists AND
`kubectl get mch -n <Namespace>` succeeds. Log "ACM already installed" and
return nil.

### Step 3: Create ACM namespace

Apply:
```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: <Namespace>
```

### Step 4: Create OperatorGroup

Apply:
```yaml
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: acm-operator-group
  namespace: <Namespace>
spec:
  targetNamespaces:
  - <Namespace>
```

### Step 5: Create Subscription

Apply:
```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: advanced-cluster-management
  namespace: <Namespace>
spec:
  channel: <Channel>
  installPlanApproval: Automatic
  name: advanced-cluster-management
  source: redhat-operators
  sourceNamespace: openshift-marketplace
```

### Step 6: Wait for CSV

Poll until:
```
kubectl get csv -n <Namespace> \
  -o jsonpath='{.items[?(@.spec.displayName=="Advanced Cluster Management for Kubernetes")].metadata.name}'
```
returns a non-empty string. Poll every 10 seconds, up to `Timeout`. Return error on timeout.

Then wait for CSV phase:
```
kubectl wait --for=jsonpath={.status.phase}=Succeeded \
  csv/<csv-name> -n <Namespace> --timeout=<Timeout>s
```

### Step 7: Create MultiClusterHub

Apply:
```yaml
apiVersion: operator.open-cluster-management.io/v1
kind: MultiClusterHub
metadata:
  name: multiclusterhub
  namespace: <Namespace>
spec: {}
```

### Step 8: Wait for MultiClusterHub to reach Running

Poll:
```
kubectl get mch multiclusterhub -n <Namespace> \
  -o jsonpath='{.status.phase}'
```
every 15 seconds until value is `"Running"`, up to `Timeout`. Return error on timeout with current phase in message.


---

## `Uninstall` function — step by step

Delete in reverse order of installation. Use `--ignore-not-found` everywhere.

1. Delete MultiClusterHub:
   `kubectl delete mch multiclusterhub -n <Namespace> --wait=false --ignore-not-found`
   Then poll until `kubectl get mch multiclusterhub -n <Namespace>` returns
   not-found (5s interval, up to `Timeout`). This handles finalizer delays.
2. Delete Subscription: `kubectl delete subscription.operators.coreos.com advanced-cluster-management -n <Namespace> --ignore-not-found`
3. Detect and delete CSV: query
   `kubectl get csv -n <Namespace> -o jsonpath='{.items[?(@.spec.displayName=="Advanced Cluster Management for Kubernetes")].metadata.name}'`
   then `kubectl delete csv <name> -n <Namespace> --ignore-not-found`
4. Delete OperatorGroup: `kubectl delete operatorgroup acm-operator-group -n <Namespace> --ignore-not-found`
5. Delete ACM namespace: `kubectl delete namespace <Namespace> --timeout=<Timeout>s --ignore-not-found`
6. Delete ACM CRDs:
   ```
   kubectl delete crd -l operators.coreos.com/advanced-cluster-management.<Namespace> \
     --timeout=<Timeout>s --ignore-not-found
   ```

---

## `Status` function

Print a summary of key resources. Treat missing resources as non-errors; just
report "[NOT FOUND]". Do not return an error if ACM isn't installed.

Report the following:
- ACM namespace exists/not found
- ACM Subscription state
  (`kubectl get subscription.operators.coreos.com advanced-cluster-management -n <Namespace> -o jsonpath='{.status.state}'`)
- CSV phase
  (`kubectl get csv -n <Namespace> -o jsonpath='{.items[?(@.spec.displayName=="Advanced Cluster Management for Kubernetes")].status.phase}'`)
- MultiClusterHub phase
  (`kubectl get mch multiclusterhub -n <Namespace> -o jsonpath='{.status.phase}'`)
- local-cluster ManagedCluster Available condition
  (`kubectl get managedcluster local-cluster -o jsonpath='{.status.conditions[?(@.type=="ManagedClusterConditionAvailable")].status}'`)

---

## Integration into `main.go`

Add the following to `main.go`:

```go
acmCfg := acm.NewConfig()
acmCmd := &cobra.Command{
    Use:   "acm",
    Short: "Install and manage ACM on an OpenShift cluster",
}

acmInstallCmd := &cobra.Command{
    Use:   "install",
    Short: "Install ACM operator and MultiClusterHub",
    RunE: func(cmd *cobra.Command, _ []string) error {
        log.InitializeLogger(log.WithColor())
        return acm.Install(cmd.Context(), acmCfg, log.Logger())
    },
}

acmUninstallCmd := &cobra.Command{
    Use:   "uninstall",
    Short: "Remove ACM operator and MultiClusterHub",
    RunE: func(cmd *cobra.Command, _ []string) error {
        log.InitializeLogger(log.WithColor())
        return acm.Uninstall(cmd.Context(), acmCfg, log.Logger())
    },
}

acmStatusCmd := &cobra.Command{
    Use:   "status",
    Short: "Show the status of ACM components",
    RunE: func(cmd *cobra.Command, _ []string) error {
        log.InitializeLogger(log.WithColor())
        return acm.Status(cmd.Context(), acmCfg, log.Logger())
    },
}

acmCmd.PersistentFlags().StringVar(&acmCfg.Channel, "channel", acmCfg.Channel, "ACM OLM channel")
acmCmd.PersistentFlags().StringVar(&acmCfg.KubeContext, "kube-context", acmCfg.KubeContext, "kubectl context to use (empty = current context)")
acmCmd.PersistentFlags().StringVar(&acmCfg.Namespace, "namespace", acmCfg.Namespace, "ACM namespace")
acmCmd.PersistentFlags().DurationVar(&acmCfg.Timeout, "timeout", acmCfg.Timeout, "Timeout for operations")

acmCmd.AddCommand(acmInstallCmd, acmUninstallCmd, acmStatusCmd)
rootCmd.AddCommand(acmCmd)
```

---

## Implementation notes

- **No Kiali, Istio, or observability.** The `acm` package covers the ACM
  operator and MultiClusterHub only. No MinIO, no Thanos, no
  MultiClusterObservability, no Istio CRDs, no Kiali.
- **Idempotent.** Every step must check for existence before creating. Use
  server-side apply for all Kubernetes resources — it is inherently idempotent.
- **Inline YAML.** Use `fmt.Sprintf` to construct manifests inline, consistent
  with the rest of the installer. No external template files.
- **Error wrapping.** Wrap all errors with `fmt.Errorf("context: %w", err)`.
- **No panics.** This is library code; return errors, never panic.
- **Logging.** Log each high-level step with `logger.Info().Msg(...)` before it
  starts. Subprocess output is suppressed by the `command` package — do not
  print it manually.
- **Context propagation.** Pass `ctx` through to all operations that take it.
  The `command.Command` wrapper does not accept a context but the caller can
  respect context cancellation between steps by checking `ctx.Err()`.
- **Polling pattern.** For all wait loops, use a pattern like:
  ```go
  deadline := time.Now().Add(cfg.Timeout)
  for {
      if time.Now().After(deadline) {
          return fmt.Errorf("timeout waiting for X after %s", cfg.Timeout)
      }
      // check condition
      select {
      case <-ctx.Done():
          return ctx.Err()
      case <-time.After(interval):
      }
  }
  ```
- **Subscription API group.** When deleting Subscriptions, use the full API
  group: `subscription.operators.coreos.com` to avoid ambiguity with ACM's own
  `subscription.apps` CRD.

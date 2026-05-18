package acm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/kiali/kiali/tools/cmd/installer/command"
)

type Config struct {
	Channel     string
	KubeContext string
	Namespace   string
	Timeout     time.Duration
}

func NewConfig() *Config {
	return &Config{
		Channel:   "release-2.15",
		Namespace: "open-cluster-management",
		Timeout:   1200 * time.Second,
	}
}

func kubectl(cfg *Config, args ...string) *command.Cmd {
	if cfg.KubeContext != "" {
		args = append([]string{"--context", cfg.KubeContext}, args...)
	}
	return command.Command("kubectl", args...)
}

func serverSideApply(cfg *Config, yaml string) error {
	if cfg.KubeContext != "" {
		return command.ServerSideApply(cfg.KubeContext, yaml)
	}
	return command.Command("kubectl",
		"apply", "--server-side",
		"--field-manager=kiali-installer", "--force-conflicts", "-f", "-").
		WithInput(strings.NewReader(yaml)).Run()
}

func Install(ctx context.Context, cfg *Config, logger *zerolog.Logger) error {
	logger.Info().Msg("Checking cluster connectivity and permissions")
	if err := kubectl(cfg, "auth", "can-i", "create", "namespaces", "--all-namespaces").Run(); err != nil {
		return fmt.Errorf("cluster-admin access check failed: %w", err)
	}

	if alreadyInstalled(cfg) {
		logger.Info().Msg("ACM already installed")
		return nil
	}

	logger.Info().Msgf("Creating namespace %s", cfg.Namespace)
	nsYAML := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
`, cfg.Namespace)
	if err := serverSideApply(cfg, nsYAML); err != nil {
		return fmt.Errorf("creating namespace: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	logger.Info().Msg("Creating OperatorGroup")
	ogYAML := fmt.Sprintf(`apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: acm-operator-group
  namespace: %s
spec:
  targetNamespaces:
  - %s
`, cfg.Namespace, cfg.Namespace)
	if err := serverSideApply(cfg, ogYAML); err != nil {
		return fmt.Errorf("creating OperatorGroup: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	logger.Info().Msgf("Creating Subscription (channel: %s)", cfg.Channel)
	subYAML := fmt.Sprintf(`apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: advanced-cluster-management
  namespace: %s
spec:
  channel: %s
  installPlanApproval: Automatic
  name: advanced-cluster-management
  source: redhat-operators
  sourceNamespace: openshift-marketplace
`, cfg.Namespace, cfg.Channel)
	if err := serverSideApply(cfg, subYAML); err != nil {
		return fmt.Errorf("creating Subscription: %w", err)
	}

	logger.Info().Msg("Waiting for CSV to appear")
	csvName, err := waitForCSV(ctx, cfg, logger)
	if err != nil {
		return err
	}

	logger.Info().Msgf("Waiting for CSV %s to succeed", csvName)
	timeoutSec := fmt.Sprintf("%ds", int(cfg.Timeout.Seconds()))
	if err := kubectl(cfg, "wait", "--for=jsonpath={.status.phase}=Succeeded",
		fmt.Sprintf("csv/%s", csvName), "-n", cfg.Namespace, "--timeout="+timeoutSec).Run(); err != nil {
		return fmt.Errorf("waiting for CSV to succeed: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	logger.Info().Msg("Creating MultiClusterHub")
	mchYAML := fmt.Sprintf(`apiVersion: operator.open-cluster-management.io/v1
kind: MultiClusterHub
metadata:
  name: multiclusterhub
  namespace: %s
spec: {}
`, cfg.Namespace)
	if err := serverSideApply(cfg, mchYAML); err != nil {
		return fmt.Errorf("creating MultiClusterHub: %w", err)
	}

	logger.Info().Msg("Waiting for MultiClusterHub to reach Running")
	if err := waitForMCH(ctx, cfg, logger); err != nil {
		return err
	}

	logger.Info().Msg("ACM installation complete")
	return nil
}

func Uninstall(ctx context.Context, cfg *Config, logger *zerolog.Logger) error {
	timeoutSec := fmt.Sprintf("%ds", int(cfg.Timeout.Seconds()))

	logger.Info().Msg("Deleting MultiClusterHub")
	if err := kubectl(cfg, "delete", "mch", "multiclusterhub",
		"-n", cfg.Namespace, "--wait=false", "--ignore-not-found").Run(); err != nil {
		return fmt.Errorf("deleting MultiClusterHub: %w", err)
	}

	logger.Info().Msg("Waiting for MultiClusterHub deletion")
	if err := waitForMCHDeletion(ctx, cfg, logger); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	logger.Info().Msg("Deleting Subscription")
	if err := kubectl(cfg, "delete", "subscription.operators.coreos.com",
		"advanced-cluster-management", "-n", cfg.Namespace, "--ignore-not-found").Run(); err != nil {
		return fmt.Errorf("deleting Subscription: %w", err)
	}

	csvName := detectCSVName(cfg)
	if csvName != "" {
		logger.Info().Msgf("Deleting CSV %s", csvName)
		if err := kubectl(cfg, "delete", "csv", csvName,
			"-n", cfg.Namespace, "--ignore-not-found").Run(); err != nil {
			return fmt.Errorf("deleting CSV: %w", err)
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	logger.Info().Msg("Deleting OperatorGroup")
	if err := kubectl(cfg, "delete", "operatorgroup", "acm-operator-group",
		"-n", cfg.Namespace, "--ignore-not-found").Run(); err != nil {
		return fmt.Errorf("deleting OperatorGroup: %w", err)
	}

	logger.Info().Msgf("Deleting namespace %s", cfg.Namespace)
	if err := kubectl(cfg, "delete", "namespace", cfg.Namespace,
		"--timeout="+timeoutSec, "--ignore-not-found").Run(); err != nil {
		return fmt.Errorf("deleting namespace: %w", err)
	}

	logger.Info().Msg("Deleting ACM CRDs")
	if err := kubectl(cfg, "delete", "crd",
		"-l", fmt.Sprintf("operators.coreos.com/advanced-cluster-management.%s", cfg.Namespace),
		"--timeout="+timeoutSec, "--ignore-not-found").Run(); err != nil {
		return fmt.Errorf("deleting ACM CRDs: %w", err)
	}

	logger.Info().Msg("ACM uninstall complete")
	return nil
}

func Status(_ context.Context, cfg *Config, logger *zerolog.Logger) error {
	logger.Info().Msg("ACM Status")

	if err := kubectl(cfg, "get", "namespace", cfg.Namespace).Run(); err != nil {
		logger.Info().Msgf("  Namespace %s: [NOT FOUND]", cfg.Namespace)
		return nil
	}
	logger.Info().Msgf("  Namespace %s: exists", cfg.Namespace)

	subState, err := kubectl(cfg, "get", "subscription.operators.coreos.com",
		"advanced-cluster-management", "-n", cfg.Namespace,
		"-o", "jsonpath={.status.state}").Output()
	if err != nil {
		logger.Info().Msg("  Subscription: [NOT FOUND]")
	} else {
		logger.Info().Msgf("  Subscription: %s", strings.TrimSpace(subState))
	}

	csvPhase, err := kubectl(cfg, "get", "csv", "-n", cfg.Namespace,
		"-o", `jsonpath={.items[?(@.spec.displayName=="Advanced Cluster Management for Kubernetes")].status.phase}`).Output()
	if err != nil {
		logger.Info().Msg("  CSV: [NOT FOUND]")
	} else {
		phase := strings.TrimSpace(csvPhase)
		if phase == "" {
			logger.Info().Msg("  CSV: [NOT FOUND]")
		} else {
			logger.Info().Msgf("  CSV: %s", phase)
		}
	}

	mchPhase, err := kubectl(cfg, "get", "mch", "multiclusterhub",
		"-n", cfg.Namespace, "-o", "jsonpath={.status.phase}").Output()
	if err != nil {
		logger.Info().Msg("  MultiClusterHub: [NOT FOUND]")
	} else {
		logger.Info().Msgf("  MultiClusterHub: %s", strings.TrimSpace(mchPhase))
	}

	mcStatus, err := kubectl(cfg, "get", "managedcluster", "local-cluster",
		"-o", `jsonpath={.status.conditions[?(@.type=="ManagedClusterConditionAvailable")].status}`).Output()
	if err != nil {
		logger.Info().Msg("  ManagedCluster local-cluster: [NOT FOUND]")
	} else {
		logger.Info().Msgf("  ManagedCluster local-cluster Available: %s", strings.TrimSpace(mcStatus))
	}

	return nil
}

func alreadyInstalled(cfg *Config) bool {
	if err := kubectl(cfg, "get", "namespace", cfg.Namespace).Run(); err != nil {
		return false
	}
	if err := kubectl(cfg, "get", "mch", "-n", cfg.Namespace).Run(); err != nil {
		return false
	}
	return true
}

func detectCSVName(cfg *Config) string {
	out, err := kubectl(cfg, "get", "csv", "-n", cfg.Namespace,
		"-o", `jsonpath={.items[?(@.spec.displayName=="Advanced Cluster Management for Kubernetes")].metadata.name}`).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func waitForCSV(ctx context.Context, cfg *Config, logger *zerolog.Logger) (string, error) {
	deadline := time.Now().Add(cfg.Timeout)
	for {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout waiting for ACM CSV to appear after %s", cfg.Timeout)
		}

		name := detectCSVName(cfg)
		if name != "" {
			return name, nil
		}

		logger.Info().Msg("CSV not yet available, waiting...")
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
}

func waitForMCH(ctx context.Context, cfg *Config, logger *zerolog.Logger) error {
	deadline := time.Now().Add(cfg.Timeout)
	for {
		if time.Now().After(deadline) {
			phase := currentMCHPhase(cfg)
			return fmt.Errorf("timeout waiting for MultiClusterHub to reach Running after %s (current phase: %s)", cfg.Timeout, phase)
		}

		phase := currentMCHPhase(cfg)
		if phase == "Running" {
			return nil
		}

		logger.Info().Msgf("MultiClusterHub phase: %s, waiting...", phase)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(15 * time.Second):
		}
	}
}

func waitForMCHDeletion(ctx context.Context, cfg *Config, logger *zerolog.Logger) error {
	deadline := time.Now().Add(cfg.Timeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for MultiClusterHub deletion after %s", cfg.Timeout)
		}

		if err := kubectl(cfg, "get", "mch", "multiclusterhub", "-n", cfg.Namespace).Run(); err != nil {
			return nil
		}

		logger.Info().Msg("MultiClusterHub still exists, waiting for deletion...")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func currentMCHPhase(cfg *Config) string {
	out, err := kubectl(cfg, "get", "mch", "multiclusterhub",
		"-n", cfg.Namespace, "-o", "jsonpath={.status.phase}").Output()
	if err != nil {
		return "Unknown"
	}
	phase := strings.TrimSpace(out)
	if phase == "" {
		return "Unknown"
	}
	return phase
}

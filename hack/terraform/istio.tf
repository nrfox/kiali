provider "kubectl" {
  load_config_file = true
}

resource "null_resource" "configure_kubectl" {
  provisioner "local-exec" {
    command     = "az aks get-credentials --resource-group ${azurerm_resource_group.default.name} --name ${azurerm_kubernetes_cluster.east.name} --admin --overwrite"
    interpreter = ["/bin/bash", "-c"]
  }
  depends_on = [azurerm_kubernetes_cluster.east]
}

resource "helm_release" "istio_base" {
  chart            = "base"
  description      = "Kube cluster: ${azurerm_kubernetes_cluster.east.name}"
  repository       = "https://istio-release.storage.googleapis.com/charts"
  name             = "istio-base"
  namespace        = "istio-system"
  atomic           = true
  create_namespace = true
  wait             = true
  wait_for_jobs    = true

  depends_on = [
    null_resource.configure_kubectl,
  ]
}

resource "helm_release" "istiod" {
  chart         = "istiod"
  repository    = "https://istio-release.storage.googleapis.com/charts"
  name          = "istiod"
  namespace     = helm_release.istio_base.namespace
  atomic        = true
  wait          = true
  wait_for_jobs = true

  values = [
    file("${path.module}/helm/istiod-values.yaml")
  ]
}

resource "helm_release" "ingress_gateway" {
  count         = 1
  chart         = "gateway"
  repository    = "https://istio-release.storage.googleapis.com/charts"
  name          = "ingress-gateway"
  namespace     = helm_release.istio_base.namespace
  atomic        = true
  wait          = true
  wait_for_jobs = true

  values = [
    file("${path.module}/helm/ingress-gateway-values.yaml")
  ]
}

# We're including kiali in the east/west gateway to save on IPs of which there is a limit.
resource "helm_release" "east_west_gateway" {
  count         = 0
  chart         = "gateway"
  repository    = "https://istio-release.storage.googleapis.com/charts"
  name          = "east-west-gateway"
  namespace     = helm_release.istio_base.namespace
  atomic        = true
  wait          = true
  wait_for_jobs = true

  values = [
    file("${path.module}/helm/east-west-gateway-values.yaml")
  ]
}

resource "helm_release" "prometheus" {
  atomic           = true
  create_namespace = true
  name             = "prometheus"
  namespace        = "prometheus"
  description      = "Kube cluster: ${azurerm_kubernetes_cluster.east.name}"
  repository       = "https://prometheus-community.github.io/helm-charts"
  chart            = "prometheus"
  wait             = true
  wait_for_jobs    = true

  values = [
    file("${path.module}/helm/prometheus-values.yaml")
  ]
}

data "azuread_application" "kiali" {
  application_id = var.kiali_app_id
}

## Kiali
resource "helm_release" "kiali" {
  atomic        = true
  name          = "kiali-operator"
  namespace     = helm_release.istio_base.namespace
  repository    = "https://kiali.org/helm-charts"
  chart         = "kiali-operator"
  wait          = true
  wait_for_jobs = true

  values = [
    file("${path.module}/helm/kiali-operator-values.yaml")
  ]

  set {
    name  = "cr.spec.external_services.prometheus.url"
    value = "http://prometheus-server.${helm_release.prometheus.namespace}.svc.cluster.local"
  }
  set {
    name  = "cr.spec.auth.openid.client_id"
    value = data.azuread_application.kiali.application_id
  }
  set {
    name  = "cr.spec.auth.openid.issuer_uri"
    value = "https://login.microsoftonline.com/${data.azuread_client_config.current.tenant_id}/v2.0"
  }

  provisioner "local-exec" {
    command     = "kubectl patch kialis kiali -n istio-system --type=merge -p '{\"metadata\": {\"finalizers\": null}}'"
    interpreter = ["/bin/bash", "-c"]
    when        = destroy
  }
}

resource "kubectl_manifest" "gateway_for_ingress" {
  yaml_body = <<YAML
apiVersion: networking.istio.io/v1beta1
kind: Gateway
metadata:
  name: ingress-gateway
  namespace: istio-system
spec:
  selector:
    istio: ingress-gateway
  servers:
  - hosts:
    - '*'
    port:
      name: http
      number: 80
      protocol: HTTP
  - hosts:
    - '*'
    port:
      name: https
      number: 443
      protocol: HTTPS
    tls:
      credentialName: kiali-tls
      mode: SIMPLE
YAML
}

resource "kubectl_manifest" "kiali_virtual_service" {
  yaml_body = <<YAML
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: kiali
  namespace: istio-system
spec:
  gateways:
  - ingress-gateway
  hosts:
  - '*'
  http:
  - match:
    - uri:
        prefix: /kiali
    route:
    - destination:
        host: kiali
        port:
          number: 20001
YAML
}

output "resource_group" {
  value = azurerm_resource_group.default.name
}

output "kubernetes_cluster" {
  value = azurerm_kubernetes_cluster.east.name
}

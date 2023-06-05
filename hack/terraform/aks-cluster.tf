
resource "random_pet" "prefix" {}

provider "azurerm" {
  features {}
}

resource "azurerm_resource_group" "default" {
  name     = "${random_pet.prefix.id}-rg"
  location = "East US 2"
}

data "azuread_client_config" "current" {}

resource "azuread_group" "kiali_admins" {
  display_name     = "kiali-admins"
  owners           = [data.azuread_client_config.current.object_id]
  members          = [data.azuread_client_config.current.object_id]
  security_enabled = true
}

resource "azurerm_kubernetes_cluster" "east" {
  name                = "east"
  location            = azurerm_resource_group.default.location
  resource_group_name = azurerm_resource_group.default.name
  dns_prefix          = "east"

  default_node_pool {
    name                        = "default"
    node_count                  = 1
    vm_size                     = "Standard_B2ms"
    os_disk_size_gb             = 30
    temporary_name_for_rotation = "temppool"
  }

  azure_active_directory_role_based_access_control {
    managed                = true
    admin_group_object_ids = [azuread_group.kiali_admins.object_id]
  }

  service_principal {
    client_id     = var.appId
    client_secret = var.password
  }
}

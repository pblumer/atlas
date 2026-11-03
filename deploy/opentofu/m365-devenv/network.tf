# ---------------------------------------------------------------------------
# The Azure side, shared by the Atlas server and the optional domain controller:
# one resource group and one virtual network, so the server reaches the domain
# controller over its private address and LDAPS never has to face the Internet.
# ---------------------------------------------------------------------------

locals {
  azure_count = (var.enable_atlas_server || var.enable_ad_dc) ? 1 : 0

  azure_tags = {
    purpose     = "atlas-m365-devenv"
    environment = local.env_id
    created     = time_static.created.rfc3339
    managed-by  = "opentofu"
  }
}

data "azurerm_client_config" "current" {
  count = local.azure_count
}

resource "azurerm_resource_group" "env" {
  count    = local.azure_count
  name     = "${local.env_id}-rg"
  location = var.azure_location
  tags     = local.azure_tags

  lifecycle {
    precondition {
      condition     = can(regex("^[0-9a-fA-F-]{36}$", var.azure_subscription_id))
      error_message = "The Atlas server and the domain controller run in Azure: set azure_subscription_id (or enable_atlas_server = false and enable_ad_dc = false)."
    }
  }
}

resource "azurerm_virtual_network" "env" {
  count               = local.azure_count
  name                = "${local.env_id}-vnet"
  location            = azurerm_resource_group.env[0].location
  resource_group_name = azurerm_resource_group.env[0].name
  address_space       = ["10.42.0.0/24"]
  tags                = local.azure_tags
}

resource "azurerm_subnet" "dc" {
  count                = local.ad_count
  name                 = "dc"
  resource_group_name  = azurerm_resource_group.env[0].name
  virtual_network_name = azurerm_virtual_network.env[0].name
  address_prefixes     = ["10.42.0.0/26"]
}

resource "azurerm_subnet" "app" {
  count                = local.atlas_count
  name                 = "app"
  resource_group_name  = azurerm_resource_group.env[0].name
  virtual_network_name = azurerm_virtual_network.env[0].name
  address_prefixes     = ["10.42.0.64/26"]
}

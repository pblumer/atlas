# ---------------------------------------------------------------------------
# Optional: a SharePoint Server Subscription Edition farm in the test domain. Needs the
# domain controller (enable_ad_dc): the server joins <ad_domain_name> as sp01, installs
# SQL Server Developer beside SharePoint and builds a single-server farm with a web
# application at http://sp01.<ad_domain_name> and a root site collection, unattended,
# over several reboots (scripts/sharepoint-setup.ps1). scripts/wait-sharepoint.sh
# follows it.
#
# The server is reachable inside the virtual network only. Its public address serves the
# downloads and, with ad_allow_rdp, RDP from ad_allowed_cidrs. SharePoint runs without a
# product key as a 180-day evaluation.
#
# The image needs no marketplace plan block as far as its public use shows: the same
# publisher's images are deployed without one. README, «SharePoint Server», names the
# one-line check.
# ---------------------------------------------------------------------------

locals {
  # The guard below refuses the server without the domain controller; the resources
  # themselves count on both, so the plan reaches that refusal instead of failing on
  # the controller's missing subnet first.
  sharepoint_count      = var.enable_sharepoint_server && var.enable_ad_dc ? 1 : 0
  sharepoint_private_ip = "10.42.0.12"
  sharepoint_fqdn       = "sp01.${var.ad_domain_name}"
  sharepoint_url        = "http://${local.sharepoint_fqdn}"
  # sAMAccountName allows 20 characters; the suffix is four.
  sharepoint_farm_account    = "svc-sp-farm-${random_string.suffix.result}"
  sharepoint_app_account     = "svc-sp-app-${random_string.suffix.result}"
  sharepoint_runbook_account = "svc-sp-run-${random_string.suffix.result}"
}

resource "terraform_data" "sharepoint_guard" {
  count = var.enable_sharepoint_server ? 1 : 0

  lifecycle {
    precondition {
      condition     = var.enable_ad_dc
      error_message = "enable_sharepoint_server = true needs the domain controller (enable_ad_dc = true): SharePoint Server lives in that domain."
    }
    # The schedule below follows the domain controller's time. Every VM of this
    # environment stops at night, so an empty time must not build one that keeps running.
    precondition {
      condition     = var.ad_auto_shutdown_time != ""
      error_message = "enable_sharepoint_server = true needs ad_auto_shutdown_time: the SharePoint server shuts down with the domain controller, and it must not run through the night."
    }
  }
}

resource "random_password" "sharepoint" {
  for_each = local.sharepoint_count == 1 ? toset(["farm", "app", "runbook", "passphrase"]) : toset([])

  length           = 24
  special          = true
  override_special = "-_@"
  min_upper        = 2
  min_lower        = 2
  min_numeric      = 2
  min_special      = 1
}

resource "azurerm_network_security_group" "sharepoint" {
  count               = local.sharepoint_count
  name                = "${local.env_id}-sp-nsg"
  location            = azurerm_resource_group.env[0].location
  resource_group_name = azurerm_resource_group.env[0].name
  tags                = local.azure_tags

  # Nothing from the Internet but RDP, if asked for; the Atlas server and the domain
  # controller reach the server inside the virtual network (AllowVnetInBound).
  dynamic "security_rule" {
    for_each = var.ad_allow_rdp && length(var.ad_allowed_cidrs) > 0 ? [1] : []
    content {
      name                       = "rdp-from-allowed"
      priority                   = 110
      direction                  = "Inbound"
      access                     = "Allow"
      protocol                   = "Tcp"
      source_port_range          = "*"
      destination_port_range     = "3389"
      source_address_prefixes    = var.ad_allowed_cidrs
      destination_address_prefix = "*"
    }
  }
}

resource "azurerm_public_ip" "sharepoint" {
  count               = local.sharepoint_count
  name                = "${local.env_id}-sp-pip"
  location            = azurerm_resource_group.env[0].location
  resource_group_name = azurerm_resource_group.env[0].name
  allocation_method   = "Static"
  sku                 = "Standard"
  tags                = local.azure_tags
}

resource "azurerm_network_interface" "sharepoint" {
  count               = local.sharepoint_count
  name                = "${local.env_id}-sp-nic"
  location            = azurerm_resource_group.env[0].location
  resource_group_name = azurerm_resource_group.env[0].name
  # The domain controller is the server's DNS: joining the domain needs its SRV records,
  # and the web application's name is the A record the joined server registers there.
  dns_servers = [local.ad_private_ip]
  tags        = local.azure_tags

  ip_configuration {
    name                          = "primary"
    subnet_id                     = azurerm_subnet.dc[0].id
    private_ip_address_allocation = "Static"
    private_ip_address            = local.sharepoint_private_ip
    public_ip_address_id          = azurerm_public_ip.sharepoint[0].id
  }
}

resource "azurerm_network_interface_security_group_association" "sharepoint" {
  count                     = local.sharepoint_count
  network_interface_id      = azurerm_network_interface.sharepoint[0].id
  network_security_group_id = azurerm_network_security_group.sharepoint[0].id
}

resource "azurerm_windows_virtual_machine" "sharepoint" {
  count                 = local.sharepoint_count
  name                  = "${local.env_id}-sp"
  computer_name         = "sp01"
  location              = azurerm_resource_group.env[0].location
  resource_group_name   = azurerm_resource_group.env[0].name
  size                  = var.sharepoint_server_vm_size
  admin_username        = "atlasadmin"
  admin_password        = random_password.ad["admin"].result
  network_interface_ids = [azurerm_network_interface.sharepoint[0].id]
  provision_vm_agent    = true
  # The setup script, as for the domain controller: no secret in it.
  custom_data = base64encode(file("${path.module}/scripts/sharepoint-setup.ps1"))
  tags        = local.azure_tags

  os_disk {
    caching              = "ReadWrite"
    storage_account_type = "StandardSSD_LRS"
    # SharePoint, SQL Server, their databases and the installation image.
    disk_size_gb = 256
  }

  source_image_reference {
    publisher = "MicrosoftSharePoint"
    offer     = "MicrosoftSharePointServer"
    sku       = "spse"
    version   = "latest"
  }

  depends_on = [azurerm_virtual_machine_extension.ad_setup, terraform_data.sharepoint_guard]

  # The script runs once, from the first boot on; a later change to it must not replace
  # a server whose farm took hours to build.
  lifecycle {
    ignore_changes = [custom_data]
  }
}

locals {
  sharepoint_setup_config = local.sharepoint_count == 1 ? base64encode(jsonencode({
    domainName      = var.ad_domain_name
    netbiosName     = var.ad_netbios_name
    domainAdmin     = "atlasadmin"
    domainAdminPass = random_password.ad["admin"].result
    testOu          = local.ad_test_ou_dn
    fqdn            = local.sharepoint_fqdn
    webAppUrl       = local.sharepoint_url
    farmAccount     = local.sharepoint_farm_account
    farmPassword    = random_password.sharepoint["farm"].result
    appAccount      = local.sharepoint_app_account
    appPassword     = random_password.sharepoint["app"].result
    runbookAccount  = local.sharepoint_runbook_account
    runbookPassword = random_password.sharepoint["runbook"].result
    passphrase      = random_password.sharepoint["passphrase"].result
  })) : ""
}

# Runs the setup script once; it carries on by itself through four phases and their
# reboots (scripts/sharepoint-setup.ps1). The extension reports success after phase 1.
resource "azurerm_virtual_machine_extension" "sharepoint_setup" {
  count                      = local.sharepoint_count
  name                       = "atlas-sharepoint-setup"
  virtual_machine_id         = azurerm_windows_virtual_machine.sharepoint[0].id
  publisher                  = "Microsoft.Compute"
  type                       = "CustomScriptExtension"
  type_handler_version       = "1.10"
  auto_upgrade_minor_version = true
  tags                       = local.azure_tags

  protected_settings = jsonencode({
    commandToExecute = "powershell.exe -NoProfile -ExecutionPolicy Bypass -Command \"New-Item -ItemType Directory -Force -Path C:\\AtlasSetup | Out-Null; Copy-Item -Force C:\\AzureData\\CustomData.bin C:\\AtlasSetup\\sharepoint-setup.ps1; & C:\\AtlasSetup\\sharepoint-setup.ps1 -Phase 1 -ConfigB64 ${local.sharepoint_setup_config}\""
  })

  timeouts {
    create = "60m"
  }

  # Azure stores the "created" tag in its own date format. The settings hold the setup's
  # input only: a later change must not run the extension over a finished farm again.
  lifecycle {
    ignore_changes = [tags, protected_settings]
  }
}

resource "azurerm_dev_test_global_vm_shutdown_schedule" "sharepoint" {
  count                 = local.sharepoint_count == 1 && var.ad_auto_shutdown_time != "" ? 1 : 0
  virtual_machine_id    = azurerm_windows_virtual_machine.sharepoint[0].id
  location              = azurerm_resource_group.env[0].location
  enabled               = true
  daily_recurrence_time = var.ad_auto_shutdown_time
  timezone              = "UTC"
  tags                  = local.azure_tags

  notification_settings {
    enabled = false
  }
}

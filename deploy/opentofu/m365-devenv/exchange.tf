# ---------------------------------------------------------------------------
# Optional: an Exchange Server SE in the test domain, for the Exchange worker's
# on-premises backend (ADR-0462, target "onprem"). Needs the domain controller
# (enable_ad_dc): the server joins <ad_domain_name> as exch01, prepares the schema and
# the domain and installs the Mailbox role, unattended, over several reboots
# (scripts/exchange-setup.ps1). That takes one to two hours after the apply;
# scripts/wait-exchange.sh follows it.
#
# The server is reachable inside the virtual network only (Remote PowerShell at
# http://exch01.<ad_domain_name>/PowerShell, Kerberos). Its public address serves the
# downloads (the 6 GB installation image) and, with ad_allow_rdp, RDP from
# ad_allowed_cidrs. Exchange Server SE runs without a product key as a trial.
# ---------------------------------------------------------------------------

locals {
  # The guard below refuses the server without the domain controller; the resources
  # themselves count on both, so the plan reaches that refusal instead of failing on
  # the controller's missing subnet first.
  exchange_count           = var.enable_exchange_onprem && var.enable_ad_dc ? 1 : 0
  exchange_private_ip      = "10.42.0.11"
  exchange_fqdn            = "exch01.${var.ad_domain_name}"
  exchange_service_account = "svc-exch-${random_string.suffix.result}"
}

resource "terraform_data" "exchange_guard" {
  count = var.enable_exchange_onprem ? 1 : 0

  lifecycle {
    precondition {
      condition     = var.enable_ad_dc
      error_message = "enable_exchange_onprem = true needs the domain controller (enable_ad_dc = true): Exchange Server lives in that domain."
    }
  }
}

resource "random_password" "exchange_service" {
  count            = local.exchange_count
  length           = 24
  special          = true
  override_special = "-_@"
  min_upper        = 2
  min_lower        = 2
  min_numeric      = 2
  min_special      = 1
}

resource "azurerm_network_security_group" "exchange" {
  count               = local.exchange_count
  name                = "${local.env_id}-exch-nsg"
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

resource "azurerm_public_ip" "exchange" {
  count               = local.exchange_count
  name                = "${local.env_id}-exch-pip"
  location            = azurerm_resource_group.env[0].location
  resource_group_name = azurerm_resource_group.env[0].name
  allocation_method   = "Static"
  sku                 = "Standard"
  tags                = local.azure_tags
}

resource "azurerm_network_interface" "exchange" {
  count               = local.exchange_count
  name                = "${local.env_id}-exch-nic"
  location            = azurerm_resource_group.env[0].location
  resource_group_name = azurerm_resource_group.env[0].name
  # The domain controller is the server's DNS: joining the domain and Exchange's own
  # lookups need the domain's SRV records.
  dns_servers = [local.ad_private_ip]
  tags        = local.azure_tags

  ip_configuration {
    name                          = "primary"
    subnet_id                     = azurerm_subnet.dc[0].id
    private_ip_address_allocation = "Static"
    private_ip_address            = local.exchange_private_ip
    public_ip_address_id          = azurerm_public_ip.exchange[0].id
  }
}

resource "azurerm_network_interface_security_group_association" "exchange" {
  count                     = local.exchange_count
  network_interface_id      = azurerm_network_interface.exchange[0].id
  network_security_group_id = azurerm_network_security_group.exchange[0].id
}

resource "azurerm_windows_virtual_machine" "exchange" {
  count                 = local.exchange_count
  name                  = "${local.env_id}-exch"
  computer_name         = "exch01"
  location              = azurerm_resource_group.env[0].location
  resource_group_name   = azurerm_resource_group.env[0].name
  size                  = var.exchange_vm_size
  admin_username        = "atlasadmin"
  admin_password        = random_password.ad["admin"].result
  network_interface_ids = [azurerm_network_interface.exchange[0].id]
  provision_vm_agent    = true
  # The setup script, as for the domain controller: no secret in it.
  custom_data = base64encode(file("${path.module}/scripts/exchange-setup.ps1"))
  tags        = local.azure_tags

  os_disk {
    caching              = "ReadWrite"
    storage_account_type = "StandardSSD_LRS"
    # Exchange, its databases and the 6 GB installation image.
    disk_size_gb = 256
  }

  source_image_reference {
    publisher = "MicrosoftWindowsServer"
    offer     = "WindowsServer"
    sku       = "2022-datacenter-g2"
    version   = "latest"
  }

  depends_on = [azurerm_virtual_machine_extension.ad_setup]

  # The script runs once, from the first boot on; a later change to it must not replace
  # a server that took two hours to install.
  lifecycle {
    ignore_changes = [custom_data]
  }
}

locals {
  exchange_setup_config = local.exchange_count == 1 ? base64encode(jsonencode({
    domainName      = var.ad_domain_name
    netbiosName     = var.ad_netbios_name
    domainAdmin     = "atlasadmin"
    domainAdminPass = random_password.ad["admin"].result
    organization    = "AtlasTest"
    isoUrl          = var.exchange_iso_url
    isoBytes        = var.exchange_iso_bytes
    serviceAccount  = local.exchange_service_account
    servicePassword = random_password.exchange_service[0].result
    serviceOu       = "OU=AtlasService,${local.ad_domain_dn}"
    testOu          = local.ad_test_ou_dn
    # A hybrid organization signs its people in with one UPN in both directories (AD
    # Connect). The test users get the tenant's, so a person the order form finds in
    # Entra is the same person to Exchange Server.
    tenantDomain = local.initial_domain
  })) : ""
}

# Runs the setup script once; it carries on by itself through four phases and their
# reboots (scripts/exchange-setup.ps1). The extension reports success after phase 1.
resource "azurerm_virtual_machine_extension" "exchange_setup" {
  count                      = local.exchange_count
  name                       = "atlas-exchange-setup"
  virtual_machine_id         = azurerm_windows_virtual_machine.exchange[0].id
  publisher                  = "Microsoft.Compute"
  type                       = "CustomScriptExtension"
  type_handler_version       = "1.10"
  auto_upgrade_minor_version = true
  tags                       = local.azure_tags

  protected_settings = jsonencode({
    commandToExecute = "powershell.exe -NoProfile -ExecutionPolicy Bypass -Command \"New-Item -ItemType Directory -Force -Path C:\\AtlasSetup | Out-Null; Copy-Item -Force C:\\AzureData\\CustomData.bin C:\\AtlasSetup\\exchange-setup.ps1; & C:\\AtlasSetup\\exchange-setup.ps1 -Phase 1 -ConfigB64 ${local.exchange_setup_config}\""
  })

  timeouts {
    create = "60m"
  }

  lifecycle {
    ignore_changes = [tags]
  }
}

resource "azurerm_dev_test_global_vm_shutdown_schedule" "exchange" {
  count                 = local.exchange_count == 1 && var.exchange_auto_shutdown_time != "" ? 1 : 0
  virtual_machine_id    = azurerm_windows_virtual_machine.exchange[0].id
  location              = azurerm_resource_group.env[0].location
  enabled               = true
  daily_recurrence_time = var.exchange_auto_shutdown_time
  timezone              = "UTC"
  tags                  = local.azure_tags

  notification_settings {
    enabled = false
  }
}

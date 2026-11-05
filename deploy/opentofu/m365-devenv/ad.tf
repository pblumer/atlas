# ---------------------------------------------------------------------------
# Optional: a single Active Directory domain controller in Azure for the AD worker
# (ADR-0166). The Atlas server reaches it over LDAPS inside the virtual network as
# dc01.<ad_domain_name>; from outside only the addresses in var.ad_allowed_cidrs may.
#
# The LDAPS certificate is issued by Key Vault (self-signed, Server Authentication,
# the public DNS name in its subject and SAN) and installed into the VM's
# LocalMachine\My store at provisioning, before the forest is created; Active
# Directory picks it up for port 636 on the reboot that follows promotion. Its public
# part goes into the Atlas server's trust store and to out/ad-ldaps.pem for any other
# worker host - the AD worker verifies against the host's trust store and has no
# option to skip verification.
# ---------------------------------------------------------------------------

locals {
  ad_count     = var.enable_ad_dc ? 1 : 0
  ad_domain_dn = join(",", [for p in split(".", var.ad_domain_name) : "DC=${p}"])
  ad_dns_label = "${local.env_id}-dc"
  ad_public_fqdn = (
    var.enable_ad_dc ? "${local.ad_dns_label}.${var.azure_location}.cloudapp.azure.com" : null
  )
  ad_service_account = "svc-atlas-${random_string.suffix.result}"
  ad_bind_dn         = "CN=${local.ad_service_account},OU=AtlasService,${local.ad_domain_dn}"
  ad_test_ou_dn      = "OU=AtlasTest,${local.ad_domain_dn}"
  ad_private_ip      = "10.42.0.10"
}

resource "random_string" "kv" {
  count   = local.ad_count
  length  = 6
  upper   = false
  special = false
}

resource "random_password" "ad" {
  for_each = var.enable_ad_dc ? toset(["admin", "safemode", "service", "users"]) : toset([])

  length           = 24
  special          = true
  override_special = "-_@"
  min_upper        = 2
  min_lower        = 2
  min_numeric      = 2
  min_special      = 1
}

# Without an Atlas server in the same network, the AD worker runs somewhere else and
# reaches the domain controller from outside: then the allowed addresses are required.
resource "terraform_data" "ad_guard" {
  count = local.ad_count

  lifecycle {
    precondition {
      condition     = var.enable_atlas_server || length(var.ad_allowed_cidrs) > 0
      error_message = "enable_ad_dc = true without the Atlas server needs ad_allowed_cidrs: the public address(es) of the Atlas worker host, e.g. [\"203.0.113.7/32\"]."
    }
    precondition {
      condition     = length(var.ad_netbios_name) <= 15 && can(regex("^[A-Z][A-Z0-9-]*$", var.ad_netbios_name))
      error_message = "ad_netbios_name: at most 15 upper-case letters, digits or hyphens."
    }
  }
}

resource "azurerm_network_security_group" "ad" {
  count               = local.ad_count
  name                = "${local.env_id}-dc-nsg"
  location            = azurerm_resource_group.env[0].location
  resource_group_name = azurerm_resource_group.env[0].name
  tags                = local.azure_tags

  # LDAPS from outside only for the addresses named; the Atlas server reaches the
  # domain controller inside the virtual network (AllowVnetInBound).
  dynamic "security_rule" {
    for_each = length(var.ad_allowed_cidrs) > 0 ? [1] : []
    content {
      name                       = "ldaps-from-allowed"
      priority                   = 100
      direction                  = "Inbound"
      access                     = "Allow"
      protocol                   = "Tcp"
      source_port_range          = "*"
      destination_port_range     = "636"
      source_address_prefixes    = var.ad_allowed_cidrs
      destination_address_prefix = "*"
    }
  }

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

resource "azurerm_public_ip" "ad" {
  count               = local.ad_count
  name                = "${local.env_id}-dc-pip"
  location            = azurerm_resource_group.env[0].location
  resource_group_name = azurerm_resource_group.env[0].name
  allocation_method   = "Static"
  sku                 = "Standard"
  domain_name_label   = local.ad_dns_label
  tags                = local.azure_tags
}

resource "azurerm_network_interface" "ad" {
  count               = local.ad_count
  name                = "${local.env_id}-dc-nic"
  location            = azurerm_resource_group.env[0].location
  resource_group_name = azurerm_resource_group.env[0].name
  tags                = local.azure_tags

  ip_configuration {
    name                          = "primary"
    subnet_id                     = azurerm_subnet.dc[0].id
    private_ip_address_allocation = "Static"
    private_ip_address            = local.ad_private_ip
    public_ip_address_id          = azurerm_public_ip.ad[0].id
  }
}

resource "azurerm_network_interface_security_group_association" "ad" {
  count                     = local.ad_count
  network_interface_id      = azurerm_network_interface.ad[0].id
  network_security_group_id = azurerm_network_security_group.ad[0].id
}

resource "azurerm_key_vault" "ad" {
  count                      = local.ad_count
  name                       = "atlaskv${random_string.suffix.result}${random_string.kv[0].result}"
  location                   = azurerm_resource_group.env[0].location
  resource_group_name        = azurerm_resource_group.env[0].name
  tenant_id                  = data.azurerm_client_config.current[0].tenant_id
  sku_name                   = "standard"
  enabled_for_deployment     = true
  purge_protection_enabled   = false
  soft_delete_retention_days = 7
  tags                       = local.azure_tags

  access_policy {
    tenant_id               = data.azurerm_client_config.current[0].tenant_id
    object_id               = data.azurerm_client_config.current[0].object_id
    certificate_permissions = ["Create", "Delete", "Get", "Import", "List", "Purge", "Recover", "Update"]
    secret_permissions      = ["Delete", "Get", "List", "Purge", "Recover", "Set"]
    key_permissions         = ["Create", "Delete", "Get", "List", "Purge", "Recover"]
  }
}

resource "azurerm_key_vault_certificate" "ldaps" {
  count        = local.ad_count
  name         = "ldaps-dc"
  key_vault_id = azurerm_key_vault.ad[0].id
  tags         = local.azure_tags

  certificate_policy {
    issuer_parameters {
      name = "Self"
    }

    key_properties {
      exportable = true
      key_size   = 2048
      key_type   = "RSA"
      reuse_key  = false
    }

    # PKCS#12 is what a Windows VM installs from Key Vault into a certificate store.
    secret_properties {
      content_type = "application/x-pkcs12"
    }

    x509_certificate_properties {
      subject            = "CN=${local.ad_public_fqdn}"
      validity_in_months = 12
      key_usage          = ["digitalSignature", "keyEncipherment"]
      extended_key_usage = ["1.3.6.1.5.5.7.3.1"] # Server Authentication

      subject_alternative_names {
        dns_names = [
          local.ad_public_fqdn,
          "dc01.${var.ad_domain_name}",
          var.ad_domain_name,
        ]
      }
    }
  }
}

resource "azurerm_windows_virtual_machine" "ad" {
  count                 = local.ad_count
  name                  = "${local.env_id}-dc"
  computer_name         = "dc01"
  location              = azurerm_resource_group.env[0].location
  resource_group_name   = azurerm_resource_group.env[0].name
  size                  = var.ad_vm_size
  admin_username        = "atlasadmin"
  admin_password        = random_password.ad["admin"].result
  network_interface_ids = [azurerm_network_interface.ad[0].id]
  provision_vm_agent    = true
  # The setup script; Windows places it in C:\AzureData\CustomData.bin, where the
  # Custom Script Extension below picks it up. It holds no secret - those arrive in the
  # extension's protected settings.
  custom_data = base64encode(file("${path.module}/scripts/ad-setup.ps1"))
  tags        = local.azure_tags

  os_disk {
    caching              = "ReadWrite"
    storage_account_type = "StandardSSD_LRS"
  }

  source_image_reference {
    publisher = "MicrosoftWindowsServer"
    offer     = "WindowsServer"
    sku       = "2022-datacenter-g2"
    version   = "latest"
  }

  secret {
    key_vault_id = azurerm_key_vault.ad[0].id
    certificate {
      store = "My"
      url   = azurerm_key_vault_certificate.ldaps[0].secret_id
    }
  }

  # The script runs once, at provisioning; a later change to it (phase 3 for
  # examples/ad-dienste) must not replace a domain controller and its forest. A running
  # one gets the change through Run Command (README, «Beispiele einspielen»).
  lifecycle {
    ignore_changes = [custom_data]
  }
}

locals {
  ad_setup_config = var.enable_ad_dc ? base64encode(jsonencode({
    domainName       = var.ad_domain_name
    netbiosName      = var.ad_netbios_name
    safeModePassword = random_password.ad["safemode"].result
    serviceAccount   = local.ad_service_account
    servicePassword  = random_password.ad["service"].result
    testUserPassword = random_password.ad["users"].result
    groupName        = "${local.env_id}-gruppe"
    ldapsSubject     = "CN=${local.ad_public_fqdn}"
    testUsers = [
      for k, u in var.test_users : {
        sam       = substr(lower("${random_string.suffix.result}-${k}"), 0, 20)
        upn       = "${local.env_id}-${k}@${var.ad_domain_name}"
        givenName = u.given_name
        surname   = u.surname
      }
    ]
  })) : ""
}

# Runs the setup script once: installs AD DS, creates the forest, registers the
# post-reboot phase (OUs, service account and its delegation, test users) as a
# start-up task, and reboots. The extension reports success before the reboot; the
# domain controller is ready some 10-20 minutes later (scripts/wait-ad.sh).
resource "azurerm_virtual_machine_extension" "ad_setup" {
  count                      = local.ad_count
  name                       = "atlas-ad-setup"
  virtual_machine_id         = azurerm_windows_virtual_machine.ad[0].id
  publisher                  = "Microsoft.Compute"
  type                       = "CustomScriptExtension"
  type_handler_version       = "1.10"
  auto_upgrade_minor_version = true
  tags                       = local.azure_tags

  protected_settings = jsonencode({
    commandToExecute = "powershell.exe -NoProfile -ExecutionPolicy Bypass -Command \"New-Item -ItemType Directory -Force -Path C:\\AtlasSetup | Out-Null; Copy-Item -Force C:\\AzureData\\CustomData.bin C:\\AtlasSetup\\ad-setup.ps1; & C:\\AtlasSetup\\ad-setup.ps1 -Phase 1 -ConfigB64 ${local.ad_setup_config}\""
  })

  timeouts {
    create = "60m"
  }

  # Azure stores the "created" tag of an extension in its own date format, which would
  # show as a change on every plan.
  lifecycle {
    ignore_changes = [tags]
  }
}

resource "azurerm_dev_test_global_vm_shutdown_schedule" "ad" {
  count                 = var.enable_ad_dc && var.ad_auto_shutdown_time != "" ? 1 : 0
  virtual_machine_id    = azurerm_windows_virtual_machine.ad[0].id
  location              = azurerm_resource_group.env[0].location
  enabled               = true
  daily_recurrence_time = var.ad_auto_shutdown_time
  timezone              = "UTC"
  tags                  = local.azure_tags

  notification_settings {
    enabled = false
  }
}

# The LDAPS certificate's public part, for the worker host's trust store.
resource "local_file" "ad_ldaps_cert" {
  count           = local.ad_count
  filename        = "${local.output_dir}/ad-ldaps.pem"
  file_permission = "0644"
  content = join("\n", concat(
    ["-----BEGIN CERTIFICATE-----"],
    regexall(".{1,64}", azurerm_key_vault_certificate.ldaps[0].certificate_data_base64),
    ["-----END CERTIFICATE-----", ""],
  ))
}

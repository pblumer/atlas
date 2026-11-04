# ---------------------------------------------------------------------------
# The Atlas server: an Ubuntu 24.04 VM running `atlas serve` behind Caddy (TLS),
# with PowerShell 7 and the MicrosoftTeams module for the Teams Phone worker.
# Atlas supervises its workers itself; the bootstrap creates them in the Console
# (vault bundle + worker record), so they are visible and editable there and a
# deployed model finds them. See README.md, "Atlas-Server".
# ---------------------------------------------------------------------------

locals {
  atlas_count     = var.enable_atlas_server ? 1 : 0
  atlas_dns_label = "${local.env_id}-atlas"
  atlas_fqdn      = var.enable_atlas_server ? "${local.atlas_dns_label}.${var.azure_location}.cloudapp.azure.com" : null
  atlas_url       = var.enable_atlas_server ? "https://${local.atlas_fqdn}" : null
  atlas_sso_count = var.enable_atlas_server && var.atlas_sso ? 1 : 0
}

resource "terraform_data" "atlas_guard" {
  count = local.atlas_count

  lifecycle {
    precondition {
      condition     = length(var.atlas_allowed_cidrs) > 0
      error_message = "enable_atlas_server = true needs atlas_allowed_cidrs: your own public address, e.g. [\"203.0.113.7/32\"], for HTTPS and SSH."
    }
  }
}

resource "random_password" "atlas_admin" {
  count            = local.atlas_count
  length           = 24
  special          = true
  override_special = "-_@"
  min_upper        = 2
  min_lower        = 2
  min_numeric      = 2
  min_special      = 1
}

# --- Sign-in to Atlas with the test tenant's accounts (OpenID Connect) ------

resource "azuread_application" "atlas_sso" {
  count                   = local.atlas_sso_count
  display_name            = "${local.env_id}-atlas-anmeldung"
  sign_in_audience        = "AzureADMyOrg"
  notes                   = local.note
  owners                  = local.app_owners
  group_membership_claims = ["SecurityGroup"]

  web {
    redirect_uris = ["${local.atlas_url}/auth/oidc/callback"]
    implicit_grant {
      access_token_issuance_enabled = false
      id_token_issuance_enabled     = false
    }
  }

  required_resource_access {
    resource_app_id = local.msgraph_app_id
    dynamic "resource_access" {
      for_each = ["openid", "profile", "email"]
      content {
        id   = data.azuread_service_principal.msgraph.oauth2_permission_scope_ids[resource_access.value]
        type = "Scope"
      }
    }
  }
}

resource "azuread_service_principal" "atlas_sso" {
  count     = local.atlas_sso_count
  client_id = azuread_application.atlas_sso[0].client_id
  owners    = local.app_owners
  notes     = local.note
}

# Admin consent for the sign-in scopes, so no test user is asked to consent.
resource "azuread_service_principal_delegated_permission_grant" "atlas_sso" {
  count                                = local.atlas_sso_count
  service_principal_object_id          = azuread_service_principal.atlas_sso[0].object_id
  resource_service_principal_object_id = data.azuread_service_principal.msgraph.object_id
  claim_values                         = ["openid", "profile", "email"]
}

resource "azuread_application_password" "atlas_sso" {
  count          = local.atlas_sso_count
  application_id = azuread_application.atlas_sso[0].id
  display_name   = "atlas-oidc"
  end_date       = time_offset.credential_expiry.rfc3339
}

# --- SSH key for the operator -----------------------------------------------

resource "tls_private_key" "atlas_ssh" {
  count     = local.atlas_count
  algorithm = "RSA"
  rsa_bits  = 4096
}

resource "local_sensitive_file" "atlas_ssh_key" {
  count           = local.atlas_count
  filename        = "${local.output_dir}/ssh/atlas_rsa"
  file_permission = "0600"
  content         = tls_private_key.atlas_ssh[0].private_key_openssh
}

# --- Network ---------------------------------------------------------------

resource "azurerm_network_security_group" "atlas" {
  count               = local.atlas_count
  name                = "${local.env_id}-atlas-nsg"
  location            = azurerm_resource_group.env[0].location
  resource_group_name = azurerm_resource_group.env[0].name
  tags                = local.azure_tags

  security_rule {
    name                       = "ssh-from-allowed"
    priority                   = 100
    direction                  = "Inbound"
    access                     = "Allow"
    protocol                   = "Tcp"
    source_port_range          = "*"
    destination_port_range     = "22"
    source_address_prefixes    = var.atlas_allowed_cidrs
    destination_address_prefix = "*"
  }

  # internal: HTTPS only for the allowed addresses. letsencrypt: 80 and 443 for
  # everybody, because the certificate authority validates from unpublished addresses.
  security_rule {
    name                       = "https"
    priority                   = 110
    direction                  = "Inbound"
    access                     = "Allow"
    protocol                   = "Tcp"
    source_port_range          = "*"
    destination_port_range     = "443"
    source_address_prefix      = var.atlas_tls == "letsencrypt" ? "*" : null
    source_address_prefixes    = var.atlas_tls == "letsencrypt" ? null : var.atlas_allowed_cidrs
    destination_address_prefix = "*"
  }

  dynamic "security_rule" {
    for_each = var.atlas_tls == "letsencrypt" ? [1] : []
    content {
      name                       = "http-acme"
      priority                   = 120
      direction                  = "Inbound"
      access                     = "Allow"
      protocol                   = "Tcp"
      source_port_range          = "*"
      destination_port_range     = "80"
      source_address_prefix      = "*"
      destination_address_prefix = "*"
    }
  }
}

resource "azurerm_public_ip" "atlas" {
  count               = local.atlas_count
  name                = "${local.env_id}-atlas-pip"
  location            = azurerm_resource_group.env[0].location
  resource_group_name = azurerm_resource_group.env[0].name
  allocation_method   = "Static"
  sku                 = "Standard"
  domain_name_label   = local.atlas_dns_label
  tags                = local.azure_tags
}

resource "azurerm_network_interface" "atlas" {
  count               = local.atlas_count
  name                = "${local.env_id}-atlas-nic"
  location            = azurerm_resource_group.env[0].location
  resource_group_name = azurerm_resource_group.env[0].name
  tags                = local.azure_tags

  ip_configuration {
    name                          = "primary"
    subnet_id                     = azurerm_subnet.app[0].id
    private_ip_address_allocation = "Dynamic"
    public_ip_address_id          = azurerm_public_ip.atlas[0].id
  }
}

resource "azurerm_network_interface_security_group_association" "atlas" {
  count                     = local.atlas_count
  network_interface_id      = azurerm_network_interface.atlas[0].id
  network_security_group_id = azurerm_network_security_group.atlas[0].id
}

# --- The VM and what it is given at first boot ------------------------------

locals {
  # The server's environment (atlas.service, EnvironmentFile). Worker credentials are
  # not in it: they go into the Console through the bootstrap.
  atlas_env = var.enable_atlas_server ? join("\n", concat(
    [
      "ATLAS_ADMIN_USERNAME=admin",
      "ATLAS_ADMIN_PASSWORD=${random_password.atlas_admin[0].result}",
      "ATLAS_EXTERNAL_URL=${local.atlas_url}",
      "ATLAS_TRUSTED_PROXIES=127.0.0.1",
      "HOME=/var/lib/atlas",
      "POWERSHELL_TELEMETRY_OPTOUT=1",
      "POWERSHELL_UPDATECHECK=Off",
      "DOTNET_CLI_TELEMETRY_OPTOUT=1",
    ],
    var.atlas_sso ? [
      "ATLAS_OIDC_ISSUER=https://login.microsoftonline.com/${var.tenant_id}/v2.0",
      "ATLAS_OIDC_CLIENT_ID=${azuread_application.atlas_sso[0].client_id}",
      "ATLAS_OIDC_CLIENT_SECRET=${azuread_application_password.atlas_sso[0].value}",
      # Quoted: systemd keeps the value's spaces only inside quotes for certain.
      "ATLAS_OIDC_NAME=\"Microsoft (${local.initial_domain})\"",
    ] : [],
    # Without a domain controller the AD worker serves a mock directory (ADR-0181).
    var.enable_ad_dc ? [] : ["ATLAS_AD_MOCK=1"],
    [""],
  )) : ""

  atlas_bootstrap = var.enable_atlas_server ? jsonencode({
    baseUrl       = "http://127.0.0.1:8080"
    adminUsername = "admin"
    adminPassword = random_password.atlas_admin[0].result
    workers = [for w in local.worker_defs : {
      name       = w.name
      kind       = w.kind
      endpoint   = w.endpoint
      provider   = w.provider
      sender     = w.sender
      secretName = "m365-${w.name}"
      secret     = w.bundle
    }]
    # An API token sealed in the vault as "atlas": what examples/m365-dienste expects.
    apiToken = { name = "${local.env_id} processes", scope = "full", vaultSecret = "atlas", expiresInDays = var.credential_validity_days }
  }) : ""

  # atlas-setup.sh sources this file: every value is single-quoted, or a value with a
  # space (AD_HOSTS_LINE) runs its second word as a command and aborts the setup.
  atlas_setup_env = join("\n", concat([
    for k, v in {
      ATLAS_INSTALL     = var.atlas_install
      ATLAS_VERSION     = var.atlas_version
      ATLAS_SOURCE_REPO = var.atlas_source_repo
      ATLAS_SOURCE_REF  = var.atlas_source_ref
      WITH_TEAMS        = var.workers.teamsphone ? "1" : "0"
      AD_HOSTS_LINE     = var.enable_ad_dc ? "${local.ad_private_ip} ${local.ad_internal_host}" : ""
    } : "${k}='${replace(v, "'", "'\\''")}'"
  ], [""]))

  caddyfile = var.enable_atlas_server ? join("\n", [
    "${local.atlas_fqdn} {",
    var.atlas_tls == "internal" ? "\ttls internal" : "",
    "\treverse_proxy 127.0.0.1:8080 {",
    "\t\tflush_interval -1",
    "\t}",
    "}",
    "",
  ]) : ""

  atlas_cloud_init = var.enable_atlas_server ? templatefile("${path.module}/templates/atlas-cloud-init.yaml.tftpl", {
    files = concat(
      [
        { path = "/etc/atlas/atlas.env", mode = "0600", content = local.atlas_env },
        { path = "/etc/atlas/bootstrap.json", mode = "0600", content = local.atlas_bootstrap },
        { path = "/etc/atlas/setup.env", mode = "0600", content = local.atlas_setup_env },
        { path = "/etc/atlas/Caddyfile", mode = "0644", content = local.caddyfile },
        { path = "/etc/systemd/system/atlas.service", mode = "0644", content = file("${path.module}/templates/atlas.service") },
        { path = "/usr/local/sbin/atlas-setup.sh", mode = "0700", content = file("${path.module}/scripts/atlas-setup.sh") },
        { path = "/usr/local/sbin/atlas-bootstrap.py", mode = "0700", content = file("${path.module}/scripts/atlas-bootstrap.py") },
      ],
      var.enable_ad_dc ? [
        { path = "/usr/local/share/ca-certificates/atlas-ad-ldaps.crt", mode = "0644", content = local_file.ad_ldaps_cert[0].content },
      ] : [],
    )
  }) : ""
}

resource "azurerm_linux_virtual_machine" "atlas" {
  count                           = local.atlas_count
  name                            = "${local.env_id}-atlas"
  computer_name                   = "atlas"
  location                        = azurerm_resource_group.env[0].location
  resource_group_name             = azurerm_resource_group.env[0].name
  size                            = var.atlas_vm_size
  admin_username                  = "atlasadmin"
  disable_password_authentication = true
  network_interface_ids           = [azurerm_network_interface.atlas[0].id]
  # cloud-init; gzip keeps the certificates and scripts well inside the 64 KB limit.
  custom_data = base64gzip(local.atlas_cloud_init)
  tags        = local.azure_tags

  admin_ssh_key {
    username   = "atlasadmin"
    public_key = tls_private_key.atlas_ssh[0].public_key_openssh
  }

  os_disk {
    caching              = "ReadWrite"
    storage_account_type = "StandardSSD_LRS"
    disk_size_gb         = 64
  }

  source_image_reference {
    publisher = "Canonical"
    offer     = "ubuntu-24_04-lts"
    sku       = "server"
    version   = "latest"
  }

  depends_on = [terraform_data.atlas_guard]
}

resource "azurerm_dev_test_global_vm_shutdown_schedule" "atlas" {
  count                 = var.enable_atlas_server && var.atlas_auto_shutdown_time != "" ? 1 : 0
  virtual_machine_id    = azurerm_linux_virtual_machine.atlas[0].id
  location              = azurerm_resource_group.env[0].location
  enabled               = true
  daily_recurrence_time = var.atlas_auto_shutdown_time
  timezone              = "UTC"
  tags                  = local.azure_tags

  notification_settings {
    enabled = false
  }
}

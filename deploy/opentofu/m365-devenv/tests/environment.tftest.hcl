# Offline tests: every provider is mocked, so nothing reaches a tenant or Azure.
#   tofu init -backend=false && tofu test
# They check the configuration's own logic - the tenant guards, which apps are built,
# and the worker environment file the Atlas workers read.

mock_provider "azuread" {
  mock_data "azuread_client_config" {
    defaults = {
      tenant_id = "11111111-2222-3333-4444-555555555555"
      object_id = "99999999-0000-0000-0000-000000000001"
    }
  }
  mock_data "azuread_domains" {
    defaults = {
      # OpenTofu requires every attribute of a nested mock object, not only the ones read.
      domains = [{
        domain_name         = "contoso.onmicrosoft.com"
        initial             = true
        admin_managed       = true
        authentication_type = "Managed"
        default             = false
        root                = true
        supported_services  = []
        verified            = true
      }]
    }
  }
  mock_data "azuread_service_principal" {
    defaults = {
      object_id = "99999999-0000-0000-0000-000000000002"
      app_role_ids = {
        "User.ReadWrite.All"         = "741f803b-c850-494e-b5df-cde7c675a1ca"
        "Group.ReadWrite.All"        = "62a82d76-70ea-41e2-9197-370581804d09"
        "TeamMember.ReadWrite.All"   = "0121dc95-1b9f-4aed-8bac-58c5ac466691"
        "Channel.Create"             = "f3a65bd4-b703-46df-8f7e-0174fea562aa"
        "TeamSettings.ReadWrite.All" = "bdd80a03-d9bc-451d-b7c4-ce7c63fe3c8f"
        "Sites.ReadWrite.All"        = "9492366f-7969-46a4-8d15-ed1a20078fff"
        "Sites.Selected"             = "883ea226-0bf2-4a8f-9f9d-92c9162a727d"
        "Exchange.ManageAsApp"       = "dc50a0fb-09a3-484d-be87-e023b12c6440"
        "Mail.Send"                  = "b633e1c5-b582-4048-a93e-9f11b44c7e96"
        "User.Invite.All"            = "09850681-111b-4a89-9bed-3f2cae46d706"
        "Sites.Read.All"             = "332a536c-c7ef-4017-ab91-336970924f0d"
        "LicenseAssignment.Read.All" = "e2f98668-2877-4f38-a2f4-8202e0717aa1"
        "AuditLog.Read.All"          = "b0afded3-3588-46d8-8b3d-9842eff778da"
      }
      oauth2_permission_scope_ids = {
        "openid"  = "37f7f235-527c-4136-accd-4a02d197296e"
        "profile" = "14dad69e-099b-42c9-810b-d002981feec1"
        "email"   = "64a6cdd6-aab1-4aaf-94b8-3cc8405e90d0"
      }
    }
  }
  # Computed values the provider validates when they flow into another resource; the
  # mock's random strings would not pass.
  mock_resource "azuread_application" {
    defaults = {
      id        = "/applications/aaaaaaaa-0000-0000-0000-000000000001"
      object_id = "aaaaaaaa-0000-0000-0000-000000000001"
      client_id = "aaaaaaaa-0000-0000-0000-000000000002"
    }
  }
  mock_resource "azuread_service_principal" {
    defaults = {
      object_id = "aaaaaaaa-0000-0000-0000-000000000003"
    }
  }
  mock_resource "azuread_user" {
    defaults = {
      object_id = "aaaaaaaa-0000-0000-0000-000000000004"
    }
  }
}

mock_provider "azurerm" {
  mock_data "azurerm_client_config" {
    defaults = {
      tenant_id = "11111111-2222-3333-4444-555555555555"
      object_id = "99999999-0000-0000-0000-000000000001"
    }
  }
  mock_resource "azurerm_subnet" {
    defaults = {
      id = "/subscriptions/33333333-0000-0000-0000-000000000000/resourceGroups/atlas-test-ab12-rg/providers/Microsoft.Network/virtualNetworks/atlas-test-ab12-vnet/subnets/dc"
    }
  }
  mock_resource "azurerm_public_ip" {
    defaults = {
      id = "/subscriptions/33333333-0000-0000-0000-000000000000/resourceGroups/atlas-test-ab12-rg/providers/Microsoft.Network/publicIPAddresses/atlas-test-ab12-dc-pip"
    }
  }
  mock_resource "azurerm_network_interface" {
    defaults = {
      id = "/subscriptions/33333333-0000-0000-0000-000000000000/resourceGroups/atlas-test-ab12-rg/providers/Microsoft.Network/networkInterfaces/atlas-test-ab12-dc-nic"
    }
  }
  mock_resource "azurerm_network_security_group" {
    defaults = {
      id = "/subscriptions/33333333-0000-0000-0000-000000000000/resourceGroups/atlas-test-ab12-rg/providers/Microsoft.Network/networkSecurityGroups/atlas-test-ab12-dc-nsg"
    }
  }
  mock_resource "azurerm_key_vault" {
    defaults = {
      id = "/subscriptions/33333333-0000-0000-0000-000000000000/resourceGroups/atlas-test-ab12-rg/providers/Microsoft.KeyVault/vaults/atlaskvab12ab12ab"
    }
  }
  mock_resource "azurerm_key_vault_certificate" {
    defaults = {
      secret_id               = "https://atlaskvab12ab12ab.vault.azure.net/secrets/ldaps-dc/0123456789abcdef0123456789abcdef"
      certificate_data_base64 = "TU9DSy1DRVJUSUZJQ0FURS1EQVRBLVRIQVQtSVMtTE9OR0VSLVRIQU4tU0lYVFktRk9VUi1DSEFSQUNURVJTLUZPUi1DSFVOS0lORw=="
    }
  }
  mock_resource "azurerm_linux_virtual_machine" {
    defaults = {
      id = "/subscriptions/33333333-0000-0000-0000-000000000000/resourceGroups/atlas-test-ab12-rg/providers/Microsoft.Compute/virtualMachines/atlas-test-ab12-atlas"
    }
  }
  mock_resource "azurerm_windows_virtual_machine" {
    defaults = {
      id = "/subscriptions/33333333-0000-0000-0000-000000000000/resourceGroups/atlas-test-ab12-rg/providers/Microsoft.Compute/virtualMachines/atlas-test-ab12-dc"
    }
  }
}

mock_provider "tls" {
  mock_resource "tls_self_signed_cert" {
    defaults = {
      cert_pem          = "-----BEGIN CERTIFICATE-----\nTU9DSw==\n-----END CERTIFICATE-----\n"
      validity_end_time = "2030-01-01T00:00:00Z"
    }
  }
  mock_resource "tls_private_key" {
    defaults = {
      private_key_pem       = "-----BEGIN RSA PRIVATE KEY-----\nTU9DSw==\n-----END RSA PRIVATE KEY-----\n"
      private_key_pem_pkcs8 = "-----BEGIN PRIVATE KEY-----\nTU9DSw==\n-----END PRIVATE KEY-----\n"
      # A syntactically valid, otherwise meaningless key: azurerm checks the format.
      public_key_openssh = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQDNGpEy5RDBHBTDWBQkZmnTRAZeRmPrfG8vCJqbg4PQ9oT2piD86FmSfGM3z7eO0W6kwHAK/LORjEOR72cvbCGBjJcXyATShkpqBNtoN9fcc8pLorqBqIYjGHMy9qk/pOmmEVim5+3V3niASLQuYkuknzRvhcJoHkkM3kUrOYSGSB3uZHtqEGy0YhmgS2wBiIhhgmXlPmkL0C3KqUTTsdWNHbdZ8igN1GGqh1Q1KhvqFWvZhxNog1mnZ22i0Ksvp+ls+M2WjF5MYB7BU1ia52ejSlQXOg/ioRnm3P/eH1QtfN8OesqcOvZQUk+NgPFcNa6B2C6L/vGlPSSSCj5jx+oX mock"
    }
  }
}

mock_provider "random" {
  mock_resource "random_string" {
    defaults = {
      result = "ab12"
    }
  }
  mock_resource "random_password" {
    defaults = {
      result = "Mock-Passw0rd-Mock-Passw0rd"
    }
  }
}

mock_provider "local" {}

mock_provider "time" {
  mock_resource "time_static" {
    defaults = {
      rfc3339 = "2026-11-02T10:00:00Z"
    }
  }
  mock_resource "time_offset" {
    defaults = {
      rfc3339 = "2026-11-16T10:00:00Z"
    }
  }
}

variables {
  tenant_id               = "11111111-2222-3333-4444-555555555555"
  expected_initial_domain = "contoso.onmicrosoft.com"
  azure_subscription_id   = "33333333-0000-0000-0000-000000000000"
  atlas_allowed_cidrs     = ["198.51.100.4/32"]

  # tofu test also reads terraform.tfvars. Pin everything a real configuration sets, so
  # the tests check the defaults and never reach a real tenant: with license_sku_ids set,
  # an apply run would start scripts/assign-licenses.sh against Graph despite the mocks.
  license_sku_ids           = []
  shared_tenant             = false
  credential_validity_days  = 14
  workers                   = {}
  atlas_tls                 = "internal"
  sharepoint_read_all_sites = false
  enable_exchange_onprem    = false
  enable_sharepoint_server  = false
  enable_ad_dc              = false
  ad_allowed_cidrs          = []
  mail_sender               = ""
}

run "defaults_build_every_worker_app_and_an_ad_mock" {
  command = apply

  assert {
    condition     = length(module.entra) == 1 && length(module.exchange) == 1 && length(module.sharepoint) == 1 && length(module.teamsphone) == 1
    error_message = "Every Worker Type should get its own app registration by default."
  }
  assert {
    condition     = length(azurerm_windows_virtual_machine.ad) == 0
    error_message = "No domain controller unless enable_ad_dc = true."
  }
  assert {
    condition     = strcontains(local_sensitive_file.worker_env.content, "ATLAS_AD_MOCK='1'")
    error_message = "Without a domain controller the AD worker should be put into mock mode."
  }
  assert {
    condition     = strcontains(local_sensitive_file.worker_env.content, "ATLAS_ENTRA_CONNECTORS='ucc_entra'")
    error_message = "The Entra worker name should default to ucc_entra."
  }
  assert {
    condition     = strcontains(local_sensitive_file.worker_env.content, "ATLAS_ENTRA_UCC_ENTRA_CLIENT_CERTIFICATE='")
    error_message = "The Entra worker should authenticate with a certificate by default."
  }
  assert {
    condition     = !strcontains(local_sensitive_file.worker_env.content, "CLIENT_SECRET")
    error_message = "No client secret should be written when certificates are used."
  }
  assert {
    condition     = strcontains(local_sensitive_file.worker_env.content, "ATLAS_EXCHANGE_UCC_EXCHANGE_CREDENTIALS='{")
    error_message = "The Exchange worker reads one JSON bundle."
  }
  assert {
    condition     = strcontains(local_sensitive_file.worker_env.content, "\"organization\":\"contoso.onmicrosoft.com\"")
    error_message = "The Exchange bundle must name the tenant's initial domain as organization."
  }
  assert {
    condition     = strcontains(local_sensitive_file.worker_env.content, "\"method\":\"clientCredentials\"")
    error_message = "The SharePoint bundle must use the clientCredentials grant."
  }
  assert {
    condition     = strcontains(local_sensitive_file.worker_env.content, "ATLAS_TEAMSPHONE_UCC_TEAMS_CERTIFICATE='")
    error_message = "The Teams Phone worker name should default to ucc_teams with a certificate."
  }
  assert {
    condition     = alltrue([for l in split("\n", local_sensitive_file.worker_env.content) : !startswith(l, "ATLAS_") || length(regexall("'", l)) == 2])
    error_message = "Every ATLAS_ line must be exactly one single-quoted value on one line."
  }
  assert {
    condition     = local_sensitive_file.worker_env.file_permission == "0600" && local_sensitive_file.worker_env_ps1.file_permission == "0600"
    error_message = "The environment files hold credentials and must be 0600."
  }
  assert {
    condition     = strcontains(local_sensitive_file.worker_env_ps1.content, "$env:ATLAS_ENTRA_CONNECTORS = 'ucc_entra'")
    error_message = "The PowerShell file should set the same variables."
  }
  assert {
    condition     = azuread_group.m365.types == toset(["Unified"]) && azuread_group.m365.mail_enabled
    error_message = "The SharePoint/Teams target must be a Microsoft 365 group."
  }
  assert {
    condition     = length(module.teamsphone[0].service_principal_object_id) > 0 && contains(keys(module.teamsphone[0]), "client_id")
    error_message = "The Teams Phone app should be built."
  }
}

run "wrong_tenant_is_refused" {
  command = plan

  variables {
    tenant_id = "22222222-2222-3333-4444-555555555555"
  }

  expect_failures = [random_string.suffix]
}

run "wrong_initial_domain_is_refused" {
  command = plan

  variables {
    expected_initial_domain = "production.onmicrosoft.com"
  }

  expect_failures = [random_string.suffix]
}

run "secrets_and_custom_worker_names" {
  command = apply

  variables {
    entra_credential      = "secret"
    sharepoint_credential = "secret"
    workers               = { exchange = false, teamsphone = false }
    worker_names          = { entra = "my-entra.1" }
  }

  assert {
    condition     = strcontains(local_sensitive_file.worker_env.content, "ATLAS_ENTRA_CONNECTORS='my-entra.1'") && strcontains(local_sensitive_file.worker_env.content, "ATLAS_ENTRA_MY_ENTRA_1_CLIENT_SECRET='")
    error_message = "A worker name is folded to its environment key as connector/envname.Key does."
  }
  assert {
    condition     = strcontains(local_sensitive_file.worker_env.content, "\"clientSecret\":")
    error_message = "The SharePoint bundle should carry the client secret."
  }
  assert {
    condition     = length(module.exchange) == 0 && length(module.teamsphone) == 0 && !strcontains(local_sensitive_file.worker_env.content, "ATLAS_EXCHANGE_")
    error_message = "A disabled Worker Type gets neither an app nor variables."
  }
}

run "ad_without_server_needs_allowed_cidrs" {
  command = plan

  variables {
    enable_ad_dc        = true
    enable_atlas_server = false
  }

  expect_failures = [terraform_data.ad_guard]
}

run "exchange_needs_the_domain_controller" {
  command = plan

  variables {
    enable_exchange_onprem = true
  }

  expect_failures = [terraform_data.exchange_guard]
}

run "exchange_server_joins_the_test_domain" {
  command = apply

  variables {
    enable_ad_dc           = true
    ad_allowed_cidrs       = ["203.0.113.7/32"]
    enable_exchange_onprem = true
  }

  assert {
    condition     = tolist(azurerm_network_interface.exchange[0].dns_servers) == tolist(["10.42.0.10"])
    error_message = "The Exchange server resolves through the domain controller, or it cannot join the domain."
  }
  assert {
    condition     = azurerm_windows_virtual_machine.exchange[0].size == "Standard_D4s_v5" && azurerm_windows_virtual_machine.exchange[0].os_disk[0].disk_size_gb == 256
    error_message = "Exchange needs 16 GB of memory and room for its image and databases."
  }
  assert {
    condition     = length(azurerm_network_security_group.exchange[0].security_rule) == 0
    error_message = "Without ad_allow_rdp nothing reaches the Exchange server from the Internet."
  }
  assert {
    condition     = output.exchange_onprem.connection_uri == "http://exch01.atlastest.internal/PowerShell/"
    error_message = "The worker reaches Exchange's Remote PowerShell by the server's domain name (Kerberos)."
  }
  assert {
    condition     = !strcontains(azurerm_windows_virtual_machine.exchange[0].custom_data, base64encode(random_password.ad["admin"].result))
    error_message = "The setup script carries no secret; those go in the extension's protected settings."
  }
}

run "sharepoint_needs_the_domain_controller" {
  command = plan

  variables {
    enable_sharepoint_server = true
  }

  expect_failures = [terraform_data.sharepoint_guard]
}

run "sharepoint_needs_a_shutdown_time" {
  command = plan

  variables {
    enable_ad_dc             = true
    ad_allowed_cidrs         = ["203.0.113.7/32"]
    enable_sharepoint_server = true
    ad_auto_shutdown_time    = ""
  }

  expect_failures = [terraform_data.sharepoint_guard]
}

run "sharepoint_is_off_by_default" {
  command = plan

  assert {
    condition     = length(azurerm_windows_virtual_machine.sharepoint) == 0 && !output.sharepoint_server.enabled
    error_message = "No SharePoint server unless enable_sharepoint_server = true."
  }
}

run "sharepoint_server_joins_the_test_domain" {
  command = apply

  variables {
    enable_ad_dc             = true
    ad_allowed_cidrs         = ["203.0.113.7/32"]
    enable_sharepoint_server = true
  }

  assert {
    condition     = azurerm_windows_virtual_machine.sharepoint[0].name == "atlas-test-ab12-sp" && azurerm_windows_virtual_machine.sharepoint[0].computer_name == "sp01"
    error_message = "The VM is <env>-sp and the computer sp01; the worker depends on that name."
  }
  assert {
    condition = (azurerm_windows_virtual_machine.sharepoint[0].source_image_reference[0].publisher == "MicrosoftSharePoint"
      && azurerm_windows_virtual_machine.sharepoint[0].source_image_reference[0].offer == "MicrosoftSharePointServer"
    && azurerm_windows_virtual_machine.sharepoint[0].source_image_reference[0].sku == "spse")
    error_message = "The server is built from the SharePoint Server Subscription Edition image."
  }
  assert {
    condition     = azurerm_windows_virtual_machine.sharepoint[0].size == "Standard_D4s_v5"
    error_message = "A single-server farm beside SQL Server needs 4 cores and 16 GB."
  }
  assert {
    condition     = tolist(azurerm_network_interface.sharepoint[0].dns_servers) == tolist(["10.42.0.10"])
    error_message = "The SharePoint server resolves through the domain controller, or it cannot join the domain."
  }
  assert {
    condition     = length(azurerm_network_security_group.sharepoint[0].security_rule) == 0
    error_message = "Without ad_allow_rdp nothing reaches the SharePoint server from the Internet."
  }
  assert {
    condition     = output.sharepoint_server.enabled && output.sharepoint_server.vm == "atlas-test-ab12-sp" && output.sharepoint_server.web_application_url == "http://sp01.atlastest.internal"
    error_message = "The output names the VM and the web application the worker reaches."
  }
  assert {
    condition     = !strcontains(azurerm_windows_virtual_machine.sharepoint[0].custom_data, base64encode(random_password.ad["admin"].result))
    error_message = "The setup script carries no secret; those go in the extension's protected settings."
  }
  assert {
    condition     = strcontains(base64decode(local.sharepoint_setup_config), "OU=AtlasTest,DC=atlastest,DC=internal")
    error_message = "The farm and service accounts belong in OU=AtlasTest."
  }
}

run "every_vm_shuts_down_at_night" {
  command = apply

  variables {
    enable_ad_dc                = true
    ad_allowed_cidrs            = ["203.0.113.7/32"]
    enable_exchange_onprem      = true
    enable_sharepoint_server    = true
    atlas_auto_shutdown_time    = "2200"
    ad_auto_shutdown_time       = "2200"
    exchange_auto_shutdown_time = "2200"
  }

  # Read from the files, so a VM added later without a schedule fails here as well.
  assert {
    condition = (
      length(flatten([for f in fileset(path.module, "*.tf") : regexall("(?m)^resource \"azurerm_(windows|linux)_virtual_machine\"", file("${path.module}/${f}"))]))
      == length(flatten([for f in fileset(path.module, "*.tf") : regexall("(?m)^resource \"azurerm_dev_test_global_vm_shutdown_schedule\"", file("${path.module}/${f}"))]))
    )
    error_message = "Every VM resource of this environment needs an azurerm_dev_test_global_vm_shutdown_schedule."
  }
  assert {
    condition = (length(azurerm_linux_virtual_machine.atlas) == 1 && length(azurerm_dev_test_global_vm_shutdown_schedule.atlas) == 1
      && length(azurerm_windows_virtual_machine.ad) == 1 && length(azurerm_dev_test_global_vm_shutdown_schedule.ad) == 1
      && length(azurerm_windows_virtual_machine.exchange) == 1 && length(azurerm_dev_test_global_vm_shutdown_schedule.exchange) == 1
    && length(azurerm_windows_virtual_machine.sharepoint) == 1 && length(azurerm_dev_test_global_vm_shutdown_schedule.sharepoint) == 1)
    error_message = "With every server built, each one has its shutdown schedule."
  }
  assert {
    condition     = azurerm_dev_test_global_vm_shutdown_schedule.sharepoint[0].virtual_machine_id == azurerm_windows_virtual_machine.sharepoint[0].id
    error_message = "The schedule must belong to the SharePoint VM."
  }
  assert {
    condition = (azurerm_dev_test_global_vm_shutdown_schedule.sharepoint[0].enabled
      && azurerm_dev_test_global_vm_shutdown_schedule.sharepoint[0].daily_recurrence_time == azurerm_dev_test_global_vm_shutdown_schedule.ad[0].daily_recurrence_time
    && azurerm_dev_test_global_vm_shutdown_schedule.sharepoint[0].timezone == azurerm_dev_test_global_vm_shutdown_schedule.ad[0].timezone)
    error_message = "The SharePoint server stops at the time, and in the time zone, of the others."
  }
}

run "ipv6_and_open_ranges_are_refused" {
  command = plan

  variables {
    atlas_allowed_cidrs = ["2001:db8::/32", "::/0"]
    ad_allowed_cidrs    = ["0.0.0.0/0"]
  }

  expect_failures = [var.atlas_allowed_cidrs, var.ad_allowed_cidrs]
}

run "server_needs_allowed_cidrs" {
  command = plan

  variables {
    atlas_allowed_cidrs = []
  }

  expect_failures = [terraform_data.atlas_guard]
}

run "server_needs_a_subscription" {
  command = plan

  variables {
    azure_subscription_id = ""
  }

  expect_failures = [azurerm_resource_group.env]
}

run "entra_only_needs_no_azure" {
  command = apply

  variables {
    enable_atlas_server   = false
    azure_subscription_id = ""
  }

  assert {
    condition     = length(azurerm_resource_group.env) == 0 && length(azurerm_linux_virtual_machine.atlas) == 0
    error_message = "Without server and domain controller nothing is built in Azure."
  }
  assert {
    condition     = strcontains(local_sensitive_file.worker_env.content, "ATLAS_ENTRA_CONNECTORS='ucc_entra'")
    error_message = "The worker environment file is still written."
  }
}

run "ad_domain_controller" {
  command = apply

  variables {
    enable_ad_dc     = true
    ad_allowed_cidrs = ["203.0.113.7/32"]
  }

  assert {
    condition     = length(regexall("ATLAS_AD_UCC_AD_URL='ldaps://atlas-test-ab12-dc\\.switzerlandnorth\\.cloudapp\\.azure\\.com:636'", local_sensitive_file.worker_env.content)) == 1
    error_message = "The AD worker should be pointed at the domain controller's public name over LDAPS."
  }
  assert {
    condition     = strcontains(local_sensitive_file.worker_env.content, ",OU=AtlasService,DC=atlastest,DC=internal'")
    error_message = "The bind DN should be the service account in OU=AtlasService."
  }
  assert {
    condition     = !strcontains(local_sensitive_file.worker_env.content, "ATLAS_AD_MOCK")
    error_message = "A real domain controller replaces the mock."
  }
  assert {
    condition     = strcontains(local_sensitive_file.worker_env.content, "SSL_CERT_DIR=")
    error_message = "The bash file should make the worker trust the LDAPS certificate."
  }
  assert {
    condition     = one(azurerm_network_security_group.ad[0].security_rule).destination_port_range == "636"
    error_message = "Only LDAPS is opened by default (no RDP)."
  }
  assert {
    condition     = strcontains(local.atlas_bootstrap, "\"endpoint\":\"ldaps://dc01.atlastest.internal:636\"")
    error_message = "The Atlas server's AD worker should reach the domain controller inside the network."
  }
  assert {
    condition     = strcontains(local.atlas_setup_env, "AD_HOSTS_LINE='10.42.0.10 dc01.atlastest.internal'")
    error_message = "The Atlas server should resolve the domain controller's private name."
  }
  assert {
    condition     = alltrue([for l in compact(split("\n", local.atlas_setup_env)) : can(regex("^[A-Z_]+='[^']*'$", l))])
    error_message = "Every line of setup.env must be NAME='value': atlas-setup.sh sources it, and an unquoted space aborts the setup."
  }
  assert {
    condition     = !strcontains(local.atlas_env, "ATLAS_AD_MOCK")
    error_message = "With a domain controller the server's AD worker is not a mock."
  }
  assert {
    condition     = startswith(local_file.ad_ldaps_cert[0].content, "-----BEGIN CERTIFICATE-----\n")
    error_message = "The LDAPS certificate should be written as PEM."
  }
}

run "licences_are_assigned_per_user" {
  # plan only: apply would run scripts/assign-licenses.sh against Graph.
  command = plan

  variables {
    license_sku_ids = ["c42b9cae-ea4f-4ab7-9717-81576235ccac"]
  }

  assert {
    condition     = length(terraform_data.licenses) == length(var.test_users) + 1
    error_message = "Every test user and the manager should get the licences."
  }
}

run "atlas_server_is_configured_through_its_console" {
  command = apply

  assert {
    condition     = length(azurerm_linux_virtual_machine.atlas) == 1
    error_message = "The Atlas server is built by default."
  }
  assert {
    condition = alltrue([for k in ["ucc_entra", "ucc_exchange", "ucc_sharepoint", "ucc_teams", "mail"] :
    contains([for w in jsondecode(local.atlas_bootstrap).workers : w.name], k)])
    error_message = "The bootstrap should create every Microsoft worker in the Console."
  }
  assert {
    condition = alltrue([for w in jsondecode(local.atlas_bootstrap).workers :
    w.secretName == "m365-${w.name}" && can(jsondecode(w.secret))])
    error_message = "Every worker's credential is a JSON bundle stored under m365-<name>."
  }
  assert {
    condition     = one([for w in jsondecode(local.atlas_bootstrap).workers : w if w.kind == "mail"]).provider == "microsoft"
    error_message = "The mail worker sends through Microsoft Graph."
  }
  assert {
    condition     = jsondecode(local.atlas_bootstrap).apiToken.vaultSecret == "atlas"
    error_message = "The processes' API token is sealed as the vault secret 'atlas' (examples/m365-dienste)."
  }
  assert {
    condition     = strcontains(local.atlas_env, "ATLAS_AD_MOCK=1")
    error_message = "Without a domain controller the server's AD worker is a mock."
  }
  assert {
    condition     = strcontains(local.atlas_env, "ATLAS_OIDC_ISSUER=https://login.microsoftonline.com/11111111-2222-3333-4444-555555555555/v2.0")
    error_message = "The server signs people in with the test tenant."
  }
  assert {
    condition     = azuread_application.atlas_sso[0].web[0].redirect_uris == toset(["https://atlas-test-ab12-atlas.switzerlandnorth.cloudapp.azure.com/auth/oidc/callback"])
    error_message = "The sign-in app's redirect URI must be the server's callback."
  }
  assert {
    condition     = strcontains(local.caddyfile, "tls internal")
    error_message = "By default Caddy uses its own CA."
  }
  assert {
    condition     = one([for r in azurerm_network_security_group.atlas[0].security_rule : r if r.destination_port_range == "443"]).source_address_prefixes == toset(["198.51.100.4/32"])
    error_message = "With internal TLS, HTTPS is open only to the allowed addresses."
  }
  assert {
    condition     = length([for r in azurerm_network_security_group.atlas[0].security_rule : r if r.destination_port_range == "80"]) == 0
    error_message = "Port 80 stays closed with internal TLS."
  }
  assert {
    condition     = length(base64gzip(local.atlas_cloud_init)) < 65535
    error_message = "cloud-init must stay inside Azure's custom data limit."
  }
  assert {
    condition     = startswith(local.atlas_cloud_init, "#cloud-config")
    error_message = "The custom data must be a cloud-config document."
  }
}

run "letsencrypt_opens_80_and_443" {
  command = plan

  variables {
    atlas_tls = "letsencrypt"
  }

  assert {
    condition     = !strcontains(local.caddyfile, "tls internal")
    error_message = "Let's Encrypt is Caddy's default; no tls line."
  }
  assert {
    condition     = length([for r in azurerm_network_security_group.atlas[0].security_rule : r if r.destination_port_range == "80" && r.source_address_prefix == "*"]) == 1
    error_message = "HTTP-01 validation needs port 80 from anywhere."
  }
}

run "shared_tenant_scopes_the_mailbox_rights" {
  command = apply

  variables {
    shared_tenant            = true
    workers                  = { teamsphone = false }
    credential_validity_days = 3
  }

  assert {
    condition     = length(local.mail_graph_permissions_effective) == 0
    error_message = "In a shared tenant the mail app holds no Mail.Send in Entra; an Entra grant would stay unscoped."
  }
  assert {
    condition     = length(local.exchange_directory_roles_effective) == 0
    error_message = "In a shared tenant the Exchange app holds no Exchange directory role."
  }
  assert {
    condition     = tolist(local.sharepoint_graph_permissions_effective) == tolist(["Sites.Selected"])
    error_message = "In a shared tenant SharePoint gets Sites.Selected only."
  }
  assert {
    condition     = output.shared_tenant.recipient_filter == "Alias -like 'atlas-test-ab12*'"
    error_message = "The Exchange scope covers exactly this environment's recipients."
  }
  assert {
    condition     = output.shared_tenant.scope_name == "atlas-test-ab12-scope" && output.shared_tenant.exchange_role_group == "atlas-test-ab12-exchange-worker"
    error_message = "Everything created in Exchange carries the prefix, so the cleanup finds it without state."
  }
  assert {
    condition     = startswith(output.shared_tenant.mail_sender, "atlas-test-ab12-")
    error_message = "The mail worker sends from a test mailbox inside the scope."
  }
  assert {
    condition     = length(module.teamsphone) == 0
    error_message = "No Teams Phone app in a shared tenant."
  }
}

run "shared_tenant_reads_all_sites_only_when_asked" {
  command = plan

  variables {
    shared_tenant             = true
    workers                   = { teamsphone = false }
    credential_validity_days  = 3
    sharepoint_read_all_sites = true
  }

  assert {
    condition     = tolist(local.sharepoint_graph_permissions_effective) == tolist(["Sites.Selected", "Sites.Read.All"])
    error_message = "With sharepoint_read_all_sites the SharePoint app reads every site besides its Sites.Selected grant."
  }
  assert {
    condition     = contains(var.entra_graph_permissions, "User.Invite.All")
    error_message = "The Entra app invites the SharePoint team site's guests."
  }
  assert {
    condition     = contains(var.entra_graph_permissions, "LicenseAssignment.Read.All") && contains(var.entra_graph_permissions, "AuditLog.Read.All")
    error_message = "The Entra app reads the licence stock (list-subscribed-skus) and the users' last sign-in (signInActivity)."
  }
}

run "shared_tenant_refuses_teams_phone" {
  command = plan

  variables {
    shared_tenant            = true
    credential_validity_days = 3
  }

  expect_failures = [terraform_data.shared_tenant_guard]
}

run "shared_tenant_refuses_long_lived_credentials" {
  command = plan

  variables {
    shared_tenant = true
    workers       = { teamsphone = false }
  }

  expect_failures = [terraform_data.shared_tenant_guard]
}

run "shared_tenant_refuses_a_sender_outside_the_scope" {
  command = plan

  variables {
    shared_tenant            = true
    workers                  = { teamsphone = false }
    credential_validity_days = 3
    mail_sender              = "patrick@blumer.net"
  }

  expect_failures = [terraform_data.shared_tenant_guard]
}

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
      domains = [{ domain_name = "contoso.onmicrosoft.com", initial = true }]
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
      id = "/subscriptions/33333333-0000-0000-0000-000000000000/resourceGroups/atlas-test-ab12-ad/providers/Microsoft.Network/virtualNetworks/atlas-test-ab12-vnet/subnets/dc"
    }
  }
  mock_resource "azurerm_public_ip" {
    defaults = {
      id = "/subscriptions/33333333-0000-0000-0000-000000000000/resourceGroups/atlas-test-ab12-ad/providers/Microsoft.Network/publicIPAddresses/atlas-test-ab12-dc-pip"
    }
  }
  mock_resource "azurerm_network_interface" {
    defaults = {
      id = "/subscriptions/33333333-0000-0000-0000-000000000000/resourceGroups/atlas-test-ab12-ad/providers/Microsoft.Network/networkInterfaces/atlas-test-ab12-dc-nic"
    }
  }
  mock_resource "azurerm_network_security_group" {
    defaults = {
      id = "/subscriptions/33333333-0000-0000-0000-000000000000/resourceGroups/atlas-test-ab12-ad/providers/Microsoft.Network/networkSecurityGroups/atlas-test-ab12-dc-nsg"
    }
  }
  mock_resource "azurerm_key_vault" {
    defaults = {
      id = "/subscriptions/33333333-0000-0000-0000-000000000000/resourceGroups/atlas-test-ab12-ad/providers/Microsoft.KeyVault/vaults/atlaskvab12ab12ab"
    }
  }
  mock_resource "azurerm_key_vault_certificate" {
    defaults = {
      secret_id               = "https://atlaskvab12ab12ab.vault.azure.net/secrets/ldaps-dc/0123456789abcdef0123456789abcdef"
      certificate_data_base64 = "TU9DSy1DRVJUSUZJQ0FURS1EQVRBLVRIQVQtSVMtTE9OR0VSLVRIQU4tU0lYVFktRk9VUi1DSEFSQUNURVJTLUZPUi1DSFVOS0lORw=="
    }
  }
  mock_resource "azurerm_windows_virtual_machine" {
    defaults = {
      id = "/subscriptions/33333333-0000-0000-0000-000000000000/resourceGroups/atlas-test-ab12-ad/providers/Microsoft.Compute/virtualMachines/atlas-test-ab12-dc"
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

run "ad_needs_allowed_cidrs" {
  command = plan

  variables {
    enable_ad_dc          = true
    azure_subscription_id = "33333333-0000-0000-0000-000000000000"
  }

  expect_failures = [azurerm_resource_group.ad]
}

run "ad_domain_controller" {
  command = apply

  variables {
    enable_ad_dc          = true
    azure_subscription_id = "33333333-0000-0000-0000-000000000000"
    ad_allowed_cidrs      = ["203.0.113.7/32"]
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

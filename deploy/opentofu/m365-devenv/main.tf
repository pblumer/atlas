data "azuread_client_config" "current" {}

data "azuread_domains" "initial" {
  only_initial = true
}

# A short random suffix makes every run's objects distinct. Entra keeps deleted users,
# groups and applications in its recycle bin for 30 days; without the suffix a second
# run within that time could collide with the first run's leftovers.
resource "random_string" "suffix" {
  length  = 4
  lower   = true
  upper   = false
  numeric = true
  special = false

  lifecycle {
    precondition {
      condition     = lower(data.azuread_client_config.current.tenant_id) == lower(var.tenant_id)
      error_message = "The signed-in tenant (${data.azuread_client_config.current.tenant_id}) is not var.tenant_id. Sign in to the test tenant: az login --tenant <id> --allow-no-subscriptions."
    }
    precondition {
      condition     = lower(data.azuread_domains.initial.domains[0].domain_name) == lower(var.expected_initial_domain)
      error_message = "The tenant's initial domain is ${data.azuread_domains.initial.domains[0].domain_name}, not var.expected_initial_domain. Refusing to build a test environment in a tenant that was not named twice."
    }
  }
}

resource "time_static" "created" {}

resource "time_offset" "credential_expiry" {
  base_rfc3339 = time_static.created.rfc3339
  offset_days  = var.credential_validity_days
}

locals {
  env_id         = "${var.name_prefix}-${random_string.suffix.result}"
  initial_domain = lower(data.azuread_domains.initial.domains[0].domain_name)
  # contoso.onmicrosoft.com -> contoso, the host part of contoso.sharepoint.com.
  tenant_name = split(".", local.initial_domain)[0]
  note        = "Atlas M365 development environment ${local.env_id}, created ${time_static.created.rfc3339} by OpenTofu (deploy/opentofu/m365-devenv). Remove with tofu destroy."

  # Well-known first-party application ids.
  msgraph_app_id    = "00000003-0000-0000-c000-000000000000"
  exchange_app_id   = "00000002-0000-0ff1-ce00-000000000000"
  sharepoint_app_id = "00000003-0000-0ff1-ce00-000000000000"

  # Built-in directory role template ids.
  role_teams_administrator = "69091246-20e8-4a56-aa4d-066075b2a7a8"

  output_dir = abspath(startswith(var.output_dir, "/") ? var.output_dir : "${path.module}/${var.output_dir}")
}

# The resource service principals whose app roles are granted. Their app_role_ids map
# turns a permission name (User.ReadWrite.All) into its id, so no GUID of a Graph
# permission is hard-coded here.
data "azuread_service_principal" "msgraph" {
  client_id = local.msgraph_app_id
}

# Office 365 SharePoint Online, only for sharepoint_admin_quota: its application
# permission Sites.FullControl.All is what the SharePoint admin endpoint
# (<tenant>-admin.sharepoint.com, set-site-quota) demands. Looked up by name like Graph's.
data "azuread_service_principal" "sharepoint_online" {
  count     = var.workers.sharepoint && var.sharepoint_admin_quota ? 1 : 0
  client_id = local.sharepoint_app_id
}

# Office 365 Exchange Online. Present in every tenant with an Exchange Online plan; a
# tenant without one has no such service principal, and the lookup fails - which is
# the right answer, since the Exchange worker would have nothing to administer.
data "azuread_service_principal" "exchange" {
  count     = var.workers.exchange ? 1 : 0
  client_id = local.exchange_app_id
}

# ---------------------------------------------------------------------------
# One app registration per Worker Type (docs/entra-permissions.md: "use a
# dedicated app registration for this worker alone").
# ---------------------------------------------------------------------------

locals {
  graph_roles = data.azuread_service_principal.msgraph.app_role_ids

  # In a shared tenant the tenant-wide grants are replaced by scoped ones (shared.tf):
  # no Mail.Send and no Exchange directory role in Entra, Sites.Selected for SharePoint.
  mail_graph_permissions_effective       = var.shared_tenant ? [] : var.mail_graph_permissions
  exchange_directory_roles_effective     = var.shared_tenant ? [] : var.exchange_directory_roles
  sharepoint_graph_permissions_effective = var.shared_tenant ? concat(["Sites.Selected"], var.sharepoint_read_all_sites ? ["Sites.Read.All"] : []) : var.sharepoint_graph_permissions

  # A misspelt permission name fails the plan here, naming the key Graph does not know.
  graph_role = {
    for perm in distinct(concat(var.entra_graph_permissions, local.sharepoint_graph_permissions_effective, var.teamsphone_graph_permissions, local.mail_graph_permissions_effective)) :
    perm => {
      resource_app_id    = local.msgraph_app_id
      resource_object_id = data.azuread_service_principal.msgraph.object_id
      role_id            = local.graph_roles[perm]
    }
  }

  app_owners = [data.azuread_client_config.current.object_id]
}

# --- Entra ID worker (ADR-0172) ---------------------------------------------

module "entra" {
  source = "./modules/worker-app"
  count  = var.workers.entra ? 1 : 0

  display_name                = "${local.env_id}-worker-entra"
  notes                       = local.note
  owners                      = local.app_owners
  app_roles                   = [for p in var.entra_graph_permissions : local.graph_role[p]]
  directory_role_template_ids = var.entra_directory_roles
  create_certificate          = var.entra_credential == "certificate"
  create_secret               = var.entra_credential == "secret"
  validity_days               = var.credential_validity_days
  secret_end_date             = time_offset.credential_expiry.rfc3339
}

# --- Exchange worker (ADR-0462) ---------------------------------------------
# Exchange.ManageAsApp on "Office 365 Exchange Online" plus an Exchange directory role.
# Certificate only: Microsoft documents app-only Exchange administration with a
# certificate, not with a client secret (ADR-0462, open question).

module "exchange" {
  source = "./modules/worker-app"
  count  = var.workers.exchange ? 1 : 0

  display_name = "${local.env_id}-worker-exchange"
  notes        = local.note
  owners       = local.app_owners
  app_roles = [{
    resource_app_id    = local.exchange_app_id
    resource_object_id = data.azuread_service_principal.exchange[0].object_id
    role_id            = data.azuread_service_principal.exchange[0].app_role_ids["Exchange.ManageAsApp"]
  }]
  directory_role_template_ids = local.exchange_directory_roles_effective
  create_certificate          = true
  validity_days               = var.credential_validity_days
}

# --- SharePoint worker (ADR-0141) -------------------------------------------

module "sharepoint" {
  source = "./modules/worker-app"
  count  = var.workers.sharepoint ? 1 : 0

  display_name       = "${local.env_id}-worker-sharepoint"
  notes              = local.note
  owners             = local.app_owners
  app_roles          = [for p in local.sharepoint_graph_permissions_effective : local.graph_role[p]]
  create_certificate = var.sharepoint_credential == "certificate"
  create_secret      = var.sharepoint_credential == "secret"
  validity_days      = var.credential_validity_days
  secret_end_date    = time_offset.credential_expiry.rfc3339
}

# --- Teams Phone worker (ADR-0463) ------------------------------------------
# Teams Administrator role plus User.ReadWrite.All; certificate only, because the
# worker signs in with Connect-MicrosoftTeams -Certificate.

module "teamsphone" {
  source = "./modules/worker-app"
  count  = var.workers.teamsphone ? 1 : 0

  display_name                = "${local.env_id}-worker-teamsphone"
  notes                       = local.note
  owners                      = local.app_owners
  app_roles                   = [for p in var.teamsphone_graph_permissions : local.graph_role[p]]
  directory_role_template_ids = [local.role_teams_administrator]
  create_certificate          = true
  validity_days               = var.credential_validity_days
}

# --- Mail worker over Microsoft Graph (ADR-0079) ----------------------------
# The mail provider "microsoft" authenticates with a client secret only.

module "mail" {
  source = "./modules/worker-app"
  count  = var.workers.mail ? 1 : 0

  display_name       = "${local.env_id}-worker-mail"
  notes              = local.note
  owners             = local.app_owners
  app_roles          = [for p in local.mail_graph_permissions_effective : local.graph_role[p]]
  create_certificate = false
  create_secret      = true
  validity_days      = var.credential_validity_days
  secret_end_date    = time_offset.credential_expiry.rfc3339
}

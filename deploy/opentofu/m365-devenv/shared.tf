# ---------------------------------------------------------------------------
# Shared tenant (var.shared_tenant): the tenant is also used for real work.
#
# Application permissions granted in Entra are tenant-wide and cannot be narrowed.
# So in a shared tenant the two apps whose rights reach every mailbox get none there:
#
#   mail      - no Mail.Send in Entra; instead Exchange RBAC for Applications grants
#               "Application Mail.Send" limited to a management scope. Microsoft documents
#               these Exchange grants as independent of Entra's: an Entra Mail.Send would
#               keep the app unscoped, which is why it is left out entirely.
#   exchange  - no Exchange directory role; instead the service principal joins an
#               Exchange role group whose write scope is the same management scope
#               (app-only Exchange PowerShell in REST mode, which the worker uses).
#
# The scope is every recipient whose alias starts with this environment's prefix
# (atlas-test-<suffix>): the test users, and whatever the workers create under that
# name. SharePoint gets Sites.Selected with a write grant on the test site only.
#
# Those Exchange and SharePoint grants cannot be made with the azuread provider; they
# are made by scripts/shared-tenant-setup.ps1 from `tofu output -json shared_tenant`,
# signed in interactively as an administrator, and removed by
# scripts/shared-tenant-cleanup.ps1, which finds them by prefix and needs no state.
# ---------------------------------------------------------------------------

resource "terraform_data" "shared_tenant_guard" {
  count = var.shared_tenant ? 1 : 0

  lifecycle {
    precondition {
      condition     = !var.workers.teamsphone
      error_message = "shared_tenant = true: the Teams Phone worker needs the Teams Administrator role, which cannot be limited to test objects. Set workers = { teamsphone = false }."
    }
    precondition {
      condition     = var.credential_validity_days <= 7
      error_message = "shared_tenant = true: credential_validity_days must be 7 or less, so a forgotten environment loses its access to the tenant within a week."
    }
    precondition {
      condition     = var.mail_sender == "" || startswith(lower(var.mail_sender), "${local.env_id}-")
      error_message = "shared_tenant = true: mail_sender must be a test mailbox (alias starting with the environment prefix); the mail app may send from no other."
    }
  }
}

output "shared_tenant" {
  description = "Input for scripts/shared-tenant-setup.ps1 (null unless shared_tenant = true)."
  value = var.shared_tenant ? {
    prefix              = local.env_id
    scope_name          = "${local.env_id}-scope"
    recipient_filter    = "Alias -like '${local.env_id}*'"
    exchange_role_group = "${local.env_id}-exchange-worker"
    # Security Group Creation and Membership: without it a distribution list's -Type and
    # BypassSecurityGroupManagerCheck are not granted, so a worker that is not the list's
    # owner cannot remove it or change its members (observed against a real tenant).
    exchange_roles          = ["Mail Recipients", "Mail Recipient Creation", "Distribution Groups", "Security Group Creation and Membership", "Legal Hold", "View-Only Recipients"]
    mail_app_id             = var.workers.mail ? module.mail[0].client_id : null
    mail_sp_object_id       = var.workers.mail ? module.mail[0].service_principal_object_id : null
    mail_display_name       = var.workers.mail ? "${local.env_id}-worker-mail" : null
    exchange_app_id         = var.workers.exchange ? module.exchange[0].client_id : null
    exchange_sp_object_id   = var.workers.exchange ? module.exchange[0].service_principal_object_id : null
    exchange_display_name   = var.workers.exchange ? "${local.env_id}-worker-exchange" : null
    sharepoint_app_id       = var.workers.sharepoint ? module.sharepoint[0].client_id : null
    sharepoint_display_name = var.workers.sharepoint ? "${local.env_id}-worker-sharepoint" : null
    site_group_id           = azuread_group.m365.object_id
    mail_sender             = local.mail_sender
  } : null
}

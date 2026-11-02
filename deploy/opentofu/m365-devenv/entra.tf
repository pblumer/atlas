# ---------------------------------------------------------------------------
# Test identities: users, a manager, groups, and one Microsoft 365 group whose
# SharePoint site and (once the Entra worker runs create-team) team are the
# SharePoint and Teams test targets.
# ---------------------------------------------------------------------------

resource "random_password" "user" {
  for_each = merge(var.test_users, { manager = { given_name = "Max", surname = "Vorgesetzt" } })

  length           = 20
  special          = true
  override_special = "-_@"
  min_upper        = 1
  min_lower        = 1
  min_numeric      = 1
  min_special      = 1
}

resource "azuread_user" "manager" {
  user_principal_name   = "${local.env_id}-manager@${local.initial_domain}"
  display_name          = "${local.env_id} Max Vorgesetzt"
  mail_nickname         = "${local.env_id}-manager"
  given_name            = "Max"
  surname               = "Vorgesetzt"
  department            = "Atlas Test"
  job_title             = "Vorgesetzter"
  usage_location        = var.usage_location
  password              = random_password.user["manager"].result
  force_password_change = false
  account_enabled       = true
}

resource "azuread_user" "test" {
  for_each = var.test_users

  user_principal_name   = "${local.env_id}-${each.key}@${local.initial_domain}"
  display_name          = "${local.env_id} ${each.value.given_name} ${each.value.surname}"
  mail_nickname         = "${local.env_id}-${replace(each.key, ".", "-")}"
  given_name            = each.value.given_name
  surname               = each.value.surname
  department            = each.value.department
  job_title             = each.value.job_title
  usage_location        = var.usage_location
  password              = random_password.user[each.key].result
  force_password_change = false
  account_enabled       = true
  manager_id            = azuread_user.manager.object_id
}

locals {
  all_test_user_ids = concat([azuread_user.manager.object_id], [for u in azuread_user.test : u.object_id])
}

# A security group with every test user as a member: list-groups, get-group,
# add/remove-group-member and the AD-like membership cases.
resource "azuread_group" "security" {
  display_name     = "${local.env_id}-sicherheitsgruppe"
  description      = local.note
  security_enabled = true
  mail_enabled     = false
  members          = local.all_test_user_ids
  owners           = [data.azuread_client_config.current.object_id]

  # Membership is what the workers under test change; OpenTofu must not undo it on the
  # next plan.
  lifecycle {
    ignore_changes = [members]
  }
}

# An empty security group the tests may fill and empty freely.
resource "azuread_group" "scratch" {
  display_name     = "${local.env_id}-spielwiese"
  description      = local.note
  security_enabled = true
  mail_enabled     = false
  owners           = [data.azuread_client_config.current.object_id]

  lifecycle {
    ignore_changes = [members, owners]
  }
}

# A Microsoft 365 (unified) group. Entra provisions a SharePoint team site for it
# within minutes (https://<tenant>.sharepoint.com/sites/<mail_nickname>), and its
# mailbox lives in Exchange Online. Turning it into a team is left to the Entra
# worker's create-team operation - that is a test of the worker, not of this setup.
resource "azuread_group" "m365" {
  display_name     = "${local.env_id}-projektraum"
  description      = local.note
  mail_enabled     = true
  security_enabled = false
  types            = ["Unified"]
  mail_nickname    = "${local.env_id}-projektraum"
  visibility       = "Private"
  behaviors        = ["WelcomeEmailDisabled"]
  owners           = [azuread_user.manager.object_id]
  members          = local.all_test_user_ids

  lifecycle {
    ignore_changes = [members, owners]
  }
}

# Optional licences, so that the test users get an Exchange Online mailbox and Teams.
# The azuread provider has no licence resource; Graph's assignLicense is called through
# the Azure CLI instead. Removing the users on destroy frees the licences again, so
# there is nothing to undo.
resource "terraform_data" "licenses" {
  for_each = length(var.license_sku_ids) == 0 ? {} : merge(
    { manager = azuread_user.manager.object_id },
    { for k, u in azuread_user.test : k => u.object_id },
  )

  triggers_replace = [each.value, join(",", var.license_sku_ids)]

  provisioner "local-exec" {
    command = "bash ${path.module}/scripts/assign-licenses.sh"
    environment = {
      USER_ID = each.value
      SKU_IDS = join(",", var.license_sku_ids)
    }
  }
}

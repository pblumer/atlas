# One Entra app registration for one Atlas Worker Type: the application, its service
# principal, admin consent for its application permissions, optional directory roles,
# and its credential (a self-signed certificate, a client secret, or both).
#
# Admin consent is granted by assigning the app roles to the service principal
# (azuread_app_role_assignment), which is what "Grant admin consent" in the portal
# does; required_resource_access only makes the same permissions visible on the app's
# "API permissions" page.

terraform {
  required_providers {
    azuread = { source = "hashicorp/azuread" }
    tls     = { source = "hashicorp/tls" }
  }
}

locals {
  # resource app id => list of app role ids, for required_resource_access.
  roles_by_resource = {
    for app_id in distinct([for r in var.app_roles : r.resource_app_id]) :
    app_id => [for r in var.app_roles : r.role_id if r.resource_app_id == app_id]
  }
}

resource "azuread_application" "this" {
  display_name     = var.display_name
  sign_in_audience = "AzureADMyOrg"
  notes            = var.notes
  owners           = var.owners

  dynamic "required_resource_access" {
    for_each = local.roles_by_resource
    content {
      resource_app_id = required_resource_access.key

      dynamic "resource_access" {
        for_each = required_resource_access.value
        content {
          id   = resource_access.value
          type = "Role"
        }
      }
    }
  }
}

resource "azuread_service_principal" "this" {
  client_id = azuread_application.this.client_id
  owners    = var.owners
  notes     = var.notes
}

resource "azuread_app_role_assignment" "this" {
  for_each = { for r in var.app_roles : "${r.resource_app_id}/${r.role_id}" => r }

  app_role_id         = each.value.role_id
  principal_object_id = azuread_service_principal.this.object_id
  resource_object_id  = each.value.resource_object_id
}

# Built-in roles are addressed by their template id; no azuread_directory_role
# activation is needed for that.
resource "azuread_directory_role_assignment" "this" {
  for_each = toset(var.directory_role_template_ids)

  role_id             = each.value
  principal_object_id = azuread_service_principal.this.object_id
}

# --- Credential: certificate ------------------------------------------------

resource "tls_private_key" "this" {
  count     = var.create_certificate ? 1 : 0
  algorithm = "RSA"
  rsa_bits  = 2048
}

resource "tls_self_signed_cert" "this" {
  count           = var.create_certificate ? 1 : 0
  private_key_pem = tls_private_key.this[0].private_key_pem

  subject {
    common_name  = var.display_name
    organization = "Atlas test environment"
  }

  validity_period_hours = var.validity_days * 24
  early_renewal_hours   = 0
  allowed_uses          = ["digital_signature", "key_encipherment", "client_auth"]
}

resource "azuread_application_certificate" "this" {
  count          = var.create_certificate ? 1 : 0
  application_id = azuread_application.this.id
  type           = "AsymmetricX509Cert"
  encoding       = "pem"
  value          = tls_self_signed_cert.this[0].cert_pem
  end_date       = tls_self_signed_cert.this[0].validity_end_time

  # The tls provider writes the end in local time (+01:00), Graph returns it in UTC (Z):
  # the same instant, but a different string that would replace the certificate on every
  # apply. A new certificate changes value, which still replaces it.
  lifecycle {
    ignore_changes = [end_date]
  }
}

# --- Credential: client secret ----------------------------------------------

resource "azuread_application_password" "this" {
  count          = var.create_secret ? 1 : 0
  application_id = azuread_application.this.id
  display_name   = "atlas-worker"
  end_date       = var.secret_end_date
}

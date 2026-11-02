output "client_id" {
  description = "Application (client) id."
  value       = azuread_application.this.client_id
}

output "application_object_id" {
  description = "Object id of the application."
  value       = azuread_application.this.object_id
}

output "service_principal_object_id" {
  description = "Object id of the service principal."
  value       = azuread_service_principal.this.object_id
}

output "certificate_pem" {
  description = "The app's certificate alone (public part)."
  value       = var.create_certificate ? tls_self_signed_cert.this[0].cert_pem : null
}

output "certificate_bundle_pem" {
  description = "Certificate followed by its unencrypted PKCS#8 private key - the one PEM text the Atlas workers read."
  value       = var.create_certificate ? "${trimspace(tls_self_signed_cert.this[0].cert_pem)}\n${trimspace(tls_private_key.this[0].private_key_pem_pkcs8)}\n" : null
  sensitive   = true
}

output "client_secret" {
  description = "Client secret, when one was created."
  value       = var.create_secret ? azuread_application_password.this[0].value : null
  sensitive   = true
}

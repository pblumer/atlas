variable "display_name" {
  description = "Display name of the app registration."
  type        = string
}

variable "notes" {
  description = "Free-text note on the app and its service principal."
  type        = string
  default     = null
}

variable "owners" {
  description = "Object ids of the app's owners."
  type        = list(string)
  default     = []
}

variable "app_roles" {
  description = "Application permissions to grant with admin consent: the resource's app id, the resource service principal's object id and the app role id."
  type = list(object({
    resource_app_id    = string
    resource_object_id = string
    role_id            = string
  }))
  default = []
}

variable "directory_role_template_ids" {
  description = "Built-in directory roles (template ids) to assign to the service principal."
  type        = list(string)
  default     = []
}

variable "create_certificate" {
  description = "Generate a self-signed certificate and register its public part with the app."
  type        = bool
  default     = true
}

variable "create_secret" {
  description = "Create a client secret."
  type        = bool
  default     = false
}

variable "validity_days" {
  description = "Lifetime of the certificate in days."
  type        = number
  default     = 14
}

variable "secret_end_date" {
  description = "RFC 3339 expiry of the client secret."
  type        = string
  default     = null
}

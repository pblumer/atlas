# ---------------------------------------------------------------------------
# Tenant and safety
# ---------------------------------------------------------------------------

variable "tenant_id" {
  description = "Directory (tenant) id of the Microsoft 365 TEST tenant. Required, so a plan never lands in whichever tenant the CLI happens to be signed in to."
  type        = string

  validation {
    condition     = can(regex("^[0-9a-fA-F-]{36}$", var.tenant_id))
    error_message = "tenant_id must be a tenant GUID."
  }
}

variable "expected_initial_domain" {
  description = "The tenant's initial domain (contoso.onmicrosoft.com). The plan is refused unless the signed-in tenant has exactly this initial domain: a second, independent guard against a production tenant."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9-]+\\.onmicrosoft\\.com$", lower(var.expected_initial_domain)))
    error_message = "expected_initial_domain must be the *.onmicrosoft.com initial domain."
  }
}

variable "name_prefix" {
  description = "Prefix of every object this environment creates. Together with a random suffix it marks a test object unmistakably (the test-object boundary of examples/account-bestellung) and lets scripts/purge-deleted.sh find leftovers."
  type        = string
  default     = "atlas-test"

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{2,20}$", var.name_prefix))
    error_message = "name_prefix: 3 to 21 characters, lower-case letters, digits and hyphens, starting with a letter."
  }
}

variable "usage_location" {
  description = "Usage location of the test users (ISO 3166 alpha-2). Graph assigns no licence to a user without one."
  type        = string
  default     = "CH"
}

# ---------------------------------------------------------------------------
# What to build
# ---------------------------------------------------------------------------

variable "workers" {
  description = "Which Atlas Worker Types get an app registration. Each gets its own, as docs/entra-permissions.md recommends."
  type = object({
    entra      = optional(bool, true)
    exchange   = optional(bool, true)
    sharepoint = optional(bool, true)
    teamsphone = optional(bool, true)
  })
  default = {}
}

variable "worker_names" {
  description = "Worker (connector) names as the BPMN models address them. The defaults are those of examples/m365-dienste."
  type = object({
    entra      = optional(string, "ucc_entra")
    exchange   = optional(string, "ucc_exchange")
    sharepoint = optional(string, "ucc_sharepoint")
    teamsphone = optional(string, "ucc_teams")
    ad         = optional(string, "ucc_ad")
  })
  default = {}
}

variable "test_users" {
  description = "Test users to create, keyed by a short id. The UPN is <prefix>-<suffix>-<key>@<initial domain>. One extra user, '<prefix>-<suffix>-manager', is created and set as every test user's manager, so get-user-manager has something to read."
  type = map(object({
    given_name = string
    surname    = string
    department = optional(string, "Atlas Test")
    job_title  = optional(string, "Testperson")
  }))
  default = {
    "anna.muster"   = { given_name = "Anna", surname = "Muster" }
    "beat.beispiel" = { given_name = "Beat", surname = "Beispiel" }
    "carla.probe"   = { given_name = "Carla", surname = "Probe" }
  }
}

variable "license_sku_ids" {
  description = "Licence SKU ids (GET /v1.0/subscribedSkus) to assign to every test user and the manager, e.g. an Office 365 E3/E5 SKU so the users get a mailbox and Teams. Empty assigns nothing. Assignment runs scripts/assign-licenses.sh, which needs the Azure CLI signed in to the test tenant."
  type        = list(string)
  default     = []
}

variable "entra_graph_permissions" {
  description = "Microsoft Graph application permissions of the Entra worker app. Default: the user-and-group lifecycle of docs/entra-permissions.md plus the Teams operations (create-team, add-team-member, create-channel, archive-team)."
  type        = list(string)
  default = [
    "User.ReadWrite.All",
    "Group.ReadWrite.All",
    "TeamMember.ReadWrite.All",
    "Channel.Create",
    "TeamSettings.ReadWrite.All",
  ]
}

variable "entra_directory_roles" {
  description = "Directory role template ids for the Entra worker's service principal. Empty by default: reset-password on ordinary accounts needs none. Add User Administrator (fe930be7-5e62-47db-91af-98c3a49a38b1) to test reset-password against accounts that hold a role."
  type        = list(string)
  default     = []
}

variable "entra_credential" {
  description = "How the Entra worker app proves itself: certificate (PEM in the environment) or secret."
  type        = string
  default     = "certificate"

  validation {
    condition     = contains(["certificate", "secret"], var.entra_credential)
    error_message = "entra_credential must be certificate or secret."
  }
}

variable "exchange_directory_roles" {
  description = "Directory role template ids for the Exchange worker's service principal. Default Exchange Administrator, because litigation hold needs more than Exchange Recipient Administrator (31392ffb-586c-42d1-9346-e59415a2cc4e) holds; narrow it when the tests do not touch litigation hold."
  type        = list(string)
  default     = ["29232cdf-9323-42fd-ade2-1d097af3e4de"]
}

variable "sharepoint_graph_permissions" {
  description = "Microsoft Graph application permissions of the SharePoint worker app. Sites.ReadWrite.All is tenant-wide; Sites.Selected additionally needs a per-site grant (README.md, 'Sites.Selected')."
  type        = list(string)
  default     = ["Sites.ReadWrite.All"]
}

variable "sharepoint_credential" {
  description = "How the SharePoint worker app proves itself: certificate or secret."
  type        = string
  default     = "certificate"

  validation {
    condition     = contains(["certificate", "secret"], var.sharepoint_credential)
    error_message = "sharepoint_credential must be certificate or secret."
  }
}

variable "teamsphone_graph_permissions" {
  description = "Microsoft Graph application permissions of the Teams Phone worker app (ADR-0463: User.ReadWrite.All). The Teams Administrator role is assigned in addition."
  type        = list(string)
  default     = ["User.ReadWrite.All"]
}

variable "credential_validity_days" {
  description = "Lifetime of the generated app certificates and secrets. Short on purpose: a forgotten environment stops working on its own."
  type        = number
  default     = 14

  validation {
    condition     = var.credential_validity_days >= 1 && var.credential_validity_days <= 90
    error_message = "credential_validity_days must be between 1 and 90."
  }
}

variable "output_dir" {
  description = "Directory the worker environment files and certificates are written to (mode 0600). Relative paths are relative to this module."
  type        = string
  default     = "out"
}

# ---------------------------------------------------------------------------
# Optional: Active Directory domain controller in Azure
# ---------------------------------------------------------------------------

variable "enable_ad_dc" {
  description = "Build a single Windows Server domain controller in Azure with LDAPS for the AD worker. Costs money while it exists. Without it, use the AD worker's mock mode (ATLAS_AD_MOCK=1)."
  type        = bool
  default     = false
}

variable "azure_subscription_id" {
  description = "Azure subscription for the domain controller. Required when enable_ad_dc = true."
  type        = string
  default     = ""
}

variable "azure_tenant_id" {
  description = "Tenant of that subscription, when it is not the Microsoft 365 test tenant. Empty means var.tenant_id."
  type        = string
  default     = ""
}

variable "azure_location" {
  description = "Azure region of the domain controller, as its short name (switzerlandnorth, westeurope). The public DNS name <label>.<region>.cloudapp.azure.com is derived from it."
  type        = string
  default     = "switzerlandnorth"

  validation {
    condition     = can(regex("^[a-z0-9]+$", var.azure_location))
    error_message = "azure_location must be the region's short name, e.g. switzerlandnorth."
  }
}

variable "ad_vm_size" {
  description = "VM size of the domain controller (Generation 2 capable)."
  type        = string
  default     = "Standard_B2ms"
}

variable "ad_domain_name" {
  description = "DNS name of the test forest."
  type        = string
  default     = "atlastest.internal"
}

variable "ad_netbios_name" {
  description = "NetBIOS name of the test domain (max 15 characters)."
  type        = string
  default     = "ATLASTEST"
}

variable "ad_allowed_cidrs" {
  description = "Source addresses allowed to reach LDAPS (636) and, if enabled, RDP (3389) - the public address of the Atlas worker host and of the operator. Required when enable_ad_dc = true; 0.0.0.0/0 is refused."
  type        = list(string)
  default     = []

  validation {
    condition     = alltrue([for c in var.ad_allowed_cidrs : can(cidrhost(c, 0)) && c != "0.0.0.0/0"])
    error_message = "ad_allowed_cidrs must be CIDR ranges, and 0.0.0.0/0 is not accepted."
  }
}

variable "ad_allow_rdp" {
  description = "Open RDP (3389) from ad_allowed_cidrs, for looking at the domain controller by hand."
  type        = bool
  default     = false
}

variable "ad_auto_shutdown_time" {
  description = "Daily auto-shutdown of the domain controller (HHMM, UTC), so a forgotten environment stops costing compute. Empty disables it."
  type        = string
  default     = "1900"
}

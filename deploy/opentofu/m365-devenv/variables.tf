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

variable "shared_tenant" {
  description = "The tenant is also used for real work (not a dedicated test tenant). Then the mail and Exchange apps get no tenant-wide rights: they act only on recipients whose alias starts with this environment's prefix, through Exchange RBAC for Applications and a scoped Exchange role group (scripts/shared-tenant-setup.ps1); SharePoint gets Sites.Selected with a grant on the test site only; credentials may live at most 7 days, and the Teams Phone worker (Teams Administrator cannot be scoped) must be off. See README.md, 'Geteilter Tenant'."
  type        = bool
  default     = false
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
  description = "Which Atlas Worker Types get an app registration. Each gets its own, as docs/entra-permissions.md recommends. mail sends through Microsoft Graph (Mail.Send) from mail_sender."
  type = object({
    entra      = optional(bool, true)
    exchange   = optional(bool, true)
    sharepoint = optional(bool, true)
    teamsphone = optional(bool, true)
    mail       = optional(bool, true)
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
    mail       = optional(string, "mail")
  })
  default = {}
}

variable "test_users" {
  description = "Test users to create, keyed by a short id. The UPN is <prefix>-<suffix>-<key>@<initial domain>. One extra user, '<prefix>-<suffix>-manager', is created and set as every test user's manager, so get-user-manager has something to read."
  type = map(object({
    given_name = string
    surname    = string
    # Default: the environment's id (<prefix>-<suffix>), so a directory field's fixed
    # filter "department eq <id>" offers this environment's test users and nobody else
    # in a shared tenant (examples/m365-dienste, scripts/install-m365-dienste.py).
    department = optional(string)
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
  description = "Microsoft Graph application permissions of the Entra worker app. Default: the user-and-group lifecycle of docs/entra-permissions.md plus the Teams operations (create-team, add-team-member, create-channel, archive-team) and invite-guest (User.Invite.All, the SharePoint team site's guests)."
  type        = list(string)
  default = [
    "User.ReadWrite.All",
    "Group.ReadWrite.All",
    "TeamMember.ReadWrite.All",
    "Channel.Create",
    "TeamSettings.ReadWrite.All",
    "User.Invite.All",
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

variable "sharepoint_read_all_sites" {
  description = "In a shared tenant, also grant the SharePoint worker Sites.Read.All besides Sites.Selected. The catalogue's SharePoint team site reads each new site (get-site: quota, existence), which Sites.Selected cannot reach until somebody grants that one site. Sites.Read.All lets the test app read every site of the tenant, including real ones: false by default."
  type        = bool
  default     = false
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

variable "mail_graph_permissions" {
  description = "Microsoft Graph application permissions of the mail worker app. Mail.Send lets the app send as ANY mailbox of the tenant; acceptable in a test tenant, scoped in production with an Exchange application access policy or RBAC for Applications."
  type        = list(string)
  default     = ["Mail.Send"]
}

variable "mail_sender" {
  description = "Sender (From) address of the mail worker. Empty uses the manager test user, which then needs a licence with a mailbox (license_sku_ids)."
  type        = string
  default     = ""
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
# Atlas server in Azure
# ---------------------------------------------------------------------------

variable "enable_atlas_server" {
  description = "Build an Atlas server (Ubuntu 24.04 VM) whose supervised workers are already configured in the Console for this tenant. Costs money while it exists."
  type        = bool
  default     = true
}

variable "atlas_install" {
  description = "How the server gets Atlas: release (a published release, checked against its SHA256SUMS) or source (built on the VM from atlas_source_repo at atlas_source_ref)."
  type        = string
  default     = "release"

  validation {
    condition     = contains(["release", "source"], var.atlas_install)
    error_message = "atlas_install must be release or source."
  }
}

variable "atlas_version" {
  description = "Release to install when atlas_install = release (without the leading v)."
  type        = string
  default     = "0.21.0"

  validation {
    condition     = can(regex("^[0-9]+\\.[0-9]+\\.[0-9]+([-.][0-9A-Za-z.]+)?$", var.atlas_version))
    error_message = "atlas_version must look like 0.21.0."
  }
}

variable "atlas_source_repo" {
  description = "Git repository to build from when atlas_install = source. It must be reachable from the VM without credentials."
  type        = string
  default     = "https://github.com/pblumer/atlas.git"
}

variable "atlas_source_ref" {
  description = "Branch or tag to build when atlas_install = source."
  type        = string
  default     = "main"

  validation {
    condition     = can(regex("^[A-Za-z0-9._/-]+$", var.atlas_source_ref))
    error_message = "atlas_source_ref: a branch or tag name."
  }
}

variable "atlas_vm_size" {
  description = "VM size of the Atlas server. Building from source wants 8 GB of memory."
  type        = string
  default     = "Standard_B2ms"
}

variable "atlas_tls" {
  description = "TLS in front of Atlas (Caddy): internal (Caddy's own CA, browser warning, port 443 only for atlas_allowed_cidrs) or letsencrypt (a trusted certificate; ports 80 and 443 are then open to the whole Internet, because Let's Encrypt validates from addresses it does not publish)."
  type        = string
  default     = "internal"

  validation {
    condition     = contains(["internal", "letsencrypt"], var.atlas_tls)
    error_message = "atlas_tls must be internal or letsencrypt."
  }
}

variable "atlas_allowed_cidrs" {
  description = "Source addresses allowed to reach the Atlas server over HTTPS (with atlas_tls = internal) and SSH - your own public IPv4 address (curl -4). Required with enable_atlas_server; IPv6 ranges and 0.0.0.0/0 are refused."
  type        = list(string)
  default     = []

  validation {
    condition     = alltrue([for c in var.atlas_allowed_cidrs : can(cidrhost(c, 0)) && can(regex("^[0-9]{1,3}(\\.[0-9]{1,3}){3}/([1-9]|[12][0-9]|3[0-2])$", c))])
    error_message = "atlas_allowed_cidrs must be IPv4 CIDR ranges (the public IP addresses are IPv4 only; find yours with curl -4 -s https://ifconfig.me), and a /0 range is not accepted."
  }
}

variable "atlas_sso" {
  description = "Register Atlas as an OpenID Connect app in the test tenant, so the test users sign in to Atlas with their Entra accounts (the local admin stays)."
  type        = bool
  default     = true
}

variable "atlas_auto_shutdown_time" {
  description = "Daily auto-shutdown of the Atlas server (HHMM, UTC). Empty disables it."
  type        = string
  default     = "1900"
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
  description = "Azure subscription for the Atlas server and the domain controller. Required when either is enabled."
  type        = string
  default     = ""
}

variable "azure_tenant_id" {
  description = "Tenant of that subscription, when it is not the Microsoft 365 test tenant. Empty means var.tenant_id."
  type        = string
  default     = ""
}

variable "azure_location" {
  description = "Azure region of the Atlas server and the domain controller, as its short name (switzerlandnorth, westeurope). The public DNS name <label>.<region>.cloudapp.azure.com is derived from it."
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
  description = "Source addresses allowed to reach the domain controller from outside: LDAPS (636) and, if enabled, RDP (3389). Not needed for the Atlas server, which reaches it inside the virtual network; required when enable_ad_dc = true without the server. IPv4 only; 0.0.0.0/0 is refused."
  type        = list(string)
  default     = []

  validation {
    condition     = alltrue([for c in var.ad_allowed_cidrs : can(cidrhost(c, 0)) && can(regex("^[0-9]{1,3}(\\.[0-9]{1,3}){3}/([1-9]|[12][0-9]|3[0-2])$", c))])
    error_message = "ad_allowed_cidrs must be IPv4 CIDR ranges (the public IP addresses are IPv4 only; find yours with curl -4 -s https://ifconfig.me), and a /0 range is not accepted."
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

# --- Exchange Server (optional, exchange.tf) ---------------------------------

variable "enable_exchange_onprem" {
  description = "Build an Exchange Server SE (Mailbox role) in the test domain, for the Exchange worker's on-premises backend. Needs enable_ad_dc. Costs a Windows VM of exchange_vm_size while it runs; the unattended installation takes one to two hours after the apply (scripts/wait-exchange.sh)."
  type        = bool
  default     = false
}

variable "exchange_vm_size" {
  description = "VM size of the Exchange server. Exchange wants memory: 16 GB is the least that installs and runs a test organization."
  type        = string
  default     = "Standard_D4s_v5"
}

variable "exchange_auto_shutdown_time" {
  description = "Daily auto-shutdown of the Exchange server (HHMM, UTC). Empty disables it."
  type        = string
  default     = "1900"
}

variable "exchange_iso_url" {
  description = "Installation image of Exchange Server SE, from the Microsoft Download Center (details.aspx?id=108244)."
  type        = string
  default     = "https://download.microsoft.com/download/ad9b1e53-f28e-4a82-987d-6c88803795b2/ExchangeServerSE-x64.iso"
}

variable "exchange_iso_bytes" {
  description = "Expected size of the image in bytes; a download of another size is fetched again rather than mounted."
  type        = number
  default     = 6402453504
}

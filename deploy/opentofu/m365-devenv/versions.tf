terraform {
  # OpenTofu 1.8 is the oldest release this configuration is written against
  # (early variable evaluation is not used, terraform_data and check blocks are).
  required_version = ">= 1.8.0"

  required_providers {
    # Entra ID: test users, groups, app registrations, admin consent, directory roles.
    azuread = {
      source  = "hashicorp/azuread"
      version = "~> 3.0"
    }
    # Only for the optional Active Directory domain controller (var.enable_ad_dc).
    # Pinned to 4.x on purpose: the 5.x line changes defaults this configuration
    # has not been checked against (Key Vault authorisation mode among them).
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 4.40"
    }
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
    local = {
      source  = "hashicorp/local"
      version = "~> 2.5"
    }
    time = {
      source  = "hashicorp/time"
      version = "~> 0.12"
    }
  }
}

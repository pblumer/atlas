# Authentication is left to the providers' standard mechanisms, so no credential is
# ever written into this configuration:
#
#   az login --tenant <test-tenant-id> --allow-no-subscriptions
#
# or the ARM_* variables of a service principal (ARM_TENANT_ID, ARM_CLIENT_ID,
# ARM_CLIENT_SECRET / ARM_CLIENT_CERTIFICATE_PATH). The signed-in principal needs
# Global Administrator in the test tenant (or Privileged Role Administrator plus
# Application Administrator plus User Administrator), see README.md.

provider "azuread" {
  tenant_id = var.tenant_id
}

# azurerm is configured even when no domain controller is wanted, because a provider
# block cannot be conditional. With enable_ad_dc = false no azurerm resource exists,
# the all-zero subscription id is never used for a call, and no resource provider is
# registered.
provider "azurerm" {
  features {
    key_vault {
      # The vault holds only the domain controller's LDAPS certificate; destroying the
      # environment must not leave a soft-deleted vault or certificate behind that
      # blocks the next run's name.
      purge_soft_delete_on_destroy               = true
      purge_soft_deleted_certificates_on_destroy = true
      recover_soft_deleted_key_vaults            = false
      recover_soft_deleted_certificates          = false
    }
    resource_group {
      # Everything the domain controller needs lives in one resource group, so a
      # destroy that meets something added by hand (a diagnostic setting, a disk
      # snapshot) still removes the group.
      prevent_deletion_if_contains_resources = false
    }
    virtual_machine {
      delete_os_disk_on_deletion = true
    }
  }

  subscription_id                 = var.enable_ad_dc ? var.azure_subscription_id : "00000000-0000-0000-0000-000000000000"
  tenant_id                       = coalesce(var.azure_tenant_id, var.tenant_id)
  resource_provider_registrations = "none"
}

#!/usr/bin/env bash
# Finds and removes everything this project creates, by name prefix and WITHOUT the
# OpenTofu state - for when `tofu destroy` cannot run (state lost or broken).
#
#   ./scripts/emergency-cleanup.sh <prefix>          # lists only
#   ./scripts/emergency-cleanup.sh <prefix> --yes    # deletes
#
# <prefix> is var.name_prefix (atlas-test) or one environment's id (atlas-test-ab12).
# It removes, in this order:
#   - Azure resource groups whose name starts with the prefix (Atlas server, domain
#     controller, network, Key Vault - everything Azure lives in one group), and
#     soft-deleted Key Vaults of that name
#   - Entra app registrations (their service principals, credentials, consents and
#     role assignments go with them), users and groups whose display name starts with
#     the prefix
# Then: scripts/purge-deleted.sh for the Entra recycle bin, and in a shared tenant
# scripts/shared-tenant-cleanup.ps1 for Exchange Online.
#
# Needs the Azure CLI signed in to the tenant (and to the subscription for Azure).
set -euo pipefail

prefix="${1:?usage: emergency-cleanup.sh <prefix> [--yes]}"
confirm="${2:-}"
[[ "$prefix" =~ ^[a-z][a-z0-9-]{2,}$ ]] || { echo "emergency-cleanup: refusing prefix '$prefix'" >&2; exit 1; }
yes() { [[ "$confirm" == "--yes" ]]; }
say() { echo "emergency-cleanup: $*"; }

# --- Azure ---------------------------------------------------------------------
if az account show >/dev/null 2>&1 && [[ -n "$(az account show --query id -o tsv 2>/dev/null)" ]]; then
  for rg in $(az group list --query "[?starts_with(name, '$prefix')].name" -o tsv); do
    if yes; then az group delete --name "$rg" --yes; say "deleted resource group $rg"
    else say "would delete resource group $rg"; fi
  done
  for kv in $(az keyvault list-deleted --resource-type vault --query "[?starts_with(name, 'atlaskv')].name" -o tsv 2>/dev/null); do
    # Key Vault names carry the suffix, not the prefix: atlaskv<suffix><random>.
    if yes; then az keyvault purge --name "$kv" && say "purged soft-deleted key vault $kv"
    else say "would purge soft-deleted key vault $kv (check it is this environment's)"; fi
  done
else
  say "no Azure subscription selected; skipping Azure (az account set --subscription <id>)"
fi

# --- Entra ---------------------------------------------------------------------
graph="https://graph.microsoft.com/v1.0"
for kind in applications users groups; do
  rows=$(az rest --method get \
    --url "$graph/$kind?\$filter=startswith(displayName,'$prefix')&\$select=id,displayName&\$top=999" \
    --query "value[].[id,displayName]" -o tsv)
  [[ -z "$rows" ]] && { say "no $kind starting with '$prefix'"; continue; }
  while IFS=$'\t' read -r id name; do
    if yes; then az rest --method delete --url "$graph/$kind/$id" --output none; say "deleted $kind: $name"
    else say "would delete $kind: $name ($id)"; fi
  done <<<"$rows"
done

yes || say "listing only; add --yes to delete"

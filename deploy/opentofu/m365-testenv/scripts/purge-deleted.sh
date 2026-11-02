#!/usr/bin/env bash
# Permanently removes the soft-deleted users, groups and applications a destroyed test
# environment left in the Entra recycle bin (kept there for 30 days otherwise; deleted
# app registrations count against the tenant's quota).
#
#   ./scripts/purge-deleted.sh <name-prefix>          # lists only
#   ./scripts/purge-deleted.sh <name-prefix> --yes    # deletes permanently
#
# <name-prefix> is var.name_prefix (default atlas-test) or one environment's id
# (atlas-test-ab12). Only objects whose display name starts with it are touched.
# Needs the Azure CLI signed in to the test tenant.
set -euo pipefail

prefix="${1:?usage: purge-deleted.sh <name-prefix> [--yes]}"
confirm="${2:-}"
[[ "$prefix" =~ ^[a-z][a-z0-9-]{2,}$ ]] || { echo "purge-deleted: refusing prefix '$prefix'" >&2; exit 1; }

for type in user group application; do
  ids=$(az rest --method get \
    --url "https://graph.microsoft.com/v1.0/directory/deletedItems/microsoft.graph.$type?\$select=id,displayName&\$top=999" \
    --query "value[?starts_with(displayName, '$prefix')].[id,displayName]" --output tsv)
  [[ -z "$ids" ]] && { echo "purge-deleted: no deleted $type objects starting with '$prefix'"; continue; }
  while IFS=$'\t' read -r id name; do
    if [[ "$confirm" == "--yes" ]]; then
      az rest --method delete --url "https://graph.microsoft.com/v1.0/directory/deletedItems/$id" --output none
      echo "purge-deleted: removed $type $name ($id)"
    else
      echo "purge-deleted: would remove $type $name ($id)"
    fi
  done <<<"$ids"
done
[[ "$confirm" == "--yes" ]] || echo "purge-deleted: listing only; add --yes to delete permanently"

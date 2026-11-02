#!/usr/bin/env bash
# Assigns licences to one test user through Microsoft Graph (POST /users/{id}/assignLicense).
# Called by OpenTofu (terraform_data.licenses) with USER_ID and SKU_IDS (comma-separated).
# Needs the Azure CLI signed in to the test tenant as an administrator who may assign
# licences; az rest obtains the Graph token itself.
set -euo pipefail

: "${USER_ID:?USER_ID is not set}"
: "${SKU_IDS:?SKU_IDS is not set}"

command -v az >/dev/null || { echo "assign-licenses: the Azure CLI (az) is required" >&2; exit 1; }

add=""
IFS=',' read -ra skus <<<"$SKU_IDS"
for sku in "${skus[@]}"; do
  sku="${sku//[[:space:]]/}"
  [[ "$sku" =~ ^[0-9a-fA-F-]{36}$ ]] || { echo "assign-licenses: '$sku' is not a SKU id" >&2; exit 1; }
  add+="${add:+,}{\"skuId\":\"$sku\",\"disabledPlans\":[]}"
done
body="{\"addLicenses\":[$add],\"removeLicenses\":[]}"

# A user created seconds ago may not be visible to the licensing service yet.
for attempt in 1 2 3 4 5 6; do
  if az rest --method post \
      --url "https://graph.microsoft.com/v1.0/users/$USER_ID/assignLicense" \
      --headers "Content-Type=application/json" \
      --body "$body" --output none; then
    echo "assign-licenses: $USER_ID licensed"
    exit 0
  fi
  echo "assign-licenses: attempt $attempt failed, retrying in $((attempt * 10))s" >&2
  sleep $((attempt * 10))
done
echo "assign-licenses: giving up on $USER_ID" >&2
exit 1

<#
.SYNOPSIS
  Grants the mail, Exchange and SharePoint worker apps their rights in a shared tenant -
  limited to this environment's test objects (deploy/opentofu/m365-devenv, shared_tenant).

.DESCRIPTION
  Run once after `tofu apply`, signed in interactively as an Exchange and SharePoint
  administrator:

    tofu output -json shared_tenant > out/shared-tenant.json
    pwsh ./scripts/shared-tenant-setup.ps1 -Config out/shared-tenant.json [-Device] [-Part SharePoint]

  Exchange Online:
    - a management scope over every recipient whose alias starts with the prefix;
    - mail app: RBAC for Applications, "Application Mail.Send" in that scope
      (the app holds no Mail.Send in Entra, which would be unscoped);
    - Exchange app: member of a role group whose write scope is that scope
      (the app holds no Exchange directory role).
  SharePoint: the SharePoint app gets "write" on the test site (Sites.Selected).

  Re-running is safe. Exchange caches application permissions for 30 minutes to 2 hours,
  so the mail worker may be refused until then.

  Needs PowerShell 7 with the modules ExchangeOnlineManagement and
  Microsoft.Graph.Authentication. -Device signs in with a device code (no local browser).
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)] [string] $Config,
    [switch] $Device,
    # Repeat one part only, e.g. SharePoint after its sign-in timed out.
    [ValidateSet('All', 'Exchange', 'SharePoint')] [string] $Part = 'All'
)

$ErrorActionPreference = 'Stop'
$c = Get-Content -Raw -Path $Config | ConvertFrom-Json
if (-not $c) { throw 'The shared_tenant output is null: is shared_tenant = true set in terraform.tfvars?' }
foreach ($m in 'ExchangeOnlineManagement', 'Microsoft.Graph.Authentication') {
    if (-not (Get-Module -ListAvailable -Name $m)) { throw "Module $m is missing: Install-Module $m -Scope CurrentUser" }
}

function Step([string] $Text) { Write-Host "== $Text" }
# Some Exchange cmdlets end the script on "not found" whatever -ErrorAction says, so an
# existence check catches instead of relying on SilentlyContinue.
function Find([scriptblock] $Get) { try { & $Get } catch { $null } }

# ---------------------------------------------------------------- Exchange Online
if ($Part -ne 'SharePoint' -and ($c.mail_app_id -or $c.exchange_app_id)) {
    $exo = @{ ShowBanner = $false }
    if ($Device) { $exo.Device = $true }
    Connect-ExchangeOnline @exo
    try {
        # Small tenants start "dehydrated": custom scopes and role groups are refused until
        # the one-time, irreversible Enable-OrganizationCustomization has run.
        if ((Get-OrganizationConfig).IsDehydrated) {
            throw 'Exchange Online is not customised yet (IsDehydrated). Run Enable-OrganizationCustomization once as an Exchange administrator (it cannot be undone, and may take a while to finish), then run this script again. See README.md, "Geteilter Tenant".'
        }
        if (-not (Find { Get-ManagementScope -Identity $c.scope_name -ErrorAction Stop })) {
            Step "management scope $($c.scope_name): $($c.recipient_filter)"
            New-ManagementScope -Name $c.scope_name -RecipientRestrictionFilter $c.recipient_filter | Out-Null
        }

        if ($c.mail_app_id) {
            if (-not (Find { Get-ServicePrincipal -Identity $c.mail_sp_object_id -ErrorAction Stop })) {
                Step "service principal $($c.mail_display_name)"
                New-ServicePrincipal -AppId $c.mail_app_id -ObjectId $c.mail_sp_object_id -DisplayName $c.mail_display_name | Out-Null
            }
            $assignment = "$($c.prefix)-mail-send"
            if (-not (Find { Get-ManagementRoleAssignment -Identity $assignment -ErrorAction Stop })) {
                Step "Application Mail.Send for $($c.mail_display_name), scope $($c.scope_name)"
                New-ManagementRoleAssignment -Name $assignment -App $c.mail_sp_object_id `
                    -Role 'Application Mail.Send' -CustomResourceScope $c.scope_name | Out-Null
            }
            Step "check: may the mail app send as $($c.mail_sender)?"
            Test-ServicePrincipalAuthorization -Identity $c.mail_sp_object_id -Resource $c.mail_sender |
                Format-Table -AutoSize | Out-String | Write-Host
        }

        if ($c.exchange_app_id) {
            if (-not (Find { Get-ServicePrincipal -Identity $c.exchange_sp_object_id -ErrorAction Stop })) {
                Step "service principal $($c.exchange_display_name)"
                New-ServicePrincipal -AppId $c.exchange_app_id -ObjectId $c.exchange_sp_object_id -DisplayName $c.exchange_display_name | Out-Null
            }
            if (-not (Find { Get-RoleGroup -Identity $c.exchange_role_group -ErrorAction Stop })) {
                Step "role group $($c.exchange_role_group): $($c.exchange_roles -join ', '), write scope $($c.scope_name)"
                New-RoleGroup -Name $c.exchange_role_group -Roles $c.exchange_roles `
                    -CustomRecipientWriteScope $c.scope_name `
                    -Description "Atlas test environment $($c.prefix): Exchange worker, test recipients only" | Out-Null
            }
            $members = @(Find { Get-RoleGroupMember -Identity $c.exchange_role_group -ErrorAction Stop })
            if (-not ($members | Where-Object { "$($_.ExternalDirectoryObjectId)$($_.Identity)$($_.Name)" -match [regex]::Escape($c.exchange_sp_object_id) -or $_.Name -eq $c.exchange_display_name })) {
                Step "$($c.exchange_display_name) joins $($c.exchange_role_group)"
                Add-RoleGroupMember -Identity $c.exchange_role_group -Member $c.exchange_sp_object_id
            }
        }
    }
    finally {
        Disconnect-ExchangeOnline -Confirm:$false | Out-Null
    }
}

# ---------------------------------------------------------------- SharePoint
# A delegated grant reaches only as far as the signed-in admin's own access, and a Global
# Administrator has none on a private group's site (GET sites/root answers 403). The admin
# is therefore made an owner of the test group for the grant only, and removed again in
# finally - also when a step fails. Nothing outside the test group changes.
if ($Part -ne 'Exchange' -and $c.sharepoint_app_id) {
    $mg = @{ Scopes = 'Sites.FullControl.All', 'Group.ReadWrite.All', 'User.Read'; NoWelcome = $true }
    # Connect-MgGraph's device code sign-in needs a real terminal: started without one (in
    # the background), it reports "timed out after 120 seconds" although the browser
    # sign-in succeeded. -ClientTimeout does not change that.
    if ($Device) { $mg.UseDeviceCode = $true }
    Connect-MgGraph @mg
    $graph = 'https://graph.microsoft.com/v1.0'
    $ownerAdded = $false
    $me = $null
    try {
        $me = (Invoke-MgGraphRequest -Method GET -Uri "$graph/me?`$select=id,userPrincipalName")
        $owners = (Invoke-MgGraphRequest -Method GET -Uri "$graph/groups/$($c.site_group_id)/owners?`$select=id").value.id
        if ($owners -notcontains $me.id) {
            Step "$($me.userPrincipalName) becomes owner of the test group for the grant"
            $ref = @{ '@odata.id' = "$graph/directoryObjects/$($me.id)" } | ConvertTo-Json
            Invoke-MgGraphRequest -Method POST -Uri "$graph/groups/$($c.site_group_id)/owners/`$ref" -Body $ref -ContentType 'application/json' | Out-Null
            $ownerAdded = $true
        }

        # 404: the site is still being provisioned. 403 right after the owner was added:
        # SharePoint has not seen the new owner yet. Both pass within minutes.
        $site = $null
        $last = $null
        for ($i = 0; $i -lt 30 -and -not $site; $i++) {
            try { $site = Invoke-MgGraphRequest -Method GET -Uri "$graph/groups/$($c.site_group_id)/sites/root" }
            catch {
                $code = $_.Exception.Response.StatusCode.value__
                $last = "HTTP $code $($_.Exception.Message)"
                if ($code -ne 404 -and -not ($code -eq 403 -and $ownerAdded)) { throw "GET sites/root of group $($c.site_group_id): $last" }
                Step "test site not reachable yet ($last); waiting 30 s"; Start-Sleep -Seconds 30
            }
        }
        if (-not $site) { throw "The test site of group $($c.site_group_id) is not reachable ($last); run this part again later (-Part SharePoint)." }

        $uri = "$graph/sites/$($site.id)/permissions"
        $granted = (Invoke-MgGraphRequest -Method GET -Uri $uri).value | Where-Object {
            ($_.grantedToIdentitiesV2.application.id + $_.grantedToIdentities.application.id) -match [regex]::Escape($c.sharepoint_app_id)
        }
        if (-not $granted) {
            Step "SharePoint app may write to $($site.webUrl)"
            $body = @{
                roles               = @('write')
                grantedToIdentities = @(@{ application = @{ id = $c.sharepoint_app_id; displayName = $c.sharepoint_display_name } })
            } | ConvertTo-Json -Depth 5
            Invoke-MgGraphRequest -Method POST -Uri $uri -Body $body -ContentType 'application/json' | Out-Null
        }
        Step "check: site permissions for the SharePoint app"
        (Invoke-MgGraphRequest -Method GET -Uri $uri).value | Where-Object {
            ($_.grantedToIdentitiesV2.application.id + $_.grantedToIdentities.application.id) -match [regex]::Escape($c.sharepoint_app_id)
        } | ForEach-Object { Write-Host "   roles: $($_.roles -join ', ')" }
    }
    finally {
        if ($ownerAdded) {
            Step "$($me.userPrincipalName) is removed as owner of the test group"
            Invoke-MgGraphRequest -Method DELETE -Uri "$graph/groups/$($c.site_group_id)/owners/$($me.id)/`$ref" | Out-Null
        }
        Disconnect-MgGraph -ErrorAction SilentlyContinue | Out-Null
    }
}

Step 'done'

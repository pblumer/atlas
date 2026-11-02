<#
.SYNOPSIS
  Removes what scripts/shared-tenant-setup.ps1 created in Exchange Online, found by
  name prefix - so it works after `tofu destroy` and without any state.

.DESCRIPTION
    pwsh ./scripts/shared-tenant-cleanup.ps1 -Prefix atlas-test          # lists only
    pwsh ./scripts/shared-tenant-cleanup.ps1 -Prefix atlas-test -Yes     # removes

  Order: role assignments, role groups, service principals, management scopes (a scope
  cannot be removed while something still uses it). The SharePoint site grant needs no
  cleanup: it goes with the app registration and the site. Signs in interactively as an
  Exchange administrator; -Device uses a device code.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)] [ValidatePattern('^[a-z][a-z0-9-]{2,}$')] [string] $Prefix,
    [switch] $Yes,
    [switch] $Device
)

$ErrorActionPreference = 'Stop'
$exo = @{ ShowBanner = $false }
if ($Device) { $exo.Device = $true }
Connect-ExchangeOnline @exo

function Sweep([string] $What, [object[]] $Items, [string] $Label, [scriptblock] $Remove) {
    if (-not $Items) { Write-Host "== no $What starting with '$Prefix'"; return }
    foreach ($i in $Items) {
        if ($Yes) { & $Remove $i; Write-Host "== removed $What $($i.$Label)" }
        else { Write-Host "== would remove $What $($i.$Label)" }
    }
}

try {
    $like = "$Prefix*"
    Sweep 'role assignment' @(Get-ManagementRoleAssignment | Where-Object { $_.Name -like $like }) 'Name' {
        param($i) Remove-ManagementRoleAssignment -Identity $i.Identity -Confirm:$false }
    Sweep 'role group' @(Get-RoleGroup | Where-Object { $_.Name -like $like }) 'Name' {
        param($i) Remove-RoleGroup -Identity $i.Identity -Confirm:$false -BypassSecurityGroupManagerCheck }
    Sweep 'service principal' @(Get-ServicePrincipal | Where-Object { $_.DisplayName -like $like }) 'DisplayName' {
        param($i) Remove-ServicePrincipal -Identity $i.Identity -Confirm:$false }
    Sweep 'management scope' @(Get-ManagementScope | Where-Object { $_.Name -like $like }) 'Name' {
        param($i) Remove-ManagementScope -Identity $i.Identity -Confirm:$false }
    if (-not $Yes) { Write-Host '== listing only; add -Yes to remove' }
}
finally {
    Disconnect-ExchangeOnline -Confirm:$false | Out-Null
}

<#
.SYNOPSIS
  Runs an Atlas Exchange worker for the on-premises Exchange server on exch01 itself
  (deploy/opentofu/m365-devenv, exchange.tf), as the worker's service account.

.DESCRIPTION
  Started once through Azure's run command (onprem/install-worker.sh), as SYSTEM:
    - fetches atlas.exe from the Atlas server inside the virtual network,
    - writes the worker's settings to C:\Atlas\worker.json, readable by SYSTEM,
      Administrators and the service account only,
    - registers a start-up task that runs `atlas.exe worker --connector exchange` as the
      service account and starts it now.
  The worker serves the connector ucc_exchange_onprem with "target": "onprem": every job
  opens Remote PowerShell to http://<fqdn>/PowerShell/ with Kerberos as the account the
  worker runs as, a member of Organization Management. No password reaches Atlas.

  Written for Windows PowerShell 5.1.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)] [string] $AtlasUrl,
    [Parameter(Mandatory)] [string] $BinaryUrl,
    [Parameter(Mandatory)] [string] $Token,
    [Parameter(Mandatory)] [string] $ServiceAccount,
    [Parameter(Mandatory)] [string] $ServicePassword,
    [Parameter(Mandatory)] [string] $ExchangeFqdn,
    [string] $Connector = 'ucc_exchange_onprem'
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$dir = 'C:\Atlas'
New-Item -ItemType Directory -Force -Path $dir | Out-Null

& curl.exe -sSfL --retry 5 -o "$dir\atlas.exe" $BinaryUrl
if ($LASTEXITCODE -ne 0) { throw "download of atlas.exe failed (curl exit $LASTEXITCODE)" }

$name = ($Connector.ToUpperInvariant() -replace '[^A-Z0-9]', '_')
$bundle = @{ target = 'onprem'; connectionUri = "http://$ExchangeFqdn/PowerShell/"; authentication = 'Kerberos' } | ConvertTo-Json -Compress
$settings = [ordered]@{
    ATLAS_TOKEN                          = $Token
    ATLAS_EXCHANGE_CONNECTORS            = $Connector
    "ATLAS_EXCHANGE_${name}_CREDENTIALS" = $bundle
    ATLAS_EXCHANGE_POWERSHELL            = 'powershell.exe'
}
$cfg = "$dir\worker.json"
$settings | ConvertTo-Json | Set-Content -Path $cfg -Encoding UTF8
$user = ($ServiceAccount -split '@')[0]
$domain = (Get-WmiObject Win32_ComputerSystem).Domain
$netbios = (Get-WmiObject Win32_NTDomain | Where-Object { $_.DnsForestName -eq $domain } | Select-Object -First 1).DomainName
& icacls.exe $cfg /inheritance:r /grant:r 'SYSTEM:(F)' 'BUILTIN\Administrators:(F)' "${netbios}\${user}:(R)" | Out-Null
if ($LASTEXITCODE -ne 0) { throw "icacls failed on $cfg" }

# The runner reads the settings into its own environment and starts the worker; its log
# goes to C:\Atlas\worker.log.
$runner = @"
`$ErrorActionPreference = 'Stop'
(Get-Content -Raw '$cfg' | ConvertFrom-Json).PSObject.Properties | ForEach-Object { [Environment]::SetEnvironmentVariable(`$_.Name, `$_.Value, 'Process') }
& '$dir\atlas.exe' worker --server '$AtlasUrl' --connector exchange *>> '$dir\worker.log'
"@
Set-Content -Path "$dir\run-worker.ps1" -Value $runner -Encoding UTF8

$action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$dir\run-worker.ps1`""
$trigger = New-ScheduledTaskTrigger -AtStartup
$set = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -StartWhenAvailable
Register-ScheduledTask -TaskName 'AtlasExchangeWorker' -Action $action -Trigger $trigger -Settings $set `
    -User "${netbios}\${user}" -Password $ServicePassword -RunLevel Highest -Force | Out-Null
Stop-ScheduledTask -TaskName 'AtlasExchangeWorker' -ErrorAction SilentlyContinue
Start-ScheduledTask -TaskName 'AtlasExchangeWorker'
Start-Sleep -Seconds 15
Write-Output "task: $((Get-ScheduledTask -TaskName 'AtlasExchangeWorker').State)"
if (Test-Path "$dir\worker.log") { Get-Content -Tail 15 "$dir\worker.log" }

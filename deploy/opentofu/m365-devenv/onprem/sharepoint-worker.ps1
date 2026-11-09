<#
.SYNOPSIS
  Runs an Atlas SharePoint Server worker on the farm server itself
  (deploy/opentofu/m365-devenv, sharepoint-server.tf), as the worker's service account.

.DESCRIPTION
  Started once through Azure's run command (onprem/install-sharepoint-worker.sh), as SYSTEM:
    - fetches atlas.exe from the Atlas server inside the virtual network,
    - writes the worker's settings to C:\Atlas\worker.json, readable by SYSTEM,
      Administrators and the service account only,
    - registers a start-up task that runs `atlas.exe worker --connector sharepointserver`
      as the service account and starts it now.
  The worker serves ucc_sharepoint_onprem against the web application: every job starts
  powershell.exe with SharePoint's commands, as the account the worker runs as, which
  sharepoint-setup.ps1 gave SharePoint_Shell_Access. No password reaches Atlas.

  Written for Windows PowerShell 5.1.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)] [string] $AtlasUrl,
    [Parameter(Mandatory)] [string] $BinaryUrl,
    [Parameter(Mandatory)] [string] $Token,
    [Parameter(Mandatory)] [string] $ServiceAccount,
    [Parameter(Mandatory)] [string] $ServicePassword,
    [Parameter(Mandatory)] [string] $WebApplicationUrl,
    [string] $Connector = 'ucc_sharepoint_onprem'
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$dir = 'C:\Atlas'
New-Item -ItemType Directory -Force -Path $dir | Out-Null

# A worker from an earlier run holds atlas.exe open; Windows refuses to replace it.
Stop-ScheduledTask -TaskName 'AtlasSharePointWorker' -ErrorAction SilentlyContinue
Get-Process -Name atlas -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq "$dir\atlas.exe" } | Stop-Process -Force
Start-Sleep -Seconds 2

& curl.exe -sSfL --retry 5 -o "$dir\atlas.exe" $BinaryUrl
if ($LASTEXITCODE -ne 0) { throw "download of atlas.exe failed (curl exit $LASTEXITCODE)" }

$user = ($ServiceAccount -split '@')[0]
$domain = (Get-WmiObject Win32_ComputerSystem).Domain
$netbios = (Get-WmiObject Win32_NTDomain | Where-Object { $_.DnsForestName -eq $domain } | Select-Object -First 1).DomainName
$account = "${netbios}\${user}"

# The account is no administrator here. It may log on as a batch job (the scheduled
# task) through "Performance Log Users", which holds that right and little else; it may
# read C:\Atlas and write only C:\Atlas\logs. Nobody else may change atlas.exe.
$members = & net.exe localgroup 'Performance Log Users'
if (-not ($members -match [regex]::Escape($account))) {
    & net.exe localgroup 'Performance Log Users' $account /add | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "could not add $account to Performance Log Users" }
}
& icacls.exe $dir /inheritance:r /grant:r 'SYSTEM:(OI)(CI)(F)' 'BUILTIN\Administrators:(OI)(CI)(F)' "${account}:(OI)(CI)(RX)" | Out-Null
if ($LASTEXITCODE -ne 0) { throw "icacls failed on $dir" }
New-Item -ItemType Directory -Force -Path "$dir\logs" | Out-Null
& icacls.exe "$dir\logs" /grant "${account}:(OI)(CI)(M)" | Out-Null
if ($LASTEXITCODE -ne 0) { throw "icacls failed on $dir\logs" }

$name = ($Connector.ToUpperInvariant() -replace '[^A-Z0-9]', '_')
$settings = [ordered]@{
    ATLAS_TOKEN                          = $Token
    ATLAS_SHAREPOINTSERVER_CONNECTORS    = $Connector
    "ATLAS_SHAREPOINTSERVER_${name}_URL" = $WebApplicationUrl
    ATLAS_SHAREPOINTSERVER_POWERSHELL    = 'powershell.exe'
}
$cfg = "$dir\worker.json"
$settings | ConvertTo-Json | Set-Content -Path $cfg -Encoding UTF8
& icacls.exe $cfg /inheritance:r /grant:r 'SYSTEM:(F)' 'BUILTIN\Administrators:(F)' "${account}:(R)" | Out-Null
if ($LASTEXITCODE -ne 0) { throw "icacls failed on $cfg" }

# The runner reads the settings into its own environment and starts the worker; its log
# goes to C:\Atlas\logs\worker.log. cmd.exe does the redirection: Windows PowerShell 5.1
# turns every stderr line of a native program into an error record, and with
# ErrorActionPreference Stop the worker's first log line would end the runner.
$runner = @"
`$ErrorActionPreference = 'Stop'
(Get-Content -Raw '$cfg' | ConvertFrom-Json).PSObject.Properties | ForEach-Object { [Environment]::SetEnvironmentVariable(`$_.Name, `$_.Value, 'Process') }
`$ErrorActionPreference = 'Continue'
& cmd.exe /c "`"$dir\atlas.exe`" worker --server $AtlasUrl --connector sharepointserver >> `"$dir\logs\worker.log`" 2>&1"
exit `$LASTEXITCODE
"@
Set-Content -Path "$dir\run-worker.ps1" -Value $runner -Encoding UTF8

$action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$dir\run-worker.ps1`""
$trigger = New-ScheduledTaskTrigger -AtStartup
$set = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -StartWhenAvailable
Register-ScheduledTask -TaskName 'AtlasSharePointWorker' -Action $action -Trigger $trigger -Settings $set `
    -User $account -Password $ServicePassword -RunLevel Limited -Force | Out-Null
Stop-ScheduledTask -TaskName 'AtlasSharePointWorker' -ErrorAction SilentlyContinue
Start-ScheduledTask -TaskName 'AtlasSharePointWorker'
Start-Sleep -Seconds 20
$state = (Get-ScheduledTask -TaskName 'AtlasSharePointWorker').State
$info = Get-ScheduledTaskInfo -TaskName 'AtlasSharePointWorker'
Write-Output ("task: {0}, last result 0x{1:X}" -f $state, $info.LastTaskResult)
if (Test-Path "$dir\logs\worker.log") { Get-Content -Tail 15 "$dir\logs\worker.log" }
if ($state -ne 'Running') { throw "the worker task is $state, not running" }

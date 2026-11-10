<#
.SYNOPSIS
  Installs the German, French and Italian language packs on the SharePoint Server farm
  (scripts/sharepoint-languages.sh).

.DESCRIPTION
  Called by Azure's managed run command as SYSTEM, it copies itself to C:\AtlasSetup and
  registers a scheduled task that runs it again with -Run as the domain administrator,
  the farm's administrator: the SharePoint cmdlets and PSConfig need that account, and a
  run command could not wait out an installation this long.

  With -Run, for each language not yet installed (registry InstalledLanguages), it
  downloads ServerLanguagePack.iso from Microsoft's Download Center (id 103600), mounts
  it and runs setup.exe /config <ISO>\Files\SetupSilent\config.xml /IAcceptTheLicenseTerms,
  as Microsoft documents for an unattended installation. It then runs PSConfig, without
  which Microsoft says a language pack is not installed correctly, and writes
  C:\AtlasSetup\sharepoint-languages.done with the installed LCIDs, or
  sharepoint-languages-failed.txt. It restarts the server at the end, as setup asks.

  Log: C:\AtlasSetup\sharepoint-languages.log. Re-running is safe: an installed language
  is skipped. Written for Windows PowerShell 5.1.
#>
[CmdletBinding()]
param(
    [string] $DomainUser,
    [string] $DomainPassword,
    [switch] $Run
)
$ErrorActionPreference = 'Stop'
$root = 'C:\AtlasSetup'
$self = Join-Path $root 'sharepoint-languages.ps1'
$doneFile = Join-Path $root 'sharepoint-languages.done'
$failedFile = Join-Path $root 'sharepoint-languages-failed.txt'
$task = 'AtlasSharePointLanguages'

# The Download Center's file per language (details.aspx?id=103600, read 2026-11-10).
$packs = @(
    @{ Code = 'de-de'; Lcid = '1031'; Url = 'https://download.microsoft.com/download/f/9/c/f9cf4dd5-c7df-43b2-910a-ff1afa77d309/ServerLanguagePack.iso' },
    @{ Code = 'fr-fr'; Lcid = '1036'; Url = 'https://download.microsoft.com/download/f/0/0/f0050ddc-6ece-495d-a8ee-90c2b44cc3ff/ServerLanguagePack.iso' },
    @{ Code = 'it-it'; Lcid = '1040'; Url = 'https://download.microsoft.com/download/4/2/8/428b78cf-3cfd-4e4b-9105-dd9a96dca7c1/ServerLanguagePack.iso' }
)

if (-not $Run) {
    if (-not $DomainUser -or -not $DomainPassword) { throw 'DomainUser and DomainPassword are required' }
    New-Item -ItemType Directory -Force -Path $root | Out-Null
    Copy-Item -Path $PSCommandPath -Destination $self -Force
    Set-ItemProperty -Path $self -Name IsReadOnly -Value $false
    Remove-Item $doneFile, $failedFile -ErrorAction SilentlyContinue
    $action = New-ScheduledTaskAction -Execute 'powershell.exe' `
        -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$self`" -Run"
    $settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit (New-TimeSpan -Hours 3)
    Register-ScheduledTask -TaskName $task -Action $action -Settings $settings -User $DomainUser `
        -Password $DomainPassword -RunLevel Highest -Force | Out-Null
    Start-ScheduledTask -TaskName $task
    Write-Output "language packs: task $task started as $DomainUser"
    return
}

function Get-File([string] $Url, [string] $Path) {
    for ($attempt = 1; $attempt -le 10; $attempt++) {
        & curl.exe -fsSL -o $Path $Url
        if ($LASTEXITCODE -eq 0) { return }
        Write-Output "download of $Url failed (curl exit $LASTEXITCODE), attempt $attempt"
        Start-Sleep -Seconds 15
    }
    throw "download of $Url failed (curl exit $LASTEXITCODE)"
}

# Bounded, and reading the exit code the way Windows PowerShell 5.1 needs (see
# sharepoint-setup.ps1, Invoke-Checked).
function Invoke-Checked([string] $File, [string[]] $Arguments, [int[]] $Ok = @(0), [int] $TimeoutMinutes = 60) {
    Write-Output "> $File $($Arguments -join ' ')"
    $p = Start-Process -FilePath $File -ArgumentList $Arguments -PassThru -NoNewWindow
    $null = $p.Handle
    if (-not $p.WaitForExit($TimeoutMinutes * 60 * 1000)) {
        & taskkill.exe /PID $p.Id /T /F | Out-Null
        throw "$File did not finish within $TimeoutMinutes minutes; see its log"
    }
    if ($Ok -notcontains $p.ExitCode) { throw "$File exited with $($p.ExitCode)" }
}

function Get-InstalledLcids {
    $key = 'HKLM:\SOFTWARE\Microsoft\Shared Tools\Web Server Extensions\16.0\InstalledLanguages'
    (Get-ItemProperty $key).PSObject.Properties | Where-Object { $_.Name -match '^\d+$' } | ForEach-Object Name
}

Start-Transcript -Path (Join-Path $root 'sharepoint-languages.log') -Append | Out-Null
try {
    $installed = @(Get-InstalledLcids)
    Write-Output "installed before: $($installed -join ', ')"
    foreach ($pack in $packs) {
        if ($installed -contains $pack.Lcid) { Write-Output "$($pack.Code) ($($pack.Lcid)) installed already"; continue }
        $dir = Join-Path $root "lp\$($pack.Code)"
        New-Item -ItemType Directory -Force -Path $dir | Out-Null
        $iso = Join-Path $dir 'ServerLanguagePack.iso'
        if (-not (Test-Path $iso)) { Get-File $pack.Url $iso }
        $img = Get-DiskImage -ImagePath $iso
        if (-not $img.Attached) { $img = Mount-DiskImage -ImagePath $iso -PassThru }
        $drive = ($img | Get-Volume).DriveLetter + ':'
        try {
            $config = "$drive\Files\SetupSilent\config.xml"
            if (-not (Test-Path $config)) { throw "$config is not on the language pack image" }
            Invoke-Checked "$drive\setup.exe" @('/config', "`"$config`"", '/IAcceptTheLicenseTerms') @(0, 3010) -TimeoutMinutes 45
        }
        finally {
            Dismount-DiskImage -ImagePath $iso -ErrorAction SilentlyContinue | Out-Null
        }
        Write-Output "$($pack.Code) ($($pack.Lcid)) installed"
    }
    # Microsoft: without SharePoint Products Configuration a language pack is not installed
    # correctly. This is its command-line form with the steps the wizard takes by default.
    $psconfig = Join-Path $env:CommonProgramFiles 'microsoft shared\Web Server Extensions\16\BIN\psconfig.exe'
    Invoke-Checked $psconfig @('-cmd', 'upgrade', '-inplace', 'b2b', '-wait', '-force',
        '-cmd', 'applicationcontent', '-install', '-cmd', 'installfeatures') @(0) -TimeoutMinutes 60
    $now = @(Get-InstalledLcids)
    $missing = $packs | Where-Object { $now -notcontains $_.Lcid } | ForEach-Object Lcid
    if ($missing) { throw "not installed after setup: $($missing -join ', ')" }
    Set-Content -Path $doneFile -Value ($now -join ',')
    Write-Output "language packs ready: $($now -join ', ')"
}
catch {
    Write-Output "FAILED: $($_.Exception.Message)"
    Set-Content -Path $failedFile -Value $_.Exception.Message
}
finally {
    Stop-Transcript | Out-Null
}
if (Test-Path $doneFile) { Restart-Computer -Force }

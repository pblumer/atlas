<#
.SYNOPSIS
  Installs Exchange Server SE (Mailbox role) into the Atlas test domain, unattended
  (deploy/opentofu/m365-devenv, exchange.tf).

.DESCRIPTION
  Each phase registers the next as a start-up task and reboots:

  Phase 1 (Custom Script Extension, SYSTEM): keeps the settings in a file only SYSTEM
    and Administrators can read, joins the domain.
  Phase 2 (SYSTEM): Windows features, Visual C++ 2012 and 2013, IIS URL Rewrite, the
    installation image (downloaded with resume and checked by size).
  Phase 3 (the domain administrator, who holds Schema and Enterprise Admins):
    UCMA 4.0 from the image, Setup /PrepareSchema, /PrepareAD, /Mode:Install
    /Roles:Mailbox.
  Phase 4 (the domain administrator): the worker's service account in OU=AtlasService,
    member of "Organization Management"; a mailbox for every user in OU=AtlasTest;
    removes the settings file and the tasks.

  Log: C:\AtlasSetup\exchange-phase<N>.log and C:\ExchangeSetupLogs. Progress:
  C:\AtlasSetup\exchange-phase<N>.done; C:\AtlasSetup\exchange.done when the server is
  ready. A failed phase writes C:\AtlasSetup\exchange-failed.txt and stops there.

  Written for Windows PowerShell 5.1.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)] [ValidateSet('1', '2', '3', '4')] [string] $Phase,
    [string] $ConfigB64
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$root = 'C:\AtlasSetup'
$configFile = Join-Path $root 'exchange.json'
$iso = Join-Path $root 'ExchangeServerSE-x64.iso'
New-Item -ItemType Directory -Force -Path $root | Out-Null
Start-Transcript -Path (Join-Path $root "exchange-phase$Phase.log") -Append | Out-Null

function Protect-File([string] $Path) {
    & icacls.exe $Path /inheritance:r /grant:r 'SYSTEM:(F)' 'BUILTIN\Administrators:(F)' | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "icacls failed on $Path" }
}

function Read-Config { Get-Content -Raw -Path $configFile | ConvertFrom-Json }

function Get-Domain-Credential($cfg) {
    $user = "$($cfg.netbiosName)\$($cfg.domainAdmin)"
    New-Object System.Management.Automation.PSCredential($user, (ConvertTo-SecureString $cfg.domainAdminPass -AsPlainText -Force))
}

# The next phase runs at start-up, as SYSTEM or as the domain administrator. Two hours
# are enough for the slowest phase (Setup /Mode:Install).
function Register-Next([string] $Next, $cfg, [switch] $AsDomainAdmin) {
    $action = New-ScheduledTaskAction -Execute 'powershell.exe' `
        -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$root\exchange-setup.ps1`" -Phase $Next"
    $trigger = New-ScheduledTaskTrigger -AtStartup
    $settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit (New-TimeSpan -Hours 3) -StartWhenAvailable
    $name = "AtlasExchangePhase$Next"
    if ($AsDomainAdmin) {
        Register-ScheduledTask -TaskName $name -Action $action -Trigger $trigger -Settings $settings `
            -User "$($cfg.netbiosName)\$($cfg.domainAdmin)" -Password $cfg.domainAdminPass -RunLevel Highest -Force | Out-Null
    }
    else {
        $principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
        Register-ScheduledTask -TaskName $name -Action $action -Trigger $trigger -Principal $principal `
            -Settings $settings -Force | Out-Null
    }
}

function Remove-Task([string] $N) {
    Unregister-ScheduledTask -TaskName "AtlasExchangePhase$N" -Confirm:$false -ErrorAction SilentlyContinue
}

function Get-File([string] $Url, [string] $Path) {
    # curl.exe ships with Windows Server 2022; it resumes and retries a large download.
    & curl.exe -sSL --retry 10 --retry-all-errors --retry-delay 15 -C - -o $Path $Url
    if ($LASTEXITCODE -ne 0) { throw "download of $Url failed (curl exit $LASTEXITCODE)" }
}

function Invoke-Checked([string] $File, [string[]] $Arguments, [int[]] $Ok = @(0)) {
    Write-Output "> $File $($Arguments -join ' ')"
    $p = Start-Process -FilePath $File -ArgumentList $Arguments -Wait -PassThru -NoNewWindow
    if ($Ok -notcontains $p.ExitCode) { throw "$File exited with $($p.ExitCode)" }
}

function Mount-Image {
    $img = Get-DiskImage -ImagePath $iso
    if (-not $img.Attached) { $img = Mount-DiskImage -ImagePath $iso -PassThru }
    ($img | Get-Volume).DriveLetter + ':'
}

try {
    switch ($Phase) {
        '1' {
            # Run again (the extension re-applied): the server joined and moved on already.
            if (Test-Path (Join-Path $root 'exchange-phase1.done')) { Write-Output 'phase 1 done before; nothing to do'; return }
            if (-not $ConfigB64) { throw 'Phase 1 needs -ConfigB64.' }
            $json = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($ConfigB64))
            Set-Content -Path $configFile -Value $json -Encoding UTF8
            Protect-File $configFile
            $cfg = Read-Config

            # Exchange requires the Remote Registry service set to automatic.
            Set-Service -Name RemoteRegistry -StartupType Automatic

            # The domain controller may still be finishing its own setup: try for 30 min.
            $joined = $false
            for ($i = 0; $i -lt 30 -and -not $joined; $i++) {
                try {
                    Add-Computer -DomainName $cfg.domainName -Credential (Get-Domain-Credential $cfg) -Force
                    $joined = $true
                }
                catch { Write-Output "domain join not possible yet: $($_.Exception.Message); waiting 60 s"; Start-Sleep -Seconds 60 }
            }
            if (-not $joined) { throw "could not join $($cfg.domainName)" }
            Register-Next '2' $cfg
            New-Item -ItemType File -Force -Path (Join-Path $root 'exchange-phase1.done') | Out-Null
            Stop-Transcript | Out-Null
            # Restart after a delay, so the extension reports success before the reboot.
            & shutdown.exe /r /t 30 /c 'Atlas Exchange setup, phase 2'
            exit 0
            return
        }
        '2' {
            $cfg = Read-Config
            Install-WindowsFeature Server-Media-Foundation, NET-Framework-45-Core, NET-Framework-45-ASPNET, NET-WCF-HTTP-Activation45, NET-WCF-Pipe-Activation45, NET-WCF-TCP-Activation45, NET-WCF-TCP-PortSharing45, RPC-over-HTTP-proxy, RSAT-Clustering, RSAT-Clustering-CmdInterface, RSAT-Clustering-Mgmt, RSAT-Clustering-PowerShell, WAS-Process-Model, Web-Asp-Net45, Web-Basic-Auth, Web-Client-Auth, Web-Digest-Auth, Web-Dir-Browsing, Web-Dyn-Compression, Web-Http-Errors, Web-Http-Logging, Web-Http-Redirect, Web-Http-Tracing, Web-ISAPI-Ext, Web-ISAPI-Filter, Web-Metabase, Web-Mgmt-Console, Web-Mgmt-Service, Web-Net-Ext45, Web-Request-Monitor, Web-Server, Web-Stat-Compression, Web-Static-Content, Web-Windows-Auth, Web-WMI, Windows-Identity-Foundation, RSAT-ADDS | Out-Null

            $vc12 = Join-Path $root 'vcredist2012_x64.exe'
            $vc13 = Join-Path $root 'vcredist2013_x64.exe'
            $rw = Join-Path $root 'rewrite_amd64_en-US.msi'
            Get-File 'https://download.microsoft.com/download/1/6/B/16B06F60-3B20-4FF2-B699-5E9B7962F9AE/VSU_4/vcredist_x64.exe' $vc12
            Get-File 'https://aka.ms/highdpimfc2013x64enu' $vc13
            Get-File 'https://download.microsoft.com/download/1/2/8/128E2E22-C1B9-44A4-BE2A-5859ED1D4592/rewrite_amd64_en-US.msi' $rw
            # 3010: installed, restart required - the reboot below follows anyway.
            Invoke-Checked $vc12 @('/install', '/quiet', '/norestart') @(0, 1638, 3010)
            Invoke-Checked $vc13 @('/install', '/quiet', '/norestart') @(0, 1638, 3010)
            Invoke-Checked 'msiexec.exe' @('/i', "`"$rw`"", '/qn', '/norestart') @(0, 1638, 3010)

            for ($i = 0; $i -lt 5; $i++) {
                Get-File $cfg.isoUrl $iso
                if ((Get-Item $iso).Length -eq [int64]$cfg.isoBytes) { break }
                Write-Output "image has $((Get-Item $iso).Length) bytes, want $($cfg.isoBytes); fetching again"
                Remove-Item -Force $iso
            }
            if (-not (Test-Path $iso) -or (Get-Item $iso).Length -ne [int64]$cfg.isoBytes) { throw 'the installation image did not arrive whole' }
            Write-Output "image SHA256: $((Get-FileHash -Algorithm SHA256 $iso).Hash)"

            # Unregistered only now: a task removed while it runs may be stopped with it.
            Remove-Task '2'
            Register-Next '3' $cfg -AsDomainAdmin
            New-Item -ItemType File -Force -Path (Join-Path $root 'exchange-phase2.done') | Out-Null
            Stop-Transcript | Out-Null
            Restart-Computer -Force
            return
        }
        '3' {
            $cfg = Read-Config
            $d = Mount-Image
            Invoke-Checked "$d\UCMARedist\Setup.exe" @('/passive', '/norestart') @(0, 1638, 3010)
            $setup = "$d\Setup.exe"
            $terms = '/IAcceptExchangeServerLicenseTerms_DiagnosticDataOFF'
            Invoke-Checked $setup @($terms, '/PrepareSchema')
            Invoke-Checked $setup @($terms, '/PrepareAD', "/OrganizationName:`"$($cfg.organization)`"")
            Invoke-Checked $setup @($terms, '/Mode:Install', '/Roles:Mailbox', '/InstallWindowsComponents')
            # Unregistered only now: a task removed while it runs may be stopped with it.
            Remove-Task '3'
            Register-Next '4' $cfg -AsDomainAdmin
            New-Item -ItemType File -Force -Path (Join-Path $root 'exchange-phase3.done') | Out-Null
            Stop-Transcript | Out-Null
            Restart-Computer -Force
            return
        }
        '4' {
            $cfg = Read-Config
            # Exchange's services start some minutes after boot.
            Start-Sleep -Seconds 300
            Add-PSSnapin Microsoft.Exchange.Management.PowerShell.SnapIn
            Import-Module ActiveDirectory

            $sam = $cfg.serviceAccount
            if (-not (Get-ADUser -Filter "SamAccountName -eq '$sam'")) {
                New-ADUser -Name $sam -SamAccountName $sam -UserPrincipalName "$sam@$($cfg.domainName)" `
                    -Path $cfg.serviceOu -AccountPassword (ConvertTo-SecureString $cfg.servicePassword -AsPlainText -Force) `
                    -Enabled $true -PasswordNeverExpires $true -Description 'Atlas Exchange worker (on-premises)'
            }
            # Organization Management: every cmdlet of the worker's table, within this
            # test organization only.
            Add-RoleGroupMember -Identity 'Organization Management' -Member $sam -ErrorAction SilentlyContinue
            # Remote PowerShell for the account (on by default; stated so it is checked).
            Set-User -Identity $sam -RemotePowerShellEnabled $true

            Get-ADUser -SearchBase $cfg.testOu -Filter * | ForEach-Object {
                if (-not (Get-Recipient -Identity $_.DistinguishedName -ErrorAction SilentlyContinue)) {
                    Enable-Mailbox -Identity $_.DistinguishedName | Out-Null
                    Write-Output "mailbox for $($_.SamAccountName)"
                }
            }

            Remove-Item -Force $configFile
            Remove-Task '4'
            Dismount-DiskImage -ImagePath $iso -ErrorAction SilentlyContinue | Out-Null
            New-Item -ItemType File -Force -Path (Join-Path $root 'exchange-phase4.done') | Out-Null
            Set-Content -Path (Join-Path $root 'exchange.done') -Value (Get-Date -Format o)
            Write-Output 'Exchange Server is ready.'
        }
    }
}
catch {
    Write-Output "FAILED in phase ${Phase}: $($_.Exception.Message)"
    Set-Content -Path (Join-Path $root 'exchange-failed.txt') -Value "phase ${Phase}: $($_.Exception.Message)"
    throw
}
finally {
    # A phase that reboots has ended its transcript already; Stop-Transcript then throws
    # whatever -ErrorAction says, and the Custom Script Extension would report phase 1
    # as failed although it did its work.
    try { Stop-Transcript | Out-Null } catch { }
}

<#
.SYNOPSIS
  Builds a single-server SharePoint Server Subscription Edition farm in the Atlas test
  domain, unattended (deploy/opentofu/m365-devenv, sharepoint-server.tf).

.DESCRIPTION
  Each phase registers the next as a start-up task and reboots:

  Phase 1 (Custom Script Extension, SYSTEM): keeps the settings in a file only SYSTEM
    and Administrators can read, joins the domain.
  Phase 2 (SYSTEM): checks the image carries SharePoint, Active Directory PowerShell.
  Phase 3 (the domain administrator): the farm, application pool and runbook accounts in
    OU=AtlasTest; SQL Server 2025 Standard Developer from Microsoft's download.
  Phase 4 (the domain administrator): the farm, Central Administration, the web
    application with a root site collection, the runbook account's rights; removes the
    settings file and the tasks.

  A phase stopped half way - the nightly shutdown does that - runs again at the next
  start, so every step first looks whether it is done. The domain controller shuts down
  with this server; a task that finds it still off retries.

  Log: C:\AtlasSetup\sharepoint-phase<N>.log. Progress:
  C:\AtlasSetup\sharepoint-phase<N>.done; C:\AtlasSetup\sharepoint.done when the server
  is ready. A failed phase writes C:\AtlasSetup\sharepoint-failed.txt and is tried again
  at the next start.

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
$configFile = Join-Path $root 'sharepoint.json'
$logFile = Join-Path $root "sharepoint-phase$Phase.log"
$failedFile = Join-Path $root 'sharepoint-failed.txt'
$sqlMedia = Join-Path $root 'sql-media'
New-Item -ItemType Directory -Force -Path $root | Out-Null
# A transcript's header repeats the process's command line, and phase 1's holds the
# settings with every password. That phase writes its log itself.
if ($Phase -ne '1') { Start-Transcript -Path $logFile -Append | Out-Null }
# An earlier attempt failed; this one reports for itself.
Remove-Item -Force -ErrorAction SilentlyContinue $failedFile

function Write-Step([string] $Message) {
    Write-Output $Message
    if ($Phase -eq '1') { Add-Content -Path $logFile -Value "$(Get-Date -Format o) $Message" }
}

function Protect-File([string] $Path) {
    & icacls.exe $Path /inheritance:r /grant:r 'SYSTEM:(F)' 'BUILTIN\Administrators:(F)' | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "icacls failed on $Path" }
}

function Read-Config { Get-Content -Raw -Path $configFile | ConvertFrom-Json }

function ConvertTo-Secure([string] $Text) { ConvertTo-SecureString -String $Text -AsPlainText -Force }

function New-DomainCredential([string] $NetBios, [string] $User, [string] $Password) {
    New-Object System.Management.Automation.PSCredential("$NetBios\$User", (ConvertTo-Secure $Password))
}

# The next phase runs at start-up, as SYSTEM or as the domain administrator. A task of
# the domain administrator cannot start while the domain controller is off, hence the
# restarts.
function Register-Next([string] $Next, $cfg, [switch] $AsDomainAdmin) {
    $action = New-ScheduledTaskAction -Execute 'powershell.exe' `
        -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$root\sharepoint-setup.ps1`" -Phase $Next"
    $trigger = New-ScheduledTaskTrigger -AtStartup
    $settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit (New-TimeSpan -Hours 4) -StartWhenAvailable `
        -RestartCount 12 -RestartInterval (New-TimeSpan -Minutes 10)
    $name = "AtlasSharePointPhase$Next"
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
    Unregister-ScheduledTask -TaskName "AtlasSharePointPhase$N" -Confirm:$false -ErrorAction SilentlyContinue
}

# Retries here, not with curl --retry-all-errors: the curl.exe of Windows Server 2022 is
# too old for that option. -f makes an HTTP error a failure instead of a saved error page.
function Get-File([string] $Url, [string] $Path) {
    for ($attempt = 1; $attempt -le 10; $attempt++) {
        & curl.exe -fsSL -o $Path $Url
        if ($LASTEXITCODE -eq 0) { return }
        Write-Output "download of $Url failed (curl exit $LASTEXITCODE), attempt $attempt"
        Start-Sleep -Seconds 15
    }
    throw "download of $Url failed (curl exit $LASTEXITCODE)"
}

# Never given a password: its arguments go to the log.
# Bounded: an installer that fails into a dialog nobody sees never exits. SSEI 2022 did
# that on 2026-11-09 and held phase 3 until the task's four-hour limit, without a word.
function Invoke-Checked([string] $File, [string[]] $Arguments, [int[]] $Ok = @(0), [int] $TimeoutMinutes = 60) {
    Write-Output "> $File $($Arguments -join ' ')"
    $p = Start-Process -FilePath $File -ArgumentList $Arguments -PassThru -NoNewWindow
    # Without -Wait, Windows PowerShell 5.1 reports ExitCode only if the handle was taken
    # while the process ran; otherwise it is empty and every exit reads as a failure.
    $null = $p.Handle
    if (-not $p.WaitForExit($TimeoutMinutes * 60 * 1000)) {
        & taskkill.exe /PID $p.Id /T /F | Out-Null
        throw "$File did not finish within $TimeoutMinutes minutes; see its log"
    }
    if ($Ok -notcontains $p.ExitCode) { throw "$File exited with $($p.ExitCode)" }
}

function Complete-Phase([string] $N) {
    New-Item -ItemType File -Force -Path (Join-Path $root "sharepoint-phase$N.done") | Out-Null
}

function Wait-Domain {
    $deadline = (Get-Date).AddMinutes(60)
    while ($true) {
        try { Import-Module ActiveDirectory; Get-ADDomain | Out-Null; return }
        catch {
            if ((Get-Date) -gt $deadline) { throw "the domain did not answer: $($_.Exception.Message)" }
            Write-Output 'domain not reachable yet; waiting 60 s'
            Start-Sleep -Seconds 60
        }
    }
}

function New-SetupAccount([string] $Sam, [string] $Password, [string] $Description, $cfg) {
    if (-not (Get-ADUser -Filter "SamAccountName -eq '$Sam'")) {
        New-ADUser -Name $Sam -SamAccountName $Sam -UserPrincipalName "$Sam@$($cfg.domainName)" `
            -Path $cfg.testOu -AccountPassword (ConvertTo-Secure $Password) `
            -Enabled $true -PasswordNeverExpires $true -CannotChangePassword $true -Description $Description
        Write-Output "account $Sam created"
    }
}

function Install-Sql($cfg) {
    if (Get-Service -Name MSSQLSERVER -ErrorAction SilentlyContinue) { Write-Output 'SQL Server is installed'; return }
    New-Item -ItemType Directory -Force -Path $sqlMedia | Out-Null
    $iso = Get-ChildItem -Path $sqlMedia -Filter '*.iso' -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $iso) {
        # SQL Server 2025 Standard Developer: Microsoft offers no current 2022 bootstrapper
        # (aka.ms/sqlserver2022developer serves one its own manifest refuses, 2026-11-09).
        # SharePoint Server SE takes any SQL Server for Windows with compatibility level 150.
        $bootstrap = Join-Path $root 'SQL2025-SSEI-StdDev.exe'
        Get-File 'https://go.microsoft.com/fwlink/?linkid=2344626' $bootstrap
        Invoke-Checked $bootstrap @('/ACTION=Download', "/MEDIAPATH=`"$sqlMedia`"", '/MEDIATYPE=ISO', '/ENU', '/QUIET') -TimeoutMinutes 45
        $iso = Get-ChildItem -Path $sqlMedia -Filter '*.iso' | Select-Object -First 1
        if (-not $iso) { throw "no SQL Server image in $sqlMedia after the download" }
    }
    $img = Get-DiskImage -ImagePath $iso.FullName
    if (-not $img.Attached) { $img = Mount-DiskImage -ImagePath $iso.FullName -PassThru }
    $drive = ($img | Get-Volume).DriveLetter + ':'
    # MAXDOP 1 is what SharePoint requires. The cap leaves SharePoint the rest of 16 GB.
    Invoke-Checked "$drive\setup.exe" @(
        '/Q', '/ACTION=Install', '/IACCEPTSQLSERVERLICENSETERMS', '/FEATURES=SQLENGINE',
        '/INSTANCENAME=MSSQLSERVER', '/SQLSVCACCOUNT="NT Service\MSSQLSERVER"',
        '/AGTSVCACCOUNT="NT Service\SQLSERVERAGENT"',
        "/SQLSYSADMINACCOUNTS=`"$($cfg.netbiosName)\$($cfg.domainAdmin)`"",
        '/SQLCOLLATION=Latin1_General_CI_AS_KS_WS', '/SQLMAXDOP=1', '/SQLMINMEMORY=0', '/SQLMAXMEMORY=6144',
        '/TCPENABLED=1', '/UPDATEENABLED=False'
    ) @(0, 3010) -TimeoutMinutes 90
    Dismount-DiskImage -ImagePath $iso.FullName -ErrorAction SilentlyContinue | Out-Null
}

# Version 24H2 and later refuse a database connection without TLS unless told otherwise,
# and this SQL Server has only its self-signed certificate. Earlier builds, as an image
# may carry, do not know the parameter.
function Get-DatabaseEncryption([string] $Cmdlet) {
    if ((Get-Command $Cmdlet).Parameters.ContainsKey('DatabaseConnectionEncryption')) {
        return @{ DatabaseConnectionEncryption = 'Optional' }
    }
    @{}
}

function Test-InFarm {
    try { return [bool](Get-SPFarm -ErrorAction Stop) } catch { return $false }
}

function Get-ManagedAccount([string] $Name) {
    try { return Get-SPManagedAccount -Identity $Name -ErrorAction Stop } catch { return $null }
}

function New-Farm($cfg) {
    if (-not (Test-InFarm)) {
        $farmCredential = New-DomainCredential $cfg.netbiosName $cfg.farmAccount $cfg.farmPassword
        $encryption = Get-DatabaseEncryption 'New-SPConfigurationDatabase'
        New-SPConfigurationDatabase -DatabaseName 'SP_Config' -DatabaseServer 'sp01' `
            -AdministrationContentDatabaseName 'SP_AdminContent' -FarmCredentials $farmCredential `
            -Passphrase (ConvertTo-Secure $cfg.passphrase) -LocalServerRole SingleServerFarm @encryption | Out-Null
        Write-Output 'farm created'
    }
    Initialize-SPResourceSecurity
    Install-SPService
    Install-SPFeature -AllExistingFeatures -Force | Out-Null
    if (-not (Get-SPWebApplication -IncludeCentralAdministration | Where-Object { $_.IsAdministrationWebApplication })) {
        New-SPCentralAdministration -Port 9999 -WindowsAuthProvider NTLM | Out-Null
        Write-Output 'Central Administration created'
    }
    Install-SPApplicationContent
    Start-Service -Name SPTimerV4
}

function New-WebApplicationWithSite($cfg) {
    $url = $cfg.webAppUrl
    $webApp = Get-SPWebApplication | Where-Object { $_.Url.TrimEnd('/') -eq $url }
    if (-not $webApp) {
        $appAccount = "$($cfg.netbiosName)\$($cfg.appAccount)"
        $managed = Get-ManagedAccount $appAccount
        if (-not $managed) {
            $managed = New-SPManagedAccount -Credential (New-DomainCredential $cfg.netbiosName $cfg.appAccount $cfg.appPassword)
        }
        $encryption = Get-DatabaseEncryption 'New-SPWebApplication'
        $webApp = New-SPWebApplication -Name 'Atlas Test' -Port 80 -HostHeader $cfg.fqdn -Url $url `
            -ApplicationPool 'AtlasTest' -ApplicationPoolAccount $managed `
            -AuthenticationProvider (New-SPAuthenticationProvider) -DatabaseServer 'sp01' `
            -DatabaseName 'SP_Content_AtlasTest' @encryption
        Write-Output "web application $url created"
    }
    $owner = "$($cfg.netbiosName)\$($cfg.domainAdmin)"
    $runbook = "$($cfg.netbiosName)\$($cfg.runbookAccount)"
    if (-not (Get-SPSite -Limit All | Where-Object { $_.Url.TrimEnd('/') -eq $url })) {
        New-SPSite -Url $url -OwnerAlias $owner -SecondaryOwnerAlias $runbook -Name 'Atlas Test' `
            -Template 'STS#3' -Language 1033 | Out-Null
        Write-Output "site collection $url created"
    }
}

# What the SharePoint Server worker signs in with: local administrator for a remote
# session, shell administrator of the farm and of the content database.
function Grant-RunbookRights($cfg) {
    $runbook = "$($cfg.netbiosName)\$($cfg.runbookAccount)"
    if (-not (Get-LocalGroupMember -Group 'Administrators' | Where-Object { $_.Name -eq $runbook })) {
        Add-LocalGroupMember -Group 'Administrators' -Member $runbook
    }
    Add-SPShellAdmin -UserName $runbook
    Add-SPShellAdmin -UserName $runbook -Database (Get-SPContentDatabase -WebApplication $cfg.webAppUrl)
    Grant-RunbookPolicy $cfg.webAppUrl $runbook
}

# Shell admin reaches the content databases, not the sites: EnsureUser on a site the
# worker had just created answered "Attempted to perform an unauthorized operation."
# (2026-11-10). The account gets a web application policy with a role of its own that
# holds what the worker's operations need on a site (resolve users, create and fill the
# default groups, set the site's properties) and nothing to read or write content.
function Grant-RunbookPolicy([string] $WebAppUrl, [string] $Account) {
    $wa = Get-SPWebApplication $WebAppUrl
    $rights = [Microsoft.SharePoint.SPBasePermissions]'Open, ViewPages, BrowseUserInfo, EnumeratePermissions, ManagePermissions, CreateGroups, ManageWeb'
    $role = $wa.PolicyRoles | Where-Object { $_.Name -eq 'Atlas Worker' }
    if ($null -eq $role) {
        $role = $wa.PolicyRoles.Add('Atlas Worker', 'What the Atlas SharePoint Server worker needs on a site; no content.', $rights, [Microsoft.SharePoint.SPBasePermissions]::EmptyMask)
        $wa.Update()
    }
    elseif ($role.GrantRightsMask -ne $rights) {
        $role.GrantRightsMask = $rights
        $wa.Update()
    }
    $claim = (New-SPClaimsPrincipal -Identity $Account -IdentityType WindowsSamAccountName).ToEncodedString()
    $policy = $wa.Policies[$claim]
    if ($null -eq $policy) { $policy = $wa.Policies.Add($claim, 'Atlas SharePoint Server worker') }
    if (-not ($policy.PolicyRoleBindings | Where-Object { $_.Name -eq 'Atlas Worker' })) {
        $policy.PolicyRoleBindings.Add($wa.PolicyRoles['Atlas Worker'])
        $wa.Update()
    }
    Write-Output "policy Atlas Worker for $Account on $WebAppUrl"
}

# The server asks itself for the site under its own name; without this Windows answers
# that loopback request with 401.
function Enable-LoopbackName($cfg) {
    $key = 'HKLM:\SYSTEM\CurrentControlSet\Control\Lsa\MSV1_0'
    New-ItemProperty -Path $key -Name 'BackConnectionHostNames' -PropertyType MultiString `
        -Value @($cfg.fqdn) -Force | Out-Null
}

# The ready marker promises a site that answers. A cold application pool takes minutes.
function Test-Site($cfg) {
    for ($i = 0; $i -lt 20; $i++) {
        try {
            $r = Invoke-WebRequest -Uri $cfg.webAppUrl -UseDefaultCredentials -UseBasicParsing -TimeoutSec 300
            if ($r.StatusCode -eq 200) { Write-Output "$($cfg.webAppUrl) answers 200"; return }
        }
        catch { Write-Output "site not answering yet: $($_.Exception.Message)" }
        Start-Sleep -Seconds 30
    }
    throw "$($cfg.webAppUrl) did not answer"
}

try {
    switch ($Phase) {
        '1' {
            # Run again (the extension re-applied): the server joined and moved on already.
            if (Test-Path (Join-Path $root 'sharepoint-phase1.done')) { Write-Step 'phase 1 done before; nothing to do'; return }
            if (-not $ConfigB64) { throw 'Phase 1 needs -ConfigB64.' }
            $json = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($ConfigB64))
            Set-Content -Path $configFile -Value $json -Encoding UTF8
            Protect-File $configFile
            $cfg = Read-Config

            # Already a member (a run stopped before its marker): Add-Computer would refuse.
            if ((Get-CimInstance Win32_ComputerSystem).Domain -ine $cfg.domainName) {
                # The domain controller may still be finishing its own setup: try for 30 min.
                $joined = $false
                for ($i = 0; $i -lt 30 -and -not $joined; $i++) {
                    try {
                        Add-Computer -DomainName $cfg.domainName -Force `
                            -Credential (New-DomainCredential $cfg.netbiosName $cfg.domainAdmin $cfg.domainAdminPass)
                        $joined = $true
                    }
                    catch { Write-Step "domain join not possible yet: $($_.Exception.Message); waiting 60 s"; Start-Sleep -Seconds 60 }
                }
                if (-not $joined) { throw "could not join $($cfg.domainName)" }
            }
            Register-Next '2' $cfg
            Complete-Phase '1'
            Write-Step 'joined the domain; rebooting'
            # Restart after a delay, so the extension reports success before the reboot.
            & shutdown.exe /r /t 30 /c 'Atlas SharePoint setup, phase 2'
            exit 0
        }
        '2' {
            $cfg = Read-Config
            # Without SharePoint on the image nothing below can work; say so here.
            if (-not (Test-Path 'HKLM:\SOFTWARE\Microsoft\Shared Tools\Web Server Extensions\16.0')) {
                throw 'SharePoint Server is not installed on this image.'
            }
            Install-WindowsFeature RSAT-AD-PowerShell | Out-Null
            # Unregistered only now: a task removed while it runs may be stopped with it.
            Remove-Task '2'
            Register-Next '3' $cfg -AsDomainAdmin
            Complete-Phase '2'
            Stop-Transcript | Out-Null
            Restart-Computer -Force
            return
        }
        '3' {
            $cfg = Read-Config
            Wait-Domain
            New-SetupAccount $cfg.farmAccount $cfg.farmPassword 'SharePoint farm account (Atlas test environment)' $cfg
            New-SetupAccount $cfg.appAccount $cfg.appPassword 'SharePoint application pool (Atlas test environment)' $cfg
            New-SetupAccount $cfg.runbookAccount $cfg.runbookPassword 'SharePoint Server worker (Atlas test environment)' $cfg
            Install-Sql $cfg
            Remove-Task '3'
            Register-Next '4' $cfg -AsDomainAdmin
            Complete-Phase '3'
            Stop-Transcript | Out-Null
            Restart-Computer -Force
            return
        }
        '4' {
            $cfg = Read-Config
            Wait-Domain
            Start-Service -Name MSSQLSERVER
            # The module since Version 22H2; the snap-in before it.
            if (Get-Module -ListAvailable -Name SharePointServer) { Import-Module SharePointServer -DisableNameChecking }
            else { Add-PSSnapin Microsoft.SharePoint.PowerShell }

            Enable-LoopbackName $cfg
            New-Farm $cfg
            New-WebApplicationWithSite $cfg
            Grant-RunbookRights $cfg
            Test-Site $cfg

            Remove-Item -Force $configFile
            Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $sqlMedia
            Remove-Task '4'
            Complete-Phase '4'
            Set-Content -Path (Join-Path $root 'sharepoint.done') -Value (Get-Date -Format o)
            Write-Output 'SharePoint Server is ready.'
        }
    }
}
catch {
    Write-Step "FAILED in phase ${Phase}: $($_.Exception.Message)"
    Set-Content -Path $failedFile -Value "phase ${Phase}: $($_.Exception.Message)"
    throw
}
finally {
    # A phase that reboots has ended its transcript already; Stop-Transcript then throws.
    if ($Phase -ne '1') { try { Stop-Transcript | Out-Null } catch { } }
}

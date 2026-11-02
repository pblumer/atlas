<#
.SYNOPSIS
  Builds the Atlas AD test forest on a fresh Windows Server VM (deploy/opentofu/m365-testenv).

.DESCRIPTION
  Phase 1 (run once by the Custom Script Extension):
    - installs AD DS, keeps the phase-2 settings in a file only SYSTEM and
      Administrators can read, registers phase 2 as a start-up task,
      creates the forest and reboots.
  Phase 2 (run by the start-up task after the reboot):
    - waits for Active Directory Web Services,
    - creates OU=AtlasService and OU=AtlasTest,
    - creates the worker's bind account in OU=AtlasService with full control over
      OU=AtlasTest (and only there) plus "Replicating Directory Changes" on the
      domain, so the sync operation (DirSync) can be tested,
    - creates the test users and a test group in OU=AtlasTest,
    - asks AD DS to load the LDAPS certificate, removes the settings file and the task.
  Log: C:\AtlasSetup\setup-phase<N>.log. Done marker: C:\AtlasSetup\phase2.done.

  Written for Windows PowerShell 5.1.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)] [ValidateSet('1', '2')] [string] $Phase,
    [string] $ConfigB64
)

$ErrorActionPreference = 'Stop'
$root = 'C:\AtlasSetup'
$configFile = Join-Path $root 'phase2.json'
New-Item -ItemType Directory -Force -Path $root | Out-Null
Start-Transcript -Path (Join-Path $root "setup-phase$Phase.log") -Append | Out-Null

function Protect-File([string] $Path) {
    # Only SYSTEM and the local Administrators may read the settings (they hold passwords).
    & icacls.exe $Path /inheritance:r /grant:r 'SYSTEM:(F)' 'BUILTIN\Administrators:(F)' | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "icacls failed on $Path" }
}

function ConvertTo-PlainSecure([string] $Text) {
    ConvertTo-SecureString -String $Text -AsPlainText -Force
}

try {
    if ($Phase -eq '1') {
        if (-not $ConfigB64) { throw 'Phase 1 needs -ConfigB64.' }
        $json = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($ConfigB64))
        $cfg = $json | ConvertFrom-Json

        # The LDAPS certificate must already be in LocalMachine\My (installed from Key
        # Vault at provisioning); without it 636 would never answer, so stop here.
        $cert = Get-ChildItem Cert:\LocalMachine\My | Where-Object { $_.Subject -eq $cfg.ldapsSubject -and $_.HasPrivateKey }
        if (-not $cert) { throw "No certificate with subject '$($cfg.ldapsSubject)' and private key in LocalMachine\My." }
        Write-Output "LDAPS certificate present: $($cert.Thumbprint)"

        Set-Content -Path $configFile -Value $json -Encoding UTF8
        Protect-File $configFile

        Install-WindowsFeature -Name AD-Domain-Services -IncludeManagementTools | Out-Null

        $action = New-ScheduledTaskAction -Execute 'powershell.exe' `
            -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$root\ad-setup.ps1`" -Phase 2"
        $trigger = New-ScheduledTaskTrigger -AtStartup
        $principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
        $settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit (New-TimeSpan -Hours 1) -StartWhenAvailable
        Register-ScheduledTask -TaskName 'AtlasAdPhase2' -Action $action -Trigger $trigger `
            -Principal $principal -Settings $settings -Force | Out-Null

        Import-Module ADDSDeployment
        Install-ADDSForest `
            -DomainName $cfg.domainName `
            -DomainNetbiosName $cfg.netbiosName `
            -SafeModeAdministratorPassword (ConvertTo-PlainSecure $cfg.safeModePassword) `
            -InstallDns `
            -NoRebootOnCompletion `
            -Force | Out-Null

        # Reboot shortly after the extension has reported success.
        & shutdown.exe /r /t 30 /c 'Atlas AD test forest: reboot after promotion' /d p:4:2
        Write-Output 'Phase 1 complete; rebooting.'
        exit 0
    }

    # ---------------- Phase 2 ----------------
    if (-not (Test-Path $configFile)) { Write-Output 'No settings file; phase 2 already done.'; exit 0 }
    $cfg = Get-Content -Raw -Path $configFile | ConvertFrom-Json

    $deadline = (Get-Date).AddMinutes(30)
    while ($true) {
        try { Import-Module ActiveDirectory; $domain = Get-ADDomain; break }
        catch {
            if ((Get-Date) -gt $deadline) { throw "Active Directory did not become available: $_" }
            Start-Sleep -Seconds 20
        }
    }
    $domainDN = $domain.DistinguishedName
    $netbios = $domain.NetBIOSName

    foreach ($ou in 'AtlasService', 'AtlasTest') {
        if (-not (Get-ADOrganizationalUnit -LDAPFilter "(ou=$ou)" -SearchBase $domainDN -SearchScope OneLevel)) {
            New-ADOrganizationalUnit -Name $ou -Path $domainDN -ProtectedFromAccidentalDeletion $false
        }
    }
    $serviceOU = "OU=AtlasService,$domainDN"
    $testOU = "OU=AtlasTest,$domainDN"

    $svc = $cfg.serviceAccount
    if (-not (Get-ADUser -Filter "sAMAccountName -eq '$svc'")) {
        New-ADUser -Name $svc -SamAccountName $svc -UserPrincipalName "$svc@$($cfg.domainName)" `
            -Path $serviceOU -AccountPassword (ConvertTo-PlainSecure $cfg.servicePassword) `
            -PasswordNeverExpires $true -CannotChangePassword $true -Enabled $true `
            -Description 'Atlas AD worker bind account (test environment)'
    }

    # Full control over the test OU and everything below it - and nothing else.
    & dsacls.exe $testOU /I:T /G "$netbios\${svc}:GA" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'dsacls (OU delegation) failed' }
    # DirSync needs this control-access right on the domain head.
    & dsacls.exe $domainDN /G "$netbios\${svc}:CA;Replicating Directory Changes" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'dsacls (Replicating Directory Changes) failed' }

    $members = @()
    foreach ($u in $cfg.testUsers) {
        if (-not (Get-ADUser -Filter "sAMAccountName -eq '$($u.sam)'")) {
            New-ADUser -Name "$($u.givenName) $($u.surname) [$($u.sam)]" -SamAccountName $u.sam `
                -UserPrincipalName $u.upn -GivenName $u.givenName -Surname $u.surname `
                -DisplayName "$($u.givenName) $($u.surname)" -Path $testOU `
                -AccountPassword (ConvertTo-PlainSecure $cfg.testUserPassword) -Enabled $true `
                -Department 'Atlas Test'
        }
        $members += $u.sam
    }
    if (-not (Get-ADGroup -Filter "Name -eq '$($cfg.groupName)'")) {
        New-ADGroup -Name $cfg.groupName -GroupScope Global -GroupCategory Security -Path $testOU `
            -Description 'Atlas test group'
    }
    if ($members.Count -gt 0) { Add-ADGroupMember -Identity $cfg.groupName -Members $members }

    # AD DS loads a server certificate at start-up; ask it once more in case the
    # certificate store was read before the certificate was usable.
    try {
        $rootDse = [ADSI]'LDAP://localhost/RootDSE'
        $rootDse.Put('renewServerCertificate', 1)
        $rootDse.SetInfo()
    } catch { Write-Output "renewServerCertificate: $_ (ignored)" }

    Remove-Item -Force $configFile
    Set-Content -Path (Join-Path $root 'phase2.done') -Value (Get-Date -Format o)
    Unregister-ScheduledTask -TaskName 'AtlasAdPhase2' -Confirm:$false
    Write-Output 'Phase 2 complete.'
}
catch {
    Write-Output "FAILED: $_"
    throw
}
finally {
    Stop-Transcript | Out-Null
}

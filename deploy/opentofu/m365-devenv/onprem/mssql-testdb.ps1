<#
.SYNOPSIS
  Prepares the SQL Server on the SharePoint VM for the MS SQL worker test (scripts/mssql-testdb.sh).

.DESCRIPTION
  Runs as SYSTEM through Azure's managed run command. It
    1. switches the default instance to mixed-mode authentication (LoginMode 2) and
       restarts it when that changed: SharePoint loses its database for about half a
       minute and reconnects on its own;
    2. opens TCP 1433 in Windows Firewall for AllowFrom only (the Atlas VM's subnet);
    3. runs, as the domain administrator (the instance's only sysadmin), a T-SQL batch
       that creates the database AtlasTest_HR with the table dbo.personen the example
       examples/mssql-eintrittsmeldung.bpmn reads and writes, resets its rows to the
       test data, and creates or re-keys the SQL login atlas_hr with db_datareader and
       db_datawriter on this database only. The SharePoint databases are not touched.

  Re-running is safe and gives the same state, with the login's new password.
  The passwords arrive as protected parameters and are written nowhere but a batch file
  readable by SYSTEM and Administrators only, deleted when the batch has run.

  Written for Windows PowerShell 5.1.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)] [string] $DomainUser,
    [Parameter(Mandatory)] [string] $DomainPassword,
    [Parameter(Mandatory)] [string] $LoginPassword,
    [Parameter(Mandatory)] [string] $AllowFrom
)
$ErrorActionPreference = 'Stop'
$root = 'C:\AtlasSetup'
New-Item -ItemType Directory -Force -Path $root | Out-Null

# The login's password is quoted into T-SQL; the caller generates letters and digits only,
# and anything else is refused here rather than escaped.
if ($LoginPassword -notmatch '^[A-Za-z0-9]{20,}$') { throw 'LoginPassword must be 20 or more letters and digits' }

# 1. Mixed mode.
$instance = (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Microsoft SQL Server\Instance Names\SQL').MSSQLSERVER
$key = "HKLM:\SOFTWARE\Microsoft\Microsoft SQL Server\$instance\MSSQLServer"
if ((Get-ItemProperty $key).LoginMode -ne 2) {
    Set-ItemProperty -Path $key -Name LoginMode -Value 2
    Restart-Service -Name MSSQLSERVER -Force
    (Get-Service MSSQLSERVER).WaitForStatus('Running', (New-TimeSpan -Minutes 3))
    Write-Output 'mixed mode on; SQL Server restarted'
}
else { Write-Output 'mixed mode was on' }

# 2. Firewall.
$rule = 'Atlas MS SQL test (1433)'
Get-NetFirewallRule -DisplayName $rule -ErrorAction SilentlyContinue | Remove-NetFirewallRule
New-NetFirewallRule -DisplayName $rule -Direction Inbound -Protocol TCP -LocalPort 1433 `
    -RemoteAddress $AllowFrom -Action Allow | Out-Null
Write-Output "TCP 1433 open for $AllowFrom"

# 3. Database, table, data, login: as the domain administrator.
$sql = @"
SET NOCOUNT ON;
IF DB_ID(N'AtlasTest_HR') IS NULL CREATE DATABASE AtlasTest_HR;
GO
USE AtlasTest_HR;
IF OBJECT_ID(N'dbo.personen') IS NULL
    CREATE TABLE dbo.personen (
        id        INT IDENTITY(1,1) PRIMARY KEY,
        kuerzel   NVARCHAR(16)  NOT NULL UNIQUE,
        mail      NVARCHAR(256) NULL,
        abteilung NVARCHAR(64)  NULL,
        aktiv     BIT           NOT NULL DEFAULT 0
    );
DELETE FROM dbo.personen;
INSERT INTO dbo.personen (kuerzel, mail, abteilung, aktiv) VALUES
    (N'cbr', N'claudia.brunner@helvetia-werkstatt.example', N'GL',   0),
    (N'rke', N'reto.keller@helvetia-werkstatt.example',     N'GL',   0),
    (N'sme', N'sandra.meier@helvetia-werkstatt.example',    N'PROJ', 0),
    (N'lbe', N'luca.bernasconi@helvetia-werkstatt.example', N'PROJ', 0),
    (N'nfr', N'nina.frei@helvetia-werkstatt.example',       N'PROJ', 0),
    (N'mhu', N'martina.huber@helvetia-werkstatt.example',   N'ADM',  0);
GO
USE master;
IF SUSER_ID(N'atlas_hr') IS NULL
    CREATE LOGIN atlas_hr WITH PASSWORD = N'$LoginPassword', DEFAULT_DATABASE = AtlasTest_HR, CHECK_POLICY = ON, CHECK_EXPIRATION = OFF;
ELSE
    ALTER LOGIN atlas_hr WITH PASSWORD = N'$LoginPassword';
GO
USE AtlasTest_HR;
IF USER_ID(N'atlas_hr') IS NULL CREATE USER atlas_hr FOR LOGIN atlas_hr;
ALTER ROLE db_datareader ADD MEMBER atlas_hr;
ALTER ROLE db_datawriter ADD MEMBER atlas_hr;
SELECT COUNT(*) AS personen FROM dbo.personen;
GO
"@
$batch = Join-Path $root 'mssql-testdb.sql'
$log = Join-Path $root 'mssql-testdb.log'
$done = Join-Path $root 'mssql-testdb.exit'
Remove-Item $log, $done -ErrorAction SilentlyContinue
New-Item -ItemType File -Force -Path $batch | Out-Null
& icacls.exe $batch /inheritance:r /grant:r 'SYSTEM:(F)' 'BUILTIN\Administrators:(F)' | Out-Null
Set-Content -Path $batch -Value $sql -Encoding UTF8
try {
    # -C: ODBC 18 encrypts by default, and the instance's certificate is self-signed.
    $sqlcmd = (Get-Command sqlcmd.exe).Source
    $cmd = "& '$sqlcmd' -S localhost -E -C -b -i '$batch' -o '$log'; Set-Content -Path '$done' -Value `$LASTEXITCODE"
    $action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument "-NoProfile -ExecutionPolicy Bypass -Command `"$cmd`""
    $task = 'AtlasMssqlTestDb'
    Register-ScheduledTask -TaskName $task -Action $action -User $DomainUser -Password $DomainPassword `
        -RunLevel Highest -Force | Out-Null
    Start-ScheduledTask -TaskName $task
    $deadline = (Get-Date).AddMinutes(5)
    while (-not (Test-Path $done)) {
        if ((Get-Date) -gt $deadline) { throw 'the T-SQL batch did not finish within 5 minutes' }
        Start-Sleep -Seconds 3
    }
    Unregister-ScheduledTask -TaskName $task -Confirm:$false
}
finally {
    Remove-Item $batch -Force -ErrorAction SilentlyContinue
}
$exit = (Get-Content $done).Trim()
Get-Content $log -ErrorAction SilentlyContinue
if ($exit -ne '0') { throw "sqlcmd exited with $exit" }
Write-Output 'AtlasTest_HR ready'

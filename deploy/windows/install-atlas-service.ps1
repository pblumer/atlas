<#
.SYNOPSIS
    Installiert Atlas als Windows-Dienst (ueber den Service-Wrapper WinSW),
    aktualisiert eine bestehende Installation oder entfernt sie wieder.

.DESCRIPTION
    atlas.exe ist ein Konsolenprogramm und implementiert das Protokoll des
    Windows Service Control Manager nicht. Ein Dienst, der mit sc.exe direkt auf
    atlas.exe zeigt, startet daher nicht. Dieses Skript verwendet WinSW als
    Wrapper und fuehrt alle Schritte aus docs/install.md ("Windows Server") aus:

      1.  Voraussetzungen pruefen (Administratorrechte, kein fremder atlas-Prozess,
          kein fremder Dienst gleichen Namens)
      2.  Verzeichnisstruktur unter -InstallRoot anlegen
      3.  atlas.exe finden (vorhandene Datei unter -InstallRoot) oder herunterladen
          und pruefen, nach <InstallRoot>\bin kopieren
      4.  WinSW bereitstellen und Pruefsumme kontrollieren
      5.  Testlauf im Vordergrund mit einem Wegwerf-Datenverzeichnis
      6.  Script-Interpreter (pwsh, python3, node) im Maschinen-PATH pruefen
      7.  Vault-Schluessel und Bootstrap-Administrator vorbereiten
      8.  WinSW-Konfiguration schreiben, Dienst installieren, auf das virtuelle
          Konto NT SERVICE\<Dienstname> umstellen
      9.  Zugriffsrechte (ACL) setzen
      10. Dienst starten und auf Bereitschaft warten
      11. Bootstrap-Passwort aus der Konfiguration entfernen und neu starten
      12. Optional: Firewall-Regel, Defender-Ausnahme

    Ist der Dienst bereits installiert, arbeitet das Skript im Update-Modus:
    Dienst stoppen, Datenverzeichnis sichern, atlas.exe ersetzen (falls eine
    andere Datei gefunden wurde), Konfiguration neu schreiben, Dienst starten.

    Das Skript ist fuer Windows PowerShell 5.1 geschrieben und laeuft auch unter
    PowerShell 7.

.PARAMETER InstallRoot
    Basisverzeichnis der Installation. Standard: D:\atlas

.PARAMETER AtlasExe
    Expliziter Pfad zu atlas.exe. Ohne Angabe wird unter -InstallRoot gesucht
    (ohne bin, data, backup, service); bei mehreren Treffern gewinnt die neueste.

.PARAMETER Version
    Nur noetig, wenn keine atlas.exe vorhanden ist: laedt diese Release-Version
    von GitHub herunter und prueft sie gegen SHA256SUMS, z.B. 0.8.0

.PARAMETER DataDir
    Datenverzeichnis. Ohne Angabe: <InstallRoot>\data, oder ein vorhandenes
    <InstallRoot>\atlas-data (Standard von atlas.exe bei einem Start ohne
    --data-dir aus diesem Ordner).

.PARAMETER WinSWPath
    Lokale WinSW-x64.exe (fuer Server ohne Internetzugang). Ohne Angabe wird
    eine einzelne WinSW*.exe direkt unter -InstallRoot verwendet, sonst
    WinSW v2.12.0 von GitHub geladen.

.PARAMETER WinSWSha256
    Erwartete SHA-256-Pruefsumme von WinSW. Leer = keine Pruefung.

.PARAMETER Addr
    Listen-Adresse. Standard: 127.0.0.1:8080 (nur lokal erreichbar).

.PARAMETER TlsCert
    PEM-Zertifikatskette. Wird nach <InstallRoot>\config\tls.crt kopiert.

.PARAMETER TlsKey
    PEM-Schluessel zu -TlsCert. Wird nach <InstallRoot>\config\tls.key kopiert.

.PARAMETER ExternalUrl
    Oeffentliche Adresse, z.B. https://atlas.example.ch. Hinter einem
    Reverse-Proxy (IIS/ARR) zwingend.

.PARAMETER AdminUsername
    Name des Bootstrap-Administrators. Standard: admin

.PARAMETER AdminPassword
    Passwort des Bootstrap-Administrators als SecureString. Ohne Angabe wird
    ein zufaelliges Passwort erzeugt und am Ende einmal angezeigt. Wird nur
    verwendet, solange noch kein Benutzer existiert.

.PARAMETER VaultKeyMode
    Generated (Standard): Atlas erzeugt <DataDir>\vault.key selbst.
    File: das Skript erzeugt <InstallRoot>\config\vault.key und uebergibt ihn
    ueber ATLAS_VAULT_KEY_FILE. Nur bei einer Neuinstallation waehlbar.

.PARAMETER OpenFirewall
    Legt eine eingehende Firewall-Regel fuer den Port aus -Addr an.

.PARAMETER FirewallProfile
    Firewall-Profile fuer die Regel. Standard: Domain, Private

.PARAMETER AllowPlaintext
    Erlaubt eine nicht-lokale -Addr ohne TLS. Nicht empfohlen.

.PARAMETER DefenderExclusion
    Nimmt das Datenverzeichnis vom Echtzeit-Scan von Microsoft Defender aus.

.PARAMETER KeepAllInterpreters
    Script-Interpreter, die im Maschinen-PATH fehlen, nicht deaktivieren.

.PARAMETER ExtraArgs
    Zusaetzliche Argumente fuer "atlas serve", z.B. @('--checkpoint-keep','5')

.PARAMETER SkipSmokeTest
    Testlauf im Vordergrund ueberspringen.

.PARAMETER SkipBackup
    Im Update-Modus keine Sicherung des Datenverzeichnisses anlegen.

.PARAMETER Uninstall
    Dienst und Firewall-Regel entfernen. Daten bleiben erhalten.

.PARAMETER RemoveData
    Zusammen mit -Uninstall: bin, config, data, logs und service loeschen.
    Das Verzeichnis backup bleibt erhalten.

.EXAMPLE
    # Neuinstallation, nur lokal erreichbar
    powershell -ExecutionPolicy Bypass -File .\install-atlas-service.ps1

.EXAMPLE
    # Mit TLS im Binary, von aussen erreichbar
    .\install-atlas-service.ps1 -Addr ':8443' -TlsCert C:\certs\atlas.crt -TlsKey C:\certs\atlas.key `
        -ExternalUrl 'https://atlas.example.ch:8443' -OpenFirewall

.EXAMPLE
    # Update: neue atlas.exe nach D:\atlas\releases\0.9.0 entpacken, dann
    .\install-atlas-service.ps1 -AtlasExe D:\atlas\releases\0.9.0\atlas.exe

.EXAMPLE
    .\install-atlas-service.ps1 -Uninstall
#>
# Diese Datei ist bewusst reines ASCII (Umlaute als ae/oe/ue): Windows PowerShell
# 5.1 liest eine Datei ohne erkannte UTF-8-BOM als Windows-1252, und schon ein
# einziges Umlaut-Byte zerlegt dann den Hilfeblock oben in Code.
#Requires -RunAsAdministrator
[CmdletBinding()]
param(
    [string]$InstallRoot = 'D:\atlas',
    [string]$ServiceName = 'atlas',
    [string]$AtlasExe,
    [string]$Version,
    [string]$DataDir,
    [string]$WinSWPath,
    [string]$WinSWUrl = 'https://github.com/winsw/winsw/releases/download/v2.12.0/WinSW-x64.exe',
    [string]$WinSWSha256 = '05b82d46ad331cc16bdc00de5c6332c1ef818df8ceefcd49c726553209b3a0da',
    [string]$Addr = '127.0.0.1:8080',
    [string]$TlsCert,
    [string]$TlsKey,
    [string]$ExternalUrl,
    [string]$AdminUsername = 'admin',
    [SecureString]$AdminPassword,
    [ValidateSet('Generated', 'File')]
    [string]$VaultKeyMode = 'Generated',
    [switch]$OpenFirewall,
    [ValidateSet('Domain', 'Private', 'Public')]
    [string[]]$FirewallProfile = @('Domain', 'Private'),
    [switch]$AllowPlaintext,
    [switch]$DefenderExclusion,
    [switch]$KeepAllInterpreters,
    [string[]]$ExtraArgs = @(),
    [int]$ReadyTimeoutSec = 300,
    [switch]$SkipSmokeTest,
    [switch]$SkipBackup,
    [switch]$Uninstall,
    [switch]$RemoveData
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'   # beschleunigt Invoke-WebRequest unter 5.1

# Gruppen per SID, damit das Skript auch auf lokalisierten Systemen
# ("Administratoren") funktioniert.
$SidAdmins = '*S-1-5-32-544'
$SidSystem = '*S-1-5-18'

# ---------------------------------------------------------------------------
# Hilfsfunktionen
# ---------------------------------------------------------------------------

function Write-Step([string]$Text) {
    Write-Host ''
    Write-Host "==> $Text" -ForegroundColor Cyan
}

function Write-Info([string]$Text) {
    Write-Host "    $Text"
}

function Invoke-Native {
    # Fuehrt ein natives Programm aus und bricht bei Exit-Code <> 0 ab.
    # Windows PowerShell 5.1 wertet stderr-Ausgaben nativer Programme bei
    # ErrorActionPreference=Stop als Fehler; massgeblich ist hier der Exit-Code.
    param([string]$File, [string[]]$Arguments, [int[]]$OkCodes = @(0))
    $ErrorActionPreference = 'Continue'
    $output = & $File @Arguments 2>&1 | ForEach-Object { "$_" }
    if ($OkCodes -notcontains $LASTEXITCODE) {
        $output | ForEach-Object { Write-Host "    $_" }
        throw "$File $($Arguments -join ' ') ist mit Exit-Code $LASTEXITCODE fehlgeschlagen."
    }
    return $output
}

function Invoke-Icacls {
    param([string]$Path, [string[]]$Arguments)
    Invoke-Native -File 'icacls.exe' -Arguments (@($Path) + $Arguments) | Out-Null
}

function Get-ExeVersion([string]$Path) {
    # Erste Zeile von "atlas.exe version"; $null, wenn das Programm nicht laeuft.
    $ErrorActionPreference = 'Continue'
    $output = @(& $Path version 2>&1 | ForEach-Object { "$_" })
    if ($LASTEXITCODE -ne 0 -or $output.Count -eq 0) { return $null }
    return $output[0]
}

function Invoke-Download([string]$Uri, [string]$OutFile, [string]$OfflineHint) {
    # Laedt eine Datei herunter. Bei einem Fehler wird eine halb geschriebene
    # Datei entfernt, damit ein erneuter Lauf sie nicht ungeprueft weiterverwendet.
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    Write-Info "Download $Uri"
    try {
        Invoke-WebRequest -Uri $Uri -OutFile $OutFile -UseBasicParsing
    } catch {
        Remove-Item -LiteralPath $OutFile -Force -ErrorAction SilentlyContinue
        $msg = $_.Exception.Message
        $hint = ''
        if ($msg -match 'trust relationship|Vertrauensstellung') {
            $hint = " Das Serverzertifikat wird von diesem Server nicht als vertrauenswuerdig eingestuft (TLS-Inspection durch einen Proxy oder fehlende Stammzertifikate). Die Zertifikatspruefung nicht abschalten."
        } elseif ($msg -match 'SSL/TLS') {
            $hint = ' TLS-Verbindung nicht moeglich (Protokoll oder Proxy).'
        }
        throw "Download von $Uri fehlgeschlagen: $msg$hint Alternative: Datei auf einem anderen Rechner herunterladen und $OfflineHint verwenden."
    }
}

function Get-FileSha256([string]$Path) {
    return (Get-FileHash -Path $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function New-RandomBytes([int]$Count) {
    $bytes = New-Object byte[] $Count
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    return , $bytes
}

function New-RandomPassword([int]$Length = 24) {
    # Ohne verwechselbare Zeichen (0/O, 1/l/I); Rejection Sampling gegen Bias.
    $alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789'
    $limit = 256 - (256 % $alphabet.Length)
    $sb = New-Object Text.StringBuilder
    while ($sb.Length -lt $Length) {
        foreach ($b in (New-RandomBytes 64)) {
            if ($b -lt $limit -and $sb.Length -lt $Length) {
                [void]$sb.Append($alphabet[$b % $alphabet.Length])
            }
        }
    }
    return $sb.ToString()
}

function ConvertFrom-SecureStringPlain([SecureString]$Secure) {
    $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Secure)
    try { return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr) }
    finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr) }
}

function Write-Utf8NoBom([string]$Path, [string]$Content) {
    [IO.File]::WriteAllText($Path, $Content, (New-Object Text.UTF8Encoding $false))
}

function Split-ListenAddr([string]$Value) {
    $i = $Value.LastIndexOf(':')
    if ($i -lt 0) { throw "Ungueltige -Addr '$Value' (erwartet z.B. 127.0.0.1:8080 oder :8443)." }
    $h = $Value.Substring(0, $i).Trim('[', ']')
    $p = 0
    if (-not [int]::TryParse($Value.Substring($i + 1), [ref]$p) -or $p -lt 1 -or $p -gt 65535) {
        throw "Ungueltiger Port in -Addr '$Value'."
    }
    $loopback = @('127.0.0.1', 'localhost', '::1') -contains $h.ToLowerInvariant()
    $probe = $h
    if ($h -eq '' -or $h -eq '0.0.0.0' -or $h -eq '::') { $probe = '127.0.0.1' }
    if ($probe -eq '::1') { $probe = '[::1]' }
    return [pscustomobject]@{ Host = $h; Port = $p; Loopback = $loopback; ProbeHost = $probe }
}

function Get-FreeTcpPort {
    $listener = New-Object Net.Sockets.TcpListener ([Net.IPAddress]::Loopback, 0)
    $listener.Start()
    try { return $listener.LocalEndpoint.Port } finally { $listener.Stop() }
}

function Find-OnMachinePath([string]$FileName) {
    # Der Dienst sieht nur den Maschinen-PATH, nicht den PATH des Administrators.
    $machinePath = [Environment]::GetEnvironmentVariable('Path', 'Machine')
    foreach ($dir in ($machinePath -split ';')) {
        if ([string]::IsNullOrWhiteSpace($dir)) { continue }
        $candidate = Join-Path ([Environment]::ExpandEnvironmentVariables($dir.Trim())) $FileName
        if (Test-Path -LiteralPath $candidate -PathType Leaf) { return $candidate }
    }
    return $null
}

function Read-SharedText([string]$Path, [long]$Offset) {
    # Liest eine Datei, die WinSW gerade beschreibt (FileShare.ReadWrite).
    $fs = [IO.File]::Open($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::ReadWrite)
    try {
        if ($Offset -gt $fs.Length) { $Offset = 0 }
        [void]$fs.Seek($Offset, [IO.SeekOrigin]::Begin)
        $reader = New-Object IO.StreamReader($fs)
        return $reader.ReadToEnd()
    } finally { $fs.Dispose() }
}

function Get-LogOffsets {
    $offsets = @{}
    if (Test-Path -LiteralPath $LogsDir) {
        Get-ChildItem -LiteralPath $LogsDir -Filter '*.log' -File | ForEach-Object { $offsets[$_.FullName] = $_.Length }
    }
    return $offsets
}

function Test-LogContains([hashtable]$Offsets, [string]$Needle) {
    if (-not (Test-Path -LiteralPath $LogsDir)) { return $false }
    foreach ($f in (Get-ChildItem -LiteralPath $LogsDir -Filter '*.log' -File)) {
        $offset = 0
        if ($Offsets.ContainsKey($f.FullName)) { $offset = $Offsets[$f.FullName] }
        try {
            if ((Read-SharedText -Path $f.FullName -Offset $offset).Contains($Needle)) { return $true }
        } catch { }
    }
    return $false
}

function Show-LogTail([int]$Lines = 40) {
    if (-not (Test-Path -LiteralPath $LogsDir)) { return }
    Get-ChildItem -LiteralPath $LogsDir -Filter '*.log' -File | Sort-Object LastWriteTime -Descending | Select-Object -First 3 | ForEach-Object {
        Write-Host "    --- $($_.Name) (letzte $Lines Zeilen) ---" -ForegroundColor Yellow
        try {
            (Read-SharedText -Path $_.FullName -Offset 0) -split "`r?`n" | Select-Object -Last $Lines | ForEach-Object { Write-Host "    $_" }
        } catch { Write-Host "    (nicht lesbar: $($_.Exception.Message))" }
    }
}

function Wait-AtlasReady([hashtable]$Offsets) {
    # Bereit ist Atlas, wenn die Logzeile "recovery is complete" erscheint;
    # ohne TLS zusaetzlich ueber /readyz geprueft.
    $deadline = (Get-Date).AddSeconds($ReadyTimeoutSec)
    while ((Get-Date) -lt $deadline) {
        Start-Sleep -Seconds 2
        $svc = Get-Service -Name $ServiceName
        if ($svc.Status -eq 'Stopped') { return $false }
        if (-not (Test-LogContains -Offsets $Offsets -Needle 'recovery is complete')) { continue }
        if ($UseTls) { return $true }
        try {
            $r = Invoke-WebRequest -Uri $ProbeUrl -UseBasicParsing -TimeoutSec 5
            if ($r.StatusCode -eq 200) { return $true }
        } catch { }
    }
    return $false
}

function Stop-AtlasService {
    $svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if ($null -ne $svc -and $svc.Status -ne 'Stopped') {
        Write-Info "Dienst '$ServiceName' wird gestoppt (bis zu 30 s fuer ein geordnetes Herunterfahren) ..."
        Stop-Service -Name $ServiceName
    }
    $deadline = (Get-Date).AddSeconds(60)
    while ((Get-AtlasProcesses).Count -gt 0 -and (Get-Date) -lt $deadline) { Start-Sleep -Seconds 1 }
    if ((Get-AtlasProcesses).Count -gt 0) {
        throw "Nach dem Stoppen des Dienstes laeuft noch ein atlas.exe-Prozess aus $BinDir."
    }
}

function Get-AtlasProcesses([switch]$All) {
    $procs = @(Get-Process -Name 'atlas' -ErrorAction SilentlyContinue)
    if ($All) { return $procs }
    return @($procs | Where-Object { $_.Path -eq $BinExe })
}

function Get-ServiceSid {
    $account = New-Object Security.Principal.NTAccount('NT SERVICE', $ServiceName)
    return '*' + $account.Translate([Security.Principal.SecurityIdentifier]).Value
}

function Protect-VaultKey([string]$Path, [string]$Sid) {
    # Nur das Dienstkonto darf den Schluessel lesen (vgl. docs/install.md, Schritt 7).
    # Gehoert die Datei bereits dem Dienstkonto, darf ein Administrator ihre ACL
    # erst nach takeown aendern. Der Eigentuemer (Administratoren) steht nicht in
    # der DACL; diese enthaelt danach genau einen Eintrag.
    if (Test-Path -LiteralPath $Path) {
        Invoke-Native -File 'takeown.exe' -Arguments @('/F', $Path, '/A') | Out-Null
        Invoke-Icacls -Path $Path -Arguments @('/inheritance:r', '/grant:r', "${Sid}:F")
    }
}

function Unlock-ForAdmins([string]$Path) {
    # Holt eine Datei zurueck, auf die nur das Dienstkonto Zugriff hat (vault.key).
    if (Test-Path -LiteralPath $Path) {
        Invoke-Native -File 'takeown.exe' -Arguments @('/F', $Path, '/A') | Out-Null
        Invoke-Icacls -Path $Path -Arguments @('/grant', "${SidAdmins}:F")
    }
}

function New-ServiceXml([string[]]$AtlasArgs, $EnvVars) {
    $esc = { param($s) [Security.SecurityElement]::Escape([string]$s) }
    $argLine = ($AtlasArgs | ForEach-Object { if ($_ -match '\s') { '"' + $_ + '"' } else { $_ } }) -join ' '
    $lines = New-Object Collections.Generic.List[string]
    $lines.Add('<?xml version="1.0" encoding="utf-8"?>')
    $lines.Add('<!-- Erzeugt von install-atlas-service.ps1. Aenderungen werden beim naechsten Lauf ueberschrieben. -->')
    $lines.Add('<service>')
    $lines.Add("  <id>$(& $esc $ServiceName)</id>")
    $lines.Add('  <name>Atlas BPMN workflow engine</name>')
    $lines.Add('  <description>Durable BPMN 2.x workflow engine (atlas serve).</description>')
    $lines.Add("  <executable>$(& $esc $BinExe)</executable>")
    $lines.Add("  <arguments>$(& $esc $argLine)</arguments>")
    $lines.Add("  <workingdirectory>$(& $esc $InstallRoot)</workingdirectory>")
    $lines.Add('  <startmode>Automatic</startmode>')
    $lines.Add('  <onfailure action="restart" delay="10 sec"/>')
    $lines.Add('  <onfailure action="restart" delay="30 sec"/>')
    $lines.Add('  <onfailure action="restart" delay="60 sec"/>')
    $lines.Add('  <resetfailure>1 hour</resetfailure>')
    # Atlas hat 10 s Grace-Period (--shutdown-timeout); der Wrapper wartet laenger.
    $lines.Add('  <stoptimeout>30 sec</stoptimeout>')
    $lines.Add("  <logpath>$(& $esc $LogsDir)</logpath>")
    $lines.Add('  <log mode="roll-by-size">')
    $lines.Add('    <sizeThreshold>10240</sizeThreshold>')
    $lines.Add('    <keepFiles>8</keepFiles>')
    $lines.Add('  </log>')
    foreach ($k in $EnvVars.Keys) {
        $lines.Add("  <env name=`"$(& $esc $k)`" value=`"$(& $esc $EnvVars[$k])`"/>")
    }
    $lines.Add('</service>')
    return ($lines -join "`r`n") + "`r`n"
}

# ---------------------------------------------------------------------------
# Pfade
# ---------------------------------------------------------------------------

$InstallRoot = [IO.Path]::GetFullPath($InstallRoot.TrimEnd('\'))
$BinDir      = Join-Path $InstallRoot 'bin'
$ConfigDir   = Join-Path $InstallRoot 'config'
$LogsDir     = Join-Path $InstallRoot 'logs'
$SvcDir      = Join-Path $InstallRoot 'service'
$BackupDir   = Join-Path $InstallRoot 'backup'
$BinExe      = Join-Path $BinDir 'atlas.exe'
$WrapperExe  = Join-Path $SvcDir "$ServiceName-service.exe"
$WrapperXml  = Join-Path $SvcDir "$ServiceName-service.xml"
$ConfigKey   = Join-Path $ConfigDir 'vault.key'
$FirewallName = "Atlas ($ServiceName)"

if ([string]::IsNullOrWhiteSpace($DataDir)) {
    $DataDir = Join-Path $InstallRoot 'data'
    $legacy = Join-Path $InstallRoot 'atlas-data'
    if (-not (Test-Path -LiteralPath $DataDir) -and (Test-Path -LiteralPath $legacy)) {
        $DataDir = $legacy
        Write-Warning "Vorhandenes Datenverzeichnis $legacy wird weiterverwendet (Standard von atlas.exe ohne --data-dir)."
    }
}
$DataDir = [IO.Path]::GetFullPath($DataDir.TrimEnd('\'))
$DataKey = Join-Path $DataDir 'vault.key'

# ---------------------------------------------------------------------------
# Deinstallation
# ---------------------------------------------------------------------------

if ($Uninstall) {
    Write-Step "Deinstallation von Dienst '$ServiceName'"
    if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) {
        Stop-AtlasService
        if (Test-Path -LiteralPath $WrapperExe) {
            Invoke-Native -File $WrapperExe -Arguments @('uninstall') | Out-Null
        } else {
            Invoke-Native -File 'sc.exe' -Arguments @('delete', $ServiceName) | Out-Null
        }
        Write-Info 'Dienst entfernt.'
    } else {
        Write-Info 'Dienst ist nicht installiert.'
    }
    Get-NetFirewallRule -DisplayName $FirewallName -ErrorAction SilentlyContinue | Remove-NetFirewallRule
    if ($RemoveData) {
        Write-Warning "Folgende Verzeichnisse werden endgueltig geloescht: $BinDir, $ConfigDir, $DataDir, $LogsDir, $SvcDir"
        $answer = Read-Host 'Zur Bestaetigung JA eingeben'
        if ($answer -ne 'JA') { Write-Info 'Abgebrochen, Daten bleiben erhalten.'; return }
        Unlock-ForAdmins $DataKey
        Unlock-ForAdmins $ConfigKey
        foreach ($d in @($BinDir, $ConfigDir, $DataDir, $LogsDir, $SvcDir)) {
            if (Test-Path -LiteralPath $d) { Remove-Item -LiteralPath $d -Recurse -Force }
        }
        Write-Info "Geloescht. $BackupDir und sonstige Dateien unter $InstallRoot bleiben erhalten."
    } else {
        Write-Info "Daten unter $InstallRoot bleiben erhalten."
    }
    return
}

# ---------------------------------------------------------------------------
# 1. Voraussetzungen
# ---------------------------------------------------------------------------

Write-Step 'Voraussetzungen pruefen'

if (-not [Environment]::Is64BitOperatingSystem) { throw 'Atlas wird nur fuer Windows x64 (windows_amd64) ausgeliefert.' }

# WinSW: expliziter Pfad muss existieren; ohne Angabe wird eine bereits unter
# -InstallRoot abgelegte WinSW*.exe verwendet, bevor ein Download versucht wird.
if (-not [string]::IsNullOrWhiteSpace($WinSWPath)) {
    if (-not (Test-Path -LiteralPath $WinSWPath -PathType Leaf)) {
        $near = @()
        foreach ($d in @($InstallRoot, (Join-Path $env:USERPROFILE 'Downloads'))) {
            if (Test-Path -LiteralPath $d) {
                $near += @(Get-ChildItem -LiteralPath $d -Filter 'WinSW*.exe*' -File -Recurse -Depth 2 -ErrorAction SilentlyContinue | ForEach-Object { $_.FullName })
            }
        }
        $found = 'keine'
        if ($near.Count -gt 0) { $found = $near -join ', ' }
        throw "-WinSWPath '$WinSWPath' existiert nicht. Gefundene WinSW-Dateien unter $InstallRoot und im Download-Ordner: $found"
    }
    $WinSWPath = (Resolve-Path -LiteralPath $WinSWPath).Path
} elseif (-not (Test-Path -LiteralPath $WrapperExe)) {
    $local = @(Get-ChildItem -LiteralPath $InstallRoot -Filter 'WinSW*.exe' -File -ErrorAction SilentlyContinue)
    if ($local.Count -eq 1) {
        $WinSWPath = $local[0].FullName
        Write-Info "WinSW gefunden: $WinSWPath"
    } elseif ($local.Count -gt 1) {
        throw "Mehrere WinSW-Dateien unter ${InstallRoot}: $(($local | ForEach-Object { $_.Name }) -join ', '). Bitte mit -WinSWPath eine angeben."
    }
}

$listen = Split-ListenAddr $Addr
$UseTls = -not [string]::IsNullOrWhiteSpace($TlsCert)
if ($UseTls -xor (-not [string]::IsNullOrWhiteSpace($TlsKey))) {
    throw '-TlsCert und -TlsKey muessen gemeinsam angegeben werden (Atlas startet sonst nicht).'
}
if (-not $listen.Loopback -and -not $UseTls -and -not $AllowPlaintext) {
    throw "-Addr '$Addr' ist von aussen erreichbar, aber ohne TLS. Entweder -TlsCert/-TlsKey angeben, an 127.0.0.1 binden (Reverse-Proxy) oder bewusst -AllowPlaintext setzen."
}
$ProbeUrl = "http://$($listen.ProbeHost):$($listen.Port)/readyz"

$existing = Get-CimInstance -ClassName Win32_Service -Filter "Name='$ServiceName'" -ErrorAction SilentlyContinue
$UpdateMode = $null -ne $existing
if ($UpdateMode) {
    if ($existing.PathName -notlike "*$WrapperExe*") {
        throw "Es existiert bereits ein Dienst '$ServiceName' mit dem Programmpfad $($existing.PathName). Dieser wurde nicht von diesem Skript angelegt. Bitte pruefen und gegebenenfalls mit 'sc.exe delete $ServiceName' entfernen."
    }
    Write-Info "Dienst '$ServiceName' existiert bereits: Update-Modus."
} else {
    Write-Info 'Neuinstallation.'
    $running = Get-AtlasProcesses -All
    if ($running.Count -gt 0) {
        throw "Es laeuft bereits atlas.exe (PID $($running.Id -join ', ')). Pro Datenverzeichnis darf nur ein Prozess laufen; bitte zuerst beenden."
    }
}

if (-not $UseTls -and $listen.Loopback -eq $false) {
    Write-Warning 'Atlas wird ohne TLS von aussen erreichbar sein (-AllowPlaintext).'
}

# ---------------------------------------------------------------------------
# 2. Verzeichnisse
# ---------------------------------------------------------------------------

Write-Step "Verzeichnisse unter $InstallRoot anlegen"
foreach ($d in @($InstallRoot, $BinDir, $ConfigDir, $LogsDir, $SvcDir, $DataDir)) {
    if (-not (Test-Path -LiteralPath $d)) {
        New-Item -ItemType Directory -Path $d | Out-Null
        Write-Info "angelegt: $d"
    }
}

# ---------------------------------------------------------------------------
# 3. atlas.exe
# ---------------------------------------------------------------------------

Write-Step 'atlas.exe bereitstellen'

$sourceExe = $null
if (-not [string]::IsNullOrWhiteSpace($AtlasExe)) {
    if (-not (Test-Path -LiteralPath $AtlasExe -PathType Leaf)) { throw "-AtlasExe '$AtlasExe' existiert nicht." }
    $sourceExe = (Resolve-Path -LiteralPath $AtlasExe).Path
} else {
    $excluded = @($BinDir, $DataDir, $BackupDir, $SvcDir, $LogsDir, $ConfigDir) | ForEach-Object { $_ + '\' }
    $candidates = @(Get-ChildItem -LiteralPath $InstallRoot -Filter 'atlas.exe' -File -Recurse -Depth 4 -ErrorAction SilentlyContinue |
        Where-Object { $p = $_.FullName; -not ($excluded | Where-Object { $p.StartsWith($_, [StringComparison]::OrdinalIgnoreCase) }) } |
        Sort-Object LastWriteTime -Descending)
    foreach ($c in $candidates) { Write-Info "gefunden: $($c.FullName) ($($c.LastWriteTime))" }
    if ($candidates.Count -gt 0) { $sourceExe = $candidates[0].FullName }
}

if ($null -eq $sourceExe -and -not [string]::IsNullOrWhiteSpace($Version)) {
    $relDir = Join-Path $InstallRoot "releases\$Version"
    New-Item -ItemType Directory -Force -Path $relDir | Out-Null
    $base = "https://github.com/pblumer/atlas/releases/download/v$Version"
    $zipName = "atlas_${Version}_windows_amd64.zip"
    $zip = Join-Path $relDir $zipName
    $sums = Join-Path $relDir 'SHA256SUMS'
    Invoke-Download -Uri "$base/$zipName" -OutFile $zip -OfflineHint '-AtlasExe <Pfad zu atlas.exe>'
    Invoke-Download -Uri "$base/SHA256SUMS" -OutFile $sums -OfflineHint '-AtlasExe <Pfad zu atlas.exe>'
    $line = Select-String -LiteralPath $sums -Pattern ([regex]::Escape($zipName)) | Select-Object -First 1
    if ($null -eq $line) { throw "$zipName ist nicht in SHA256SUMS aufgefuehrt." }
    $expected = ($line.Line -split '\s+')[0].ToLowerInvariant()
    if ($expected -ne (Get-FileSha256 $zip)) { throw "Pruefsumme von $zipName stimmt nicht - Download nicht verwenden." }
    Write-Info 'Pruefsumme in Ordnung.'
    Expand-Archive -LiteralPath $zip -DestinationPath $relDir -Force
    $found = Get-ChildItem -LiteralPath $relDir -Filter 'atlas.exe' -File -Recurse | Select-Object -First 1
    if ($null -eq $found) { throw "atlas.exe nicht im Archiv $zipName gefunden." }
    $sourceExe = $found.FullName
}

$binExists = Test-Path -LiteralPath $BinExe
if ($null -eq $sourceExe -and -not $binExists) {
    throw "Keine atlas.exe unter $InstallRoot gefunden. -AtlasExe <Pfad> oder -Version <x.y.z> angeben."
}

$replaceBinary = $false
if ($null -ne $sourceExe) {
    if (-not $binExists) {
        $replaceBinary = $true
    } elseif ((Get-FileSha256 $sourceExe) -ne (Get-FileSha256 $BinExe)) {
        $replaceBinary = $true
    }
}
$oldVersion = $null
if ($binExists) { $oldVersion = Get-ExeVersion $BinExe }

if ($replaceBinary) {
    Unblock-File -LiteralPath $sourceExe
    $newVersion = Get-ExeVersion $sourceExe
    if ($null -eq $newVersion) { throw "$sourceExe laesst sich nicht ausfuehren (falsche Architektur oder beschaedigt?)." }
    Write-Info "Quelle:  $sourceExe"
    Write-Info "Version: $newVersion"
    if ($oldVersion) { Write-Info "bisher:  $oldVersion" }
} else {
    Write-Info "$BinExe ist aktuell ($oldVersion)."
}

# ---------------------------------------------------------------------------
# 4. WinSW
# ---------------------------------------------------------------------------

Write-Step 'Service-Wrapper WinSW bereitstellen'
if (-not (Test-Path -LiteralPath $WrapperExe)) {
    if (-not [string]::IsNullOrWhiteSpace($WinSWPath)) {
        Copy-Item -LiteralPath $WinSWPath -Destination $WrapperExe
    } else {
        Invoke-Download -Uri $WinSWUrl -OutFile $WrapperExe -OfflineHint '-WinSWPath <Pfad zu WinSW-x64.exe>'
    }
    if (-not [string]::IsNullOrWhiteSpace($WinSWSha256)) {
        $actual = Get-FileSha256 $WrapperExe
        if ($actual -ne $WinSWSha256.ToLowerInvariant()) {
            Remove-Item -LiteralPath $WrapperExe -Force
            throw "Pruefsumme von WinSW stimmt nicht (erwartet $WinSWSha256, erhalten $actual). Bei bewusst anderer WinSW-Version -WinSWSha256 anpassen."
        }
        Write-Info 'Pruefsumme in Ordnung.'
    }
    Unblock-File -LiteralPath $WrapperExe
} else {
    Write-Info "$WrapperExe ist vorhanden."
}

# ---------------------------------------------------------------------------
# 5. Testlauf im Vordergrund
# ---------------------------------------------------------------------------

# Interpreter vorab bestimmen, damit Testlauf und Dienst dieselben Flags haben.
$interpreterArgs = @()
foreach ($i in @(
        @{ Flag = 'powershell'; File = 'pwsh.exe';    Hint = 'PowerShell 7 (pwsh) installieren; Windows PowerShell 5.1 genuegt nicht.' },
        @{ Flag = 'python';     File = 'python3.exe'; Hint = 'Atlas sucht python3.exe; der Python-Installer legt meist nur python.exe an.' },
        @{ Flag = 'javascript'; File = 'node.exe';    Hint = 'Node.js fuer alle Benutzer installieren.' })) {
    $path = Find-OnMachinePath $i.File
    if ($null -ne $path) { continue }
    if ($KeepAllInterpreters) {
        Write-Warning "$($i.File) fehlt im Maschinen-PATH; $($i.Flag)-Script-Tasks bleiben haengen, bis er installiert ist. $($i.Hint)"
    } else {
        $interpreterArgs += "--$($i.Flag)=false"
        Write-Warning "$($i.File) fehlt im Maschinen-PATH; $($i.Flag)-Script-Tasks werden deaktiviert (--$($i.Flag)=false). $($i.Hint)"
    }
}

$testExe = $BinExe
if ($replaceBinary) { $testExe = $sourceExe }

if (-not $SkipSmokeTest) {
    Write-Step 'Testlauf im Vordergrund (Wegwerf-Datenverzeichnis)'
    $testDir = Join-Path $env:TEMP ("atlas-smoketest-" + [guid]::NewGuid().ToString('N'))
    $testPort = Get-FreeTcpPort
    $testOut = "$testDir.out.log"
    $testErr = "$testDir.err.log"
    $argList = @('serve', '--addr', "127.0.0.1:$testPort", '--data-dir', "`"$testDir`"") + $interpreterArgs
    $proc = Start-Process -FilePath $testExe -ArgumentList $argList -PassThru -WindowStyle Hidden `
        -RedirectStandardOutput $testOut -RedirectStandardError $testErr
    $ok = $false
    try {
        $deadline = (Get-Date).AddSeconds(60)
        while ((Get-Date) -lt $deadline -and -not $proc.HasExited) {
            Start-Sleep -Seconds 1
            try {
                $r = Invoke-WebRequest -Uri "http://127.0.0.1:$testPort/readyz" -UseBasicParsing -TimeoutSec 3
                if ($r.StatusCode -eq 200) { $ok = $true; break }
            } catch { }
        }
    } finally {
        if (-not $proc.HasExited) { Stop-Process -Id $proc.Id -Force; $proc.WaitForExit(15000) | Out-Null }
    }
    if (-not $ok) {
        Get-Content -LiteralPath $testErr -Tail 40 -ErrorAction SilentlyContinue | ForEach-Object { Write-Host "    $_" }
        throw 'Testlauf fehlgeschlagen: /readyz hat innerhalb von 60 s nicht geantwortet.'
    }
    Write-Info "Testlauf erfolgreich (Port $testPort)."
    Start-Sleep -Seconds 1
    foreach ($p in @($testDir, $testOut, $testErr)) {
        Remove-Item -LiteralPath $p -Recurse -Force -ErrorAction SilentlyContinue
    }
}

# ---------------------------------------------------------------------------
# Update-Modus: stoppen, sichern, Binary ersetzen
# ---------------------------------------------------------------------------

if ($UpdateMode) {
    Write-Step 'Dienst stoppen'
    Stop-AtlasService

    $hasData = @(Get-ChildItem -LiteralPath $DataDir -Force -ErrorAction SilentlyContinue).Count -gt 0
    if ($hasData -and -not $SkipBackup) {
        Write-Step 'Datenverzeichnis sichern'
        $target = Join-Path $BackupDir ("data-" + (Get-Date -Format 'yyyyMMdd-HHmmss'))
        # /B (Backup-Modus) liest auch vault.key, das nur dem Dienstkonto gehoert.
        Invoke-Native -File 'robocopy.exe' -Arguments @($DataDir, $target, '/E', '/B', '/COPY:DATS', '/DCOPY:DAT', '/R:1', '/W:1', '/NP', '/NFL', '/NDL', '/NJH', '/NJS') -OkCodes @(0, 1, 2, 3, 4, 5, 6, 7) | Out-Null
        if (Test-Path -LiteralPath $ConfigKey) {
            Invoke-Native -File 'robocopy.exe' -Arguments @($ConfigDir, (Join-Path $target '_config'), 'vault.key', '/B', '/COPY:DATS', '/R:1', '/W:1', '/NP', '/NFL', '/NDL', '/NJH', '/NJS') -OkCodes @(0, 1, 2, 3, 4, 5, 6, 7) | Out-Null
        }
        Write-Info "Sicherung: $target"
        Write-Info 'Alte Sicherungen werden nicht automatisch geloescht.'
    }
}

if ($replaceBinary) {
    Write-Step "atlas.exe nach $BinDir kopieren"
    Copy-Item -LiteralPath $sourceExe -Destination $BinExe -Force
    Unblock-File -LiteralPath $BinExe
    Write-Info "installiert: $(Get-ExeVersion $BinExe)"
}

# ---------------------------------------------------------------------------
# 6. TLS-Dateien
# ---------------------------------------------------------------------------

$tlsArgs = @()
if ($UseTls) {
    Write-Step "TLS-Dateien nach $ConfigDir kopieren"
    $crtDst = Join-Path $ConfigDir 'tls.crt'
    $keyDst = Join-Path $ConfigDir 'tls.key'
    foreach ($pair in @(@($TlsCert, $crtDst), @($TlsKey, $keyDst))) {
        if (-not (Test-Path -LiteralPath $pair[0] -PathType Leaf)) { throw "Datei '$($pair[0])' existiert nicht." }
        $src = (Resolve-Path -LiteralPath $pair[0]).Path
        if ($src -ne $pair[1]) { Copy-Item -LiteralPath $src -Destination $pair[1] -Force }
    }
    if (-not ((Get-Content -LiteralPath $crtDst -Raw) -match '-----BEGIN CERTIFICATE-----')) {
        throw "$crtDst ist keine PEM-Datei. Atlas liest PEM, nicht PFX und nicht den Windows-Zertifikatsspeicher."
    }
    if (-not ((Get-Content -LiteralPath $keyDst -Raw) -match '-----BEGIN [A-Z ]*PRIVATE KEY-----')) {
        throw "$keyDst ist kein PEM-Schluessel."
    }
    $tlsArgs = @('--tls-cert', $crtDst, '--tls-key', $keyDst)
    Write-Info 'Fuer eine Zertifikatserneuerung genuegt es, diese beiden Dateien zu ersetzen; Atlas laedt sie ohne Neustart nach.'
}

# ---------------------------------------------------------------------------
# 7. Vault-Schluessel und Bootstrap-Administrator
# ---------------------------------------------------------------------------

Write-Step 'Vault-Schluessel und Administrator vorbereiten'

$envVars = [ordered]@{}

if (Test-Path -LiteralPath $ConfigKey) {
    $VaultKeyMode = 'File'
    Write-Info "Bestehender Schluessel $ConfigKey wird verwendet (ATLAS_VAULT_KEY_FILE)."
} elseif ($VaultKeyMode -eq 'File') {
    if (Test-Path -LiteralPath $DataKey) {
        throw "$DataKey existiert bereits; die Daten sind mit diesem Schluessel verschluesselt. -VaultKeyMode File ist nur bei einer Neuinstallation moeglich."
    }
    $hex = ((New-RandomBytes 32) | ForEach-Object { $_.ToString('x2') }) -join ''
    Write-Utf8NoBom -Path $ConfigKey -Content $hex
    Write-Info "Neuer Schluessel erzeugt: $ConfigKey"
} else {
    Write-Info "Atlas erzeugt bzw. verwendet $DataKey."
}
if ($VaultKeyMode -eq 'File') { $envVars['ATLAS_VAULT_KEY_FILE'] = $ConfigKey }

$usersDir = Join-Path $DataDir 'users'
$hasUsers = (Test-Path -LiteralPath $usersDir) -and (@(Get-ChildItem -LiteralPath $usersDir -Recurse -File -ErrorAction SilentlyContinue).Count -gt 0)
$seedAdmin = -not $hasUsers
$adminPlain = $null
$generatedPassword = $false
if ($seedAdmin) {
    if ($null -ne $AdminPassword) {
        $adminPlain = ConvertFrom-SecureStringPlain $AdminPassword
    } else {
        $adminPlain = New-RandomPassword 24
        $generatedPassword = $true
    }
    Write-Info "Bootstrap-Administrator '$AdminUsername' wird beim ersten Start angelegt."
} else {
    Write-Info 'Benutzerspeicher ist nicht leer; es wird kein Administrator angelegt.'
}

# ---------------------------------------------------------------------------
# 8. Konfiguration und Dienst
# ---------------------------------------------------------------------------

$atlasArgs = @('serve', '--addr', $Addr, '--data-dir', $DataDir, '--auth', '--log-format', 'json') + $tlsArgs
if (-not [string]::IsNullOrWhiteSpace($ExternalUrl)) { $atlasArgs += @('--external-url', $ExternalUrl) }
$atlasArgs += $interpreterArgs
$atlasArgs += $ExtraArgs

Write-Step 'Zugriffsrechte vorbereiten'
# Das Basisverzeichnis wird gegen Schreibzugriffe normaler Benutzer gesperrt:
# Wer atlas.exe oder die WinSW-Konfiguration aendern kann, kann Code als
# Dienstkonto ausfuehren. Auf Datenlaufwerken erlaubt die Standard-ACL von D:\
# "Authentifizierten Benutzern" oft das Aendern.
Invoke-Icacls -Path $InstallRoot -Arguments @('/inheritance:r', '/grant:r', "${SidAdmins}:(OI)(CI)F", "${SidSystem}:(OI)(CI)F")
Write-Info "$InstallRoot ist nur noch fuer Administratoren und SYSTEM zugaenglich."

$adminEnv = [ordered]@{}
foreach ($k in $envVars.Keys) { $adminEnv[$k] = $envVars[$k] }
if ($seedAdmin) {
    $adminEnv['ATLAS_ADMIN_USERNAME'] = $AdminUsername
    $adminEnv['ATLAS_ADMIN_PASSWORD'] = $adminPlain
}

Write-Step 'WinSW-Konfiguration schreiben'
Write-Utf8NoBom -Path $WrapperXml -Content (New-ServiceXml -AtlasArgs $atlasArgs -EnvVars $adminEnv)
Write-Info $WrapperXml
Write-Info ("atlas.exe " + ($atlasArgs -join ' '))

if (-not $UpdateMode) {
    Write-Step "Dienst '$ServiceName' installieren"
    Invoke-Native -File $WrapperExe -Arguments @('install') | Out-Null
    # Virtuelles Dienstkonto: kein Passwort, eigene SID, minimale Rechte.
    Invoke-Native -File 'sc.exe' -Arguments @('config', $ServiceName, 'obj=', "NT SERVICE\$ServiceName") | Out-Null
    Write-Info "Dienst laeuft als NT SERVICE\$ServiceName."
}

# ---------------------------------------------------------------------------
# 9. Zugriffsrechte fuer das Dienstkonto
# ---------------------------------------------------------------------------

Write-Step 'Zugriffsrechte fuer das Dienstkonto setzen'
$ServiceSid = Get-ServiceSid
Invoke-Icacls -Path $InstallRoot -Arguments @('/grant', "${ServiceSid}:(OI)(CI)RX")
Invoke-Icacls -Path $DataDir     -Arguments @('/grant', "${ServiceSid}:(OI)(CI)M")
Invoke-Icacls -Path $LogsDir     -Arguments @('/grant', "${ServiceSid}:(OI)(CI)M")
Protect-VaultKey -Path $DataKey   -Sid $ServiceSid
Protect-VaultKey -Path $ConfigKey -Sid $ServiceSid
if ($UseTls) {
    Invoke-Icacls -Path (Join-Path $ConfigDir 'tls.key') -Arguments @('/inheritance:r', '/grant:r', "${ServiceSid}:R", "${SidAdmins}:F", "${SidSystem}:F")
}
Write-Info "Lesen/Ausfuehren: $InstallRoot; Aendern: $DataDir, $LogsDir"

if ($DefenderExclusion) {
    try {
        Add-MpPreference -ExclusionPath $DataDir
        Write-Info "Defender-Ausnahme: $DataDir"
    } catch {
        Write-Warning "Defender-Ausnahme konnte nicht gesetzt werden: $($_.Exception.Message)"
    }
}

# ---------------------------------------------------------------------------
# 10. Start
# ---------------------------------------------------------------------------

Write-Step "Dienst '$ServiceName' starten"
$offsets = Get-LogOffsets
Start-Service -Name $ServiceName
if (-not (Wait-AtlasReady -Offsets $offsets)) {
    Show-LogTail
    if ($seedAdmin) { Write-Warning "$WrapperXml enthaelt noch das Bootstrap-Passwort. Nach der Fehlerbehebung das Skript erneut ausfuehren; es entfernt das Passwort danach." }
    throw "Atlas ist innerhalb von $ReadyTimeoutSec s nicht bereit geworden. Log: $LogsDir"
}
Write-Info 'Atlas ist bereit.'

# Ein von Atlas erzeugter vault.key existiert erst jetzt.
Protect-VaultKey -Path $DataKey -Sid $ServiceSid

# ---------------------------------------------------------------------------
# 11. Bootstrap-Passwort aus der Konfiguration entfernen
# ---------------------------------------------------------------------------

if ($seedAdmin) {
    Write-Step 'Bootstrap-Passwort aus der Konfiguration entfernen'
    Write-Utf8NoBom -Path $WrapperXml -Content (New-ServiceXml -AtlasArgs $atlasArgs -EnvVars $envVars)
    $offsets = Get-LogOffsets
    Restart-Service -Name $ServiceName
    if (-not (Wait-AtlasReady -Offsets $offsets)) {
        Show-LogTail
        throw "Atlas ist nach dem Neustart nicht bereit geworden. Log: $LogsDir"
    }
    Write-Info 'Konfiguration enthaelt kein Passwort mehr; Dienst neu gestartet.'
}

# ---------------------------------------------------------------------------
# 12. Firewall
# ---------------------------------------------------------------------------

if ($OpenFirewall) {
    Write-Step 'Firewall-Regel'
    if ($listen.Loopback) {
        Write-Warning "-Addr '$Addr' ist nur lokal erreichbar; eine Firewall-Regel ist wirkungslos und wird nicht angelegt."
    } else {
        Get-NetFirewallRule -DisplayName $FirewallName -ErrorAction SilentlyContinue | Remove-NetFirewallRule
        New-NetFirewallRule -DisplayName $FirewallName -Direction Inbound -Protocol TCP -LocalPort $listen.Port `
            -Program $BinExe -Action Allow -Profile $FirewallProfile | Out-Null
        Write-Info "Eingehend TCP $($listen.Port) fuer $BinExe (Profile $($FirewallProfile -join ', '))."
    }
}

# ---------------------------------------------------------------------------
# Zusammenfassung
# ---------------------------------------------------------------------------

$scheme = 'http'
if ($UseTls) { $scheme = 'https' }
$url = "${scheme}://$($listen.ProbeHost):$($listen.Port)/"
if (-not [string]::IsNullOrWhiteSpace($ExternalUrl)) { $url = $ExternalUrl }

Write-Host ''
Write-Host '==========================================================================' -ForegroundColor Green
Write-Host " Atlas laeuft als Dienst '$ServiceName' (NT SERVICE\$ServiceName)" -ForegroundColor Green
Write-Host '==========================================================================' -ForegroundColor Green
Write-Host "  Version:       $(Get-ExeVersion $BinExe)"
Write-Host "  URL:           $url"
Write-Host "  Daten:         $DataDir"
Write-Host "  Logs:          $LogsDir"
Write-Host "  Konfiguration: $WrapperXml"
if ($seedAdmin) {
    Write-Host "  Administrator: $AdminUsername"
    if ($generatedPassword) {
        Write-Host "  Passwort:      $adminPlain" -ForegroundColor Yellow
        Write-Host '                 (wird nur jetzt angezeigt - sofort nach dem ersten Login aendern)' -ForegroundColor Yellow
    }
}
Write-Host ''
Write-Host '  Noch zu erledigen:'
if ($VaultKeyMode -eq 'File') {
    Write-Host "  - $ConfigKey getrennt von den Daten sichern. Ohne ihn sind die Secrets verloren."
} else {
    Write-Host "  - $DataKey getrennt von den Daten sichern. Ohne ihn sind die Secrets verloren."
}
Write-Host '  - Backup: Dienst stoppen, Datenverzeichnis vollstaendig sichern, Dienst starten.'
Write-Host "    (Dieses Skript erneut ausfuehren = Update inkl. Sicherung nach $BackupDir.)"
if (-not $UseTls -and $listen.Loopback) {
    Write-Host '  - Atlas ist nur lokal erreichbar. Fuer Zugriff von aussen: -TlsCert/-TlsKey oder'
    Write-Host '    IIS/ARR als Reverse-Proxy mit -ExternalUrl.'
}
Write-Host ''

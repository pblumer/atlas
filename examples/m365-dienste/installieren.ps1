<#
.SYNOPSIS
  Installiert das Paket «Beispiel: Microsoft-365-Dienste» in eine Atlas-Instanz.

.DESCRIPTION
  Dieselben Schritte wie installieren.sh, für Windows PowerShell 5.1 und PowerShell 7:
    1. die fünf Entscheidungsmodelle unter ihren Handles hochladen;
    2. die Applikation als Quellbaum importieren (Prozesse, Formulare, Entscheidungsreferenzen);
    3. die Applikation veröffentlichen;
    4. die Platzhalter im Katalogdokument mit den Gruppen der Instanz ausfüllen und das
       Dokument importieren und veröffentlichen.

  Das Token braucht die Rolle modeler. Den Katalog importiert nur, wer productmanager ist, und
  ein API-Token trägt diese Rolle nie: Wird Schritt 4 abgelehnt, liegt das ausgefüllte Dokument
  als katalog-zum-import.json bereit und wird in der Console unter Catalogue -> Import
  hochgeladen (mit «veröffentlichen»). Braucht tar.exe (ab Windows 10 1803 vorhanden).

  Die Applikation gehört danach dem Token, das sie angelegt hat: Ein zweiter Lauf mit demselben
  Token aktualisiert sie, ein anderes Token sieht sie nicht.

.EXAMPLE
  $env:ATLAS_TOKEN = 'atlasat_…'
  .\installieren.ps1 -Server https://atlas.intern.example.ch -Antworten antworten.json
#>
param(
  [Parameter(Mandatory = $true)][string]$Server,
  [string]$Antworten = 'antworten.json',
  [string]$Token = $env:ATLAS_TOKEN
)
$ErrorActionPreference = 'Stop'
if (-not $Token) { throw 'ATLAS_TOKEN ist nicht gesetzt (oder -Token angeben).' }
$Server = $Server.TrimEnd('/')
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$auth = @{ Authorization = "Bearer $Token" }
$utf8 = New-Object System.Text.UTF8Encoding($false)

Write-Host '1. Entscheidungsmodelle'
foreach ($m in 'm365-lizenzprofil', 'm365-namensregel', 'm365-postfachzugriff', 'm365-telefonie', 'm365-verteiler-zuordnung') {
  $bytes = [IO.File]::ReadAllBytes((Join-Path $here "$m.dmn"))
  Invoke-RestMethod -Method Post -Uri "$Server/api/v1/dmn-models?handle=$m" -Headers $auth -ContentType 'application/xml' -Body $bytes | Out-Null
  Write-Host "   $m"
}

Write-Host '2. Applikation importieren'
$manifest = [IO.File]::ReadAllText((Join-Path $here 'atlas.json'), $utf8) | ConvertFrom-Json
$files = @('atlas.json') + @($manifest.processes | ForEach-Object { $_.path }) + @($manifest.forms | ForEach-Object { $_.path })
$work = Join-Path ([IO.Path]::GetTempPath()) ('m365-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null
try {
  $archive = Join-Path $work 'source.tar.gz'
  & tar -czf $archive -C $here @files
  if ($LASTEXITCODE -ne 0) { throw "tar ist mit $LASTEXITCODE gescheitert" }
  $source = Invoke-RestMethod -Method Post -Uri "$Server/api/v1/applications/source" -Headers $auth -ContentType 'application/gzip' -InFile $archive
  Write-Host "   Applikation $($source.applicationId)"

  Write-Host '3. Applikation veröffentlichen'
  $publish = Invoke-RestMethod -Method Post -Uri "$Server/api/v1/applications/$($source.applicationId)/publish" -Headers $auth -ContentType 'application/json' -Body '{"note":"installieren.ps1"}'
  Write-Host "   Release $($publish.release.version)"
  foreach ($w in @($publish.warnings)) { if ($w) { Write-Host "   Hinweis: $w" } }

  Write-Host '4. Katalog'
  $fragen = [IO.File]::ReadAllText((Join-Path $here 'fragen.json'), $utf8) | ConvertFrom-Json
  $antwort = [IO.File]::ReadAllText((Resolve-Path $Antworten), $utf8) | ConvertFrom-Json
  $principals = @(Invoke-RestMethod -Method Get -Uri "$Server/api/v1/principals" -Headers $auth)
  $fehler = New-Object System.Collections.Generic.List[string]
  $dokument = [IO.File]::ReadAllText((Join-Path $here 'katalog.json'), $utf8)
  $gefuellt = [regex]::Replace($dokument, '\{\{([a-z0-9-]+)\}\}', {
      param($match)
      $name = $match.Groups[1].Value
      $frage = $fragen.$name
      if (-not $frage) { $fehler.Add("{{$name}} wird in fragen.json nicht gefragt"); return $match.Value }
      $wert = $antwort.$name
      if (-not $wert) { $fehler.Add("$name ist nicht beantwortet: $($frage.label.de)"); return $match.Value }
      $treffer = @($principals | Where-Object { $_.type -eq $frage.kind -and ($_.id -eq $wert -or $_.name -eq $wert) })
      if ($treffer.Count -ne 1) {
        $grund = if ($treffer.Count -eq 0) { 'unbekannt' } else { 'mehrdeutig' }
        $fehler.Add("${name}: «$wert» ist $grund ($($frage.kind))"); return $match.Value
      }
      return $treffer[0].id
    })
  if ($fehler.Count -gt 0) { throw ("Katalog nicht ausgefüllt:`n  " + ($fehler -join "`n  ")) }
  $ziel = Join-Path $here 'katalog-zum-import.json'
  [IO.File]::WriteAllText($ziel, $gefuellt, $utf8)
  try {
    Invoke-RestMethod -Method Post -Uri "$Server/api/v1/catalogs/import" -Headers $auth -ContentType 'application/json; charset=utf-8' -Body $utf8.GetBytes($gefuellt) | Out-Null
    Write-Host '   Katalog importiert und veröffentlicht'
    Remove-Item $ziel
  } catch {
    Write-Host "   Der Server hat den Katalogimport abgelehnt: $($_.Exception.Message)"
    Write-Host "   Das ausgefüllte Dokument liegt unter $ziel."
    Write-Host '   In der Console: Catalogue -> Import -> Datei wählen, «veröffentlichen» ankreuzen.'
  }
} finally {
  Remove-Item -Recurse -Force $work
}

Write-Host ''
Write-Host 'Danach: Applikationskonstanten MAIL_DOMAIN, USAGE_LOCATION, PARTNER_UPN_DOMAIN und'
Write-Host 'ATLAS_API_BASE setzen, die Worker ucc_entra, ucc_exchange und ucc_teams einrichten'
Write-Host 'und die öffentlichen Links erzeugen (README.md).'

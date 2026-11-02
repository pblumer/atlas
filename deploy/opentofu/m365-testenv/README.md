# Testumgebung für die Microsoft-Worker (OpenTofu)

Dieses OpenTofu-Projekt baut in einem **Microsoft-365-Testtenant** alles auf, was die
Atlas-Worker für Microsoft brauchen, schreibt die fertige Worker-Konfiguration in eine
Datei und entfernt mit `tofu destroy` alles wieder. Es ist für **temporäre Tests**
gedacht, nicht für den Betrieb.

| Worker Type | Ziel | Was das Projekt bereitstellt |
|---|---|---|
| `entra` ([ADR-0172](../../../docs/adr/0172-entra-id-connector.md)) | Microsoft Graph: Benutzer, Gruppen, Teams | App-Registrierung mit `User.ReadWrite.All`, `Group.ReadWrite.All`, `TeamMember.ReadWrite.All`, `Channel.Create`, `TeamSettings.ReadWrite.All`; Zertifikat (oder Secret) |
| `exchange` ([ADR-0462](../../../docs/adr/0462-exchange-worker.md)) | Exchange Online | App-Registrierung mit `Exchange.ManageAsApp` und der Rolle *Exchange Administrator*; Zertifikat |
| `sharepoint` ([ADR-0141](../../../docs/adr/0141-sharepoint-connector.md)) | SharePoint Online über Graph | App-Registrierung mit `Sites.ReadWrite.All`; Zertifikat (oder Secret) |
| `teamsphone` ([ADR-0463](../../../docs/adr/0463-teams-phone-worker.md)) | Teams Phone (PowerShell) | App-Registrierung mit `User.ReadWrite.All` und der Rolle *Teams Administrator*; Zertifikat |
| `ad` ([ADR-0166](../../../docs/adr/0166-active-directory-connector.md)) | Active Directory | **optional** ein Domänencontroller in Azure mit LDAPS, Dienstkonto und Test-OU; sonst Mock-Modus (`ATLAS_AD_MOCK=1`) |

Dazu kommen Testobjekte: drei Testpersonen und eine vorgesetzte Person (als Manager
eingetragen, für `get-user-manager`), eine Sicherheitsgruppe mit allen Testpersonen, eine
leere Gruppe zum freien Befüllen und eine **Microsoft-365-Gruppe**. Zu dieser legt Entra
automatisch eine SharePoint-Teamwebsite und ein Gruppenpostfach an; sie ist das Ziel der
SharePoint-Tests und wird durch die Operation `create-team` des Entra-Workers zum
Test-Team.

Jede App-Registrierung ist eine eigene, wie es [`docs/entra-permissions.md`](../../../docs/entra-permissions.md)
verlangt. Alle Objekte tragen den Präfix `atlas-test-<zufall>` (Variable `name_prefix`).

> **Annahme zu «Graph»:** Im Repository gibt es keinen eigenen Graph-Worker. Microsoft
> Graph wird über die Worker `entra` und `sharepoint` (und für Teams über `entra`)
> angesprochen; deren App-Registrierungen decken «Graph» hier ab.

## Voraussetzungen

1. **Ein Microsoft-365-Testtenant**, nie ein produktiver. Geeignet ist z. B. ein Tenant
   aus dem Microsoft 365 Developer Program oder ein Testabonnement. OpenTofu kann einen
   Tenant nicht anlegen.
2. **Eine Anmeldung mit genügend Rechten** im Testtenant, am einfachsten *Globaler
   Administrator*. Mindestens nötig: Anwendungen anlegen und Administratorzustimmung für
   Anwendungsberechtigungen erteilen (*Privileged Role Administrator* plus *Cloud
   Application Administrator*), Verzeichnisrollen zuweisen (*Privileged Role
   Administrator*), Benutzer und Gruppen anlegen (*User Administrator*,
   *Groups Administrator*).
3. **Werkzeuge:** OpenTofu ≥ 1.8, Azure CLI (`az`) für die Anmeldung und für die
   optionalen Skripte; für `scripts/wait-ad.sh` zusätzlich `openssl`.
4. **Lizenzen (optional, aber für Exchange und Teams nötig):** Testpersonen erhalten nur
   mit einer Lizenz ein Postfach und Teams. Freie SKUs zeigt
   `GET https://graph.microsoft.com/v1.0/subscribedSkus`; ihre `skuId` gehört in
   `license_sku_ids`.
5. **Nur für den Domänencontroller:** ein Azure-Abonnement (darf in einem anderen Tenant
   liegen), darin das Recht, eine Ressourcengruppe mit VM und Key Vault anzulegen.
6. **Für den Teams-Phone-Worker:** PowerShell 7 mit dem Modul `MicrosoftTeams` auf dem
   Worker-Host (ADR-0463). Ressourcenkonten brauchen die kostenlose Lizenz
   *Microsoft Teams Phone Resource Account*, die im Admin Center bezogen werden muss.

## Ablauf

### 1. Anmelden und konfigurieren

```bash
az login --tenant <tenant-id> --allow-no-subscriptions
```

```bash
cp terraform.tfvars.example terraform.tfvars
```

In `terraform.tfvars` mindestens `tenant_id` und `expected_initial_domain` setzen. Der
Plan bricht ab, wenn der angemeldete Tenant nicht genau diese ID **und** diese
Initial-Domain hat; ein versehentlicher Lauf gegen einen anderen Tenant scheitert so
zweimal.

### 2. Aufbauen

```bash
tofu init
```

```bash
tofu apply
```

Ergebnis im Verzeichnis `out/` (Modus 0600, durch `.gitignore` ausgeschlossen):

| Datei | Inhalt |
|---|---|
| `out/worker.env` | alle `ATLAS_*`-Variablen für `atlas worker`, Bash-Format |
| `out/worker.env.ps1` | dieselben Variablen für PowerShell |
| `out/certs/<worker>.pem` | Zertifikat und privater Schlüssel je App, z. B. für ein Console-Bundle oder `Connect-MicrosoftTeams` von Hand |
| `out/ad-ldaps.pem`, `out/trust/` | nur mit Domänencontroller: dessen LDAPS-Zertifikat |

### 3. Warten

Microsoft verteilt neue Berechtigungen nicht sofort. Erfahrungsgemäss (nicht garantiert):

- App-Berechtigungen und Zertifikate: einige Minuten.
- Exchange-Rolle für die App: bis zu etwa 30 Minuten, bis `InvokeCommand` sie anerkennt.
- SharePoint-Website der Microsoft-365-Gruppe: einige Minuten
  (`tofu output groups` zeigt URL und Graph-Abfrage).
- Postfächer nach der Lizenzzuweisung: bis zu einer Stunde.
- Domänencontroller: 10–20 Minuten nach `apply`; `scripts/wait-ad.sh` wartet darauf.

### 4. Worker starten

```bash
set -a; . out/worker.env; set +a
```

```bash
export ATLAS_TOKEN=atlasat_...
```

```bash
atlas worker --server https://atlas.example.ch --connector entra,exchange,sharepoint,teamsphone,ad
```

Der Server muss diese Kinds an Worker auslagern (siehe `--connector` in `atlas worker -h`).
Wer die Worker statt über Umgebungsvariablen in der Console einrichtet, übernimmt dieselben
Werte in die Bundles: Die JSON-Werte von `ATLAS_EXCHANGE_*_CREDENTIALS` und
`ATLAS_SHAREPOINT_*_CREDENTIALS` sind bereits Bundles.

Die Worker-Namen entsprechen den Vorgaben von [`examples/m365-dienste`](../../../examples/m365-dienste/)
(`ucc_entra`, `ucc_exchange`, `ucc_teams`) und lassen sich mit `worker_names` ändern.

### 5. Abbauen

```bash
tofu destroy
```

Danach liegen gelöschte Benutzer, Gruppen und App-Registrierungen noch 30 Tage im
Papierkorb von Entra. Endgültig entfernen (erst auflisten, dann löschen):

```bash
./scripts/purge-deleted.sh atlas-test
```

```bash
./scripts/purge-deleted.sh atlas-test --yes
```

Die SharePoint-Website der gelöschten Gruppe landet im Papierkorb der gelöschten
Websites (SharePoint Admin Center) und wird von Microsoft nach 93 Tagen entfernt; das
Skript berührt sie nicht. Zum Schluss `out/` löschen.

## Active Directory (optional)

Mit `enable_ad_dc = true` entsteht in Azure:

- eine Ressourcengruppe `atlas-test-<zufall>-ad` mit VNet, NSG, öffentlicher IP und
  einer Windows-Server-2022-VM (`Standard_B2ms`),
- ein Key Vault, der ein selbstsigniertes LDAPS-Zertifikat für den öffentlichen DNS-Namen
  `<label>.<region>.cloudapp.azure.com` ausstellt und bei der Bereitstellung in
  `LocalMachine\My` der VM installiert,
- per Custom Script Extension (`scripts/ad-setup.ps1`): AD DS, eine neue Gesamtstruktur
  (`atlastest.internal`), nach dem Neustart `OU=AtlasService` mit dem Bind-Konto
  `svc-atlas-<zufall>` und `OU=AtlasTest` mit Testpersonen und einer Testgruppe.

Das Bind-Konto hat **Vollzugriff nur auf `OU=AtlasTest`** und zusätzlich *Replicating
Directory Changes* auf der Domäne, damit die Operation `sync` (DirSync) testbar ist.

Die NSG öffnet nur Port 636 und nur für `ad_allowed_cidrs` (die öffentliche Adresse des
Worker-Hosts); RDP ist mit `ad_allow_rdp = true` zuschaltbar. `0.0.0.0/0` wird abgelehnt.
Eine tägliche automatische Abschaltung (`ad_auto_shutdown_time`, Vorgabe 19:00 UTC)
begrenzt die Kosten einer vergessenen Umgebung; eine abgeschaltete VM wird im Azure-Portal
oder mit `az vm start` wieder gestartet.

**Vertrauen in das LDAPS-Zertifikat:** Der AD-Worker prüft das Zertifikat gegen den
Vertrauensspeicher des Hosts und kennt keine Option, die Prüfung abzuschalten
(`connector/ad/client.go`). Darum:

- Linux: `out/worker.env` setzt `SSL_CERT_DIR=/etc/ssl/certs:<out>/trust`; Go liest dieses
  Verzeichnis zusätzlich zur Standard-Zertifikatsdatei.
- Windows: `Import-Certificate -FilePath out\ad-ldaps.pem -CertStoreLocation Cert:\LocalMachine\Root`.

Ohne Domänencontroller schreibt das Projekt `ATLAS_AD_MOCK=1` in die Datei; der Worker
bedient AD-Aufgaben dann aus einem Verzeichnis im Speicher (ADR-0181).

**Kosten:** VM, Datenträger, öffentliche IP und Key Vault werden in Rechnung gestellt,
solange sie existieren. Die Grössenordnung hängt von Region und Laufzeit ab; die
aktuellen Preise zeigt der Azure-Preisrechner.

## SharePoint mit `Sites.Selected`

Vorgabe ist `Sites.ReadWrite.All`, weil der Testtenant ohnehin temporär ist. Wer die
engere Variante testen will, setzt `sharepoint_graph_permissions = ["Sites.Selected"]`
und erteilt danach der App das Schreibrecht auf die Test-Website, z. B. im Graph Explorer
als Administrator:

```http
POST https://graph.microsoft.com/v1.0/sites/{site-id}/permissions
Content-Type: application/json

{ "roles": ["write"],
  "grantedToIdentities": [{ "application": { "id": "<client id der SharePoint-App>", "displayName": "atlas-test" } }] }
```

Die `site-id` liefert `GET /v1.0/groups/{gruppen-id}/sites/root` (siehe `tofu output groups`).
Das Projekt erteilt diese Berechtigung nicht selbst, weil der dafür nötige delegierte
Bereich `Sites.FullControl.All` im Token der Azure CLI nicht sicher enthalten ist.

## Sicherheit

- **Der State enthält alle erzeugten Geheimnisse** (private Schlüssel, Secrets,
  Passwörter). Er liegt standardmässig lokal in `terraform.tfstate` und gehört nicht in
  Git (`.gitignore`). Empfohlen ist die eingebaute State-Verschlüsselung von OpenTofu,
  z. B. über die Umgebungsvariable `TF_ENCRYPTION` mit einem `pbkdf2`-Schlüssel; die
  genaue Syntax steht in der OpenTofu-Dokumentation unter *State and Plan Encryption*.
- **Kurze Laufzeit:** Zertifikate und Secrets laufen nach `credential_validity_days`
  (Vorgabe 14 Tage) ab. Eine vergessene Umgebung verliert so ihren Zugang von selbst.
- **Anwendungsberechtigungen gelten tenantweit.** Die Grenze zu Fremdobjekten ist hier
  allein der Testtenant und die Präfix-Konvention; deshalb nie gegen einen produktiven
  Tenant anwenden.
- Die Passwörter für den Domänencontroller gelangen über die geschützten Einstellungen
  der Custom Script Extension auf die VM und stehen dort in deren Befehlszeile und, bis
  Phase 2 abgeschlossen ist, in `C:\AtlasSetup\phase2.json` (nur SYSTEM und
  Administratoren). Für eine Testumgebung ist das vertretbar, für Produktion nicht.

## Tests

`tests/environment.tftest.hcl` prüft die Logik des Projekts ohne Tenant (alle Provider
sind gemockt): die beiden Tenant-Sperren, welche Apps entstehen, die erzeugte
`worker.env` (Variablennamen nach `connector/envname.Key`, ein Wert pro Zeile,
Dateirechte 0600) und den AD-Schalter.

```bash
tofu init -backend=false
```

```bash
tofu test
```

## Bekannte Grenzen

- **Nicht gegen einen echten Tenant gelaufen.** Validierung und Tests liefen offline gegen
  die Schemas von `azuread` 3.10 und `azurerm` 4.81 (mit Terraform 1.16, da OpenTofu in
  der Erstellungsumgebung nicht verfügbar war). Unverifiziert sind insbesondere: das
  Verhalten von `scripts/ad-setup.ps1` auf der VM, die Wartezeiten oben und die
  Lizenzzuweisung über `az rest`.
- **Exchange Server (on-premises)** ist nicht abgedeckt; der Exchange-Worker wird nur gegen
  Exchange Online eingerichtet. Ein Exchange Server bräuchte eine eigene, deutlich
  grössere Umgebung.
- **Teams Phone:** Rufnummern, Calling Plans und Direct Routing richtet das Projekt nicht
  ein; Operationen, die eine Nummer brauchen, sind ohne diese nur teilweise testbar.
  Welche Graph-Berechtigungen `Connect-MicrosoftTeams` über `User.ReadWrite.All` hinaus
  für einzelne Cmdlets verlangt, ist nicht geprüft; `teamsphone_graph_permissions` lässt
  sie ergänzen.
- **Ein Team** wird bewusst nicht durch OpenTofu angelegt, sondern durch den Entra-Worker
  (`create-team`) – das ist selbst ein Test.
- **Gruppenmitgliedschaften** ignoriert OpenTofu nach dem ersten Anlegen
  (`ignore_changes`), damit die Worker sie ändern dürfen, ohne dass der nächste Plan sie
  zurücksetzt.
- **Lock-Datei:** `.terraform.lock.hcl` ist nicht eingecheckt; `tofu init` legt sie beim
  ersten Lauf an. Für reproduzierbare Läufe sollte sie danach versioniert werden.

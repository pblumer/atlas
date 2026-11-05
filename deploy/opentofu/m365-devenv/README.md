# M365-Entwicklungsumgebung für atlas (OpenTofu)

Dieses OpenTofu-Projekt baut mit **einem** `tofu apply` eine schlüsselfertige
Entwicklungsumgebung für die Microsoft-Worker von atlas auf und entfernt sie mit
`tofu destroy` wieder:

- im **Microsoft-365-Testtenant**: Testpersonen, Gruppen, eine Microsoft-365-Gruppe
  (SharePoint-Website, Teams-Ziel) und je Worker eine eigene App-Registrierung mit
  minimalen Berechtigungen und kurzlebigem Zertifikat oder Secret;
- in **Azure**: einen **atlas-Server** (Ubuntu 24.04), dessen Worker beim ersten Start
  bereits in der Console eingerichtet sind und von atlas selbst betrieben werden,
  Anmeldung der Testpersonen über Entra ID (OpenID Connect);
- optional einen **Active-Directory-Domänencontroller** im selben Netz.

Das Projekt ist für **Entwicklung und temporäre Tests** gedacht, nicht für den Betrieb.

## Inhalt

| Worker Type | Worker in der Console | Ziel | Berechtigungen der App |
|---|---|---|---|
| `entra` ([ADR-0172](../../../docs/adr/0172-entra-id-connector.md)) | `ucc_entra` | Microsoft Graph: Benutzer, Gruppen, Teams | `User.ReadWrite.All`, `Group.ReadWrite.All`, `TeamMember.ReadWrite.All`, `Channel.Create`, `TeamSettings.ReadWrite.All`, `User.Invite.All`, `LicenseAssignment.Read.All` (Lizenzbestand, `list-subscribed-skus`), `AuditLog.Read.All` (letzte Anmeldung, `signInActivity`; nur mit Entra ID P1/P2 im Tenant); Zertifikat |
| `exchange` ([ADR-0462](../../../docs/adr/0462-exchange-worker.md)) | `ucc_exchange` | Exchange Online | `Exchange.ManageAsApp` + Rolle *Exchange Administrator*; Zertifikat |
| `sharepoint` ([ADR-0141](../../../docs/adr/0141-sharepoint-connector.md)) | `ucc_sharepoint` | SharePoint Online | `Sites.ReadWrite.All`; Zertifikat |
| `teamsphone` ([ADR-0463](../../../docs/adr/0463-teams-phone-worker.md)) | `ucc_teams` | Teams Phone (PowerShell) | `User.ReadWrite.All` + Rolle *Teams Administrator*; Zertifikat |
| `mail` ([ADR-0079](../../../docs/adr/0079-outbound-mail-connector.md)) | `mail` | Versand über Microsoft Graph | `Mail.Send`; Secret (der Mail-Provider kennt kein Zertifikat) |
| `ad` ([ADR-0166](../../../docs/adr/0166-active-directory-connector.md)) | `ucc_ad` | Active Directory über LDAPS | Bind-Konto mit Vollzugriff nur auf `OU=AtlasTest`; ohne Domänencontroller Mock-Modus |

Die Worker-Namen entsprechen [`examples/m365-dienste`](../../../examples/m365-dienste/); dessen
Prozesse laufen damit ohne Anpassung. Das API-Token, das diese Prozesse im Vault unter
`atlas` erwarten, legt das Projekt ebenfalls an.

**Testobjekte im Tenant** (Präfix `atlas-test-<zufall>`): drei Testpersonen und eine
vorgesetzte Person (als Manager eingetragen), eine Sicherheitsgruppe mit allen
Testpersonen, eine leere Gruppe und eine Microsoft-365-Gruppe. Zu dieser legt Entra
automatisch eine SharePoint-Teamwebsite an; die Operation `create-team` des
Entra-Workers macht sie zum Test-Team.

> **Annahme zu «Graph»:** Einen eigenen Graph-Worker gibt es im Repository nicht.
> Microsoft Graph wird über `entra`, `sharepoint` und `mail` angesprochen.

### Aufbau

```
                       Microsoft-365-Testtenant
        Entra ID · Exchange Online · SharePoint · Teams
                 ▲  App-Registrierungen je Worker
                 │
 Browser ──HTTPS──► Caddy :443 ──► atlas serve 127.0.0.1:8080   (Azure-VM «atlas»)
                                    └─ überwachte Worker: entra, exchange,
                                       sharepoint, teamsphone (pwsh), mail, ad
                                             │ LDAPS 636, nur im VNet
                                             ▼
                                   Domänencontroller «dc01» (optional)
```

## Voraussetzungen

1. **Ein Microsoft-365-Testtenant** (z. B. aus dem Microsoft 365 Developer Program oder ein
   Testabonnement). OpenTofu kann keinen Tenant anlegen. Ein auch produktiv genutzter
   Tenant nur mit `shared_tenant = true` (siehe «Geteilter Tenant»).
2. **Eine Anmeldung mit genügend Rechten** im Testtenant, am einfachsten *Globaler
   Administrator* (Apps anlegen, Administratorzustimmung erteilen, Verzeichnisrollen
   zuweisen, Benutzer und Gruppen anlegen).
3. **Ein Azure-Abonnement** für Server und Domänencontroller (darf in einem anderen Tenant
   liegen: `azure_tenant_id`), mit dem Recht, eine Ressourcengruppe zu füllen.
4. **Werkzeuge:** OpenTofu ≥ 1.8, Azure CLI (`az`); für die Warteskripte `curl`, `ssh`
   und `openssl`.
5. **Lizenzen** für Postfächer und Teams der Testpersonen (siehe «Lizenzen»). Ohne Lizenz
   laufen Entra, SharePoint und AD vollständig; Exchange-, Teams- und Mail-Tests brauchen
   Postfächer bzw. Teams-Benutzer.
6. Die **eigene öffentliche IPv4-Adresse** für `atlas_allowed_cidrs` (HTTPS und SSH):
   `curl -4 -s https://ifconfig.me`. Ohne `-4` liefert der Dienst bei IPv6-Anschlüssen eine
   IPv6-Adresse; die öffentlichen Adressen der VMs sind nur IPv4, IPv6-Bereiche lehnt der Plan ab.

## Ablauf

### 1. Anmelden und konfigurieren

```bash
az login --tenant <m365-tenant-id> --allow-no-subscriptions
```

Liegt das Azure-Abonnement in einem anderen Tenant, zusätzlich mit diesem anmelden
(`az login --tenant <azure-tenant-id>`) und `azure_tenant_id` setzen.

```bash
cp terraform.tfvars.example terraform.tfvars
```

Mindestens `tenant_id`, `expected_initial_domain`, `azure_subscription_id` und
`atlas_allowed_cidrs` setzen. Der Plan bricht ab, wenn der angemeldete Tenant nicht genau
diese ID **und** diese Initial-Domain hat.

### 2. Aufbauen

```bash
tofu init
```

Zuerst nur den Zufallssuffix anlegen (lokal im State, nichts in Azure oder im Tenant).
Sonst bricht der Plan mit «`mail_nickname` is required for mail-enabled groups» ab: Der
Provider `azuread` hält den beim Planen noch unbekannten Namen der Microsoft-365-Gruppe für
leer. Die Fehlermeldung zum Output `atlas_ssh` bei diesem Teil-Apply ist harmlos.

```bash
tofu apply -target=random_string.suffix
```

```bash
tofu plan -out=plan.tfplan
```

```bash
tofu apply plan.tfplan
```

`apply` dauert einige Minuten. Danach richtet sich der Server selbst ein (Pakete,
PowerShell und das Modul MicrosoftTeams, atlas, Worker); das dauert erfahrungsgemäss
10–20 Minuten:

```bash
./scripts/wait-atlas.sh
```

Mit Domänencontroller zusätzlich (Promotion und Neustart, ebenfalls 10–20 Minuten):

```bash
./scripts/wait-ad.sh
```

`wait-ad.sh` prüft LDAPS von aussen und braucht deshalb `ad_allowed_cidrs`; ohne diese
Freigabe zeigt `tofu output atlas_ssh` den Weg auf den Server, wo
`openssl s_client -connect dc01.atlastest.internal:636` dasselbe prüft.

### 3. Anmelden und arbeiten

```bash
tofu output atlas_url
```

```bash
tofu output -json atlas_admin
```

- **Lokaler Administrator:** `admin` mit dem ausgegebenen Passwort.
- **Testpersonen:** Schaltfläche *Sign in with Microsoft (…)*, Passwörter mit
  `tofu output -json test_user_passwords`. Eine erste Anmeldung legt ein atlas-Konto mit
  der Rolle `user` an; weitere Rollen vergibt der Administrator unter *Organization →
  Users* oder über die Gruppenzuordnung unter *Organization → Single sign-on* (das
  ID-Token enthält die Objekt-IDs der Sicherheitsgruppen).
- **Worker:** *Console → Workers* zeigt `ucc_entra`, `ucc_exchange`, `ucc_sharepoint`,
  `ucc_teams`, `mail` und gegebenenfalls `ucc_ad`; ihre Zugangsdaten liegen im Vault
  unter `m365-<name>`.
- Mit `atlas_tls = "internal"` (Vorgabe) stammt das Zertifikat von Caddys eigener CA;
  der Browser warnt einmal. Ein vertrauenswürdiges Zertifikat liefert
  `atlas_tls = "letsencrypt"` (siehe «Sicherheit»).

Beispielprozesse einspielen, z. B. `examples/m365-dienste` (Anleitung in dessen README,
mit `ATLAS_TOKEN` eines Tokens mit Rolle `modeler`):

```bash
ATLAS_TOKEN=atlasat_... ../../../examples/m365-dienste/installieren.sh "$(tofu output -raw atlas_url)" antworten.json
```

Das Lizenzmanagement (`examples/lizenzmanagement`: Lizenzverträge, Kontingentprüfung der
Lizenzbestellung, täglicher Abgleich, Verrechnung) installiert nach `install-m365-dienste.py`:

```bash
python3 scripts/install-lizenzmanagement.py --cafile out/caddy-root.crt --vertrag-pruefen
```

Es legt die Gruppen Lizenz-Tenantverantwortung, Lizenz-Chef, Lizenzmanagement, Lizenz-Finanzen
und Lizenz-Integrations-Manager an, setzt `LzSku` auf die erste SKU von `license_sku_ids`
(dieselbe wie `M365Lizenzprofil`), importiert und veröffentlicht Applikation, Architektur und
Katalog, übernimmt die Vorschläge der Capability-Landkarte, setzt die Konstanten
(`EA_JAHRESTAG` der nächste 1. Juli) und startet das Tenant-Kontingent. `--vertrag-pruefen`
schaltet in `beispiel-m365-dienste` `LIZENZ_VERTRAG_PRUEFEN = ein`; danach braucht jede
Lizenzbestellung einen Lizenzvertrag (`--standardvertrag LV-…` für Bestellungen ohne). Die
Berechtigungen `LicenseAssignment.Read.All` und `AuditLog.Read.All` des Entra-Workers stehen in
`variables.tf`. Am Tenant ändert das Skript nichts. `install-m365-dienste.py` ersetzt bei jedem Lauf
alle Konstanten von `beispiel-m365-dienste`; danach `install-lizenzmanagement.py --vertrag-pruefen`
wiederholen.

Mit `atlas_tls = "internal"` lehnt `curl` das Zertifikat ab, solange Caddys
Stammzertifikat nicht vertraut ist; es liegt auf dem Server unter
`/var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt`.

### 4. Abbauen

```bash
tofu destroy
```

Gelöschte Benutzer, Gruppen und App-Registrierungen liegen danach noch 30 Tage im
Papierkorb von Entra. Endgültig entfernen (erst auflisten, dann löschen):

```bash
./scripts/purge-deleted.sh atlas-test
```

```bash
./scripts/purge-deleted.sh atlas-test --yes
```

Die SharePoint-Website der gelöschten Gruppe bleibt im Papierkorb der gelöschten Websites
(SharePoint Admin Center) und wird von Microsoft nach 93 Tagen entfernt. Zum Schluss
`out/` löschen.

**Falls `tofu destroy` nicht mehr geht** (State verloren oder beschädigt): Das Notfallskript
findet alles über den Präfix, ohne State. Es entfernt Azure-Ressourcengruppen,
App-Registrierungen, Benutzer und Gruppen. Erst auflisten, dann löschen:

```bash
./scripts/emergency-cleanup.sh atlas-test
```

```bash
./scripts/emergency-cleanup.sh atlas-test --yes
```

Danach `purge-deleted.sh` und, im geteilten Tenant, `shared-tenant-cleanup.ps1`. Weil das
Skript nach Präfix löscht, darf im Tenant **nichts anderes** mit `atlas-test` beginnen.

## Geteilter Tenant (`shared_tenant = true`)

Für einen Tenant, der auch produktiv genutzt wird, etwa den eigenen. Anwendungsberechtigungen
aus Entra gelten immer tenantweit. Deshalb erhalten die Apps, deren Rechte jedes Postfach
erreichen, in Entra **keine** solchen Rechte, sondern eingegrenzte Rechte in Exchange und
SharePoint:

| Worker | Ohne Schalter | Mit `shared_tenant = true` |
|---|---|---|
| `mail` | `Mail.Send` in Entra (jedes Postfach) | kein `Mail.Send` in Entra; Exchange **RBAC for Applications**: «Application Mail.Send» nur im Management Scope |
| `exchange` | Rolle *Exchange Administrator* | keine Verzeichnisrolle; Mitglied einer Exchange-Rollengruppe (*Mail Recipients*, *Mail Recipient Creation*, *Distribution Groups*, *Security Group Creation and Membership*, *Legal Hold*, *View-Only Recipients*) mit **Schreibbereich** = Management Scope |
| `sharepoint` | `Sites.ReadWrite.All` | `Sites.Selected` mit Schreibrecht **nur** auf der Test-Website |
| `teamsphone` | Rolle *Teams Administrator* | **nicht erlaubt**: die Rolle lässt sich nicht eingrenzen (`workers = { teamsphone = false }` setzen) |
| `entra` | `User.ReadWrite.All`, `Group.ReadWrite.All` … | **unverändert** (siehe unten) |
| Zugangsdaten | `credential_validity_days` frei | höchstens 7 Tage |

**Speicherkontingent automatisch setzen (`sharepoint_admin_quota`, Vorgabe `false`).** Der
Katalogschritt `set-site-quota` setzt das Kontingent einer Site über den Admin-Endpunkt von
SharePoint (`<tenant>-admin.sharepoint.com`, ADR-0141). Dieser verlangt die SharePoint-Online-
Anwendungsberechtigung `Sites.FullControl.All` (nicht die von Graph) und eine Anmeldung mit
Zertifikat (`sharepoint_credential = "certificate"`). Die Berechtigung gilt für **jede** Site des
Tenants, auch für echte; ein engeres Recht gibt es für diesen Endpunkt nicht. Ohne den Schalter
fällt der Katalog auf die Aufgabe «Speicherkontingent von Hand setzen» zurück. Nachträglich
anwenden, ohne die Atlas-VM anzufassen: `tofu apply -target=module.sharepoint`.

Der **Management Scope** umfasst alle Empfänger, deren Alias mit dem Präfix der Umgebung
beginnt (`Alias -like 'atlas-test-<zufall>*'`): die Testpersonen und alles, was die Worker
unter diesem Namen anlegen. Ein Testprozess, der ein Funktionspostfach oder eine Verteilerliste
anlegt, muss deren Alias deshalb mit dem Präfix beginnen lassen; sonst lehnt Exchange die
Änderung ab. Lesen (*View-Only Recipients*) bleibt organisationsweit möglich; Exchange kennt für
Leserechte keinen Empfängerbereich.

**Voraussetzung in Exchange:** Kleine Tenants sind anfangs «dehydrated»; Management Scopes
und Rollengruppen lehnt Exchange dann ab. Einmalig als Exchange-Administrator
`Enable-OrganizationCustomization` ausführen (nicht umkehrbar, ändert sonst nichts am Betrieb;
kann einige Zeit brauchen, bis es wirkt). Das Skript prüft das vorab (`IsDehydrated`).

Die Exchange- und SharePoint-Rechte kann OpenTofu nicht setzen. Sie werden nach `tofu apply`
einmal mit einem Skript vergeben, angemeldet als Administrator (PowerShell 7 mit den Modulen
`ExchangeOnlineManagement` und `Microsoft.Graph.Authentication`):

```bash
tofu output -json shared_tenant > out/shared-tenant.json
```

```bash
pwsh ./scripts/shared-tenant-setup.ps1 -Config out/shared-tenant.json -Device
```

Exchange übernimmt Rechte für Anwendungen laut Microsoft erst nach 30 Minuten bis 2 Stunden;
bis dahin lehnt Exchange den Mail-Worker ab. Beim Abbauen **vor oder nach** `tofu destroy`:

```bash
pwsh ./scripts/shared-tenant-cleanup.ps1 -Prefix atlas-test -Device -Yes
```

**Was der Schalter nicht eingrenzt:** Der Entra-Worker behält `User.ReadWrite.All` und
`Group.ReadWrite.All`, denn Graph-Anwendungsberechtigungen lassen sich nicht auf Objekte
begrenzen. Er könnte also auch bestehende normale Konten und Gruppen ändern. Konten mit
Administratorrolle sind davor geschützt; diese Berechtigung allein reicht dafür nicht. Wer
das nicht will, testet zunächst nur lesend (`entra_graph_permissions = ["User.Read.All",
"Group.Read.All"]`). Die prozessseitige Sperre auf Testobjekte (Präfix im Modell, wie in
`examples/account-bestellung`) bleibt die eigentliche Grenze.

Am 2026-11-02 an einem echten Tenant geprüft (siehe «Erfahrungen mit einem echten Tenant»):
der Filter `Alias -like …`, das Anlegen von Verteilerlisten und Funktionspostfächern im
Schreibbereich, der Exchange-Worker mit der Rollengruppe statt der Verzeichnisrolle und der
Gegentest mit `Test-ServicePrincipalAuthorization` (die Mail-App darf als Testpostfach senden,
als echtes Postfach nicht).

## atlas-Server

- **Installation:** `atlas_install = "release"` lädt `atlas_version` von den GitHub-Releases
  und prüft es gegen `SHA256SUMS`. `atlas_install = "source"` baut `atlas_source_ref` aus
  `atlas_source_repo` auf der VM (Go wird über `GOTOOLCHAIN=auto` passend geladen); so
  lässt sich ein Branch in der Umgebung testen. Das Repository muss dann ohne Anmeldung
  klonbar sein.
- **Dienst:** `atlas serve --addr 127.0.0.1:8080 --auth` als systemd-Dienst nach
  [`docs/install.md`](../../../docs/install.md), davor Caddy für TLS. Atlas startet und
  überwacht die Worker selbst; sie erben dessen Umgebung.
- **atlas hub:** Mit `atlas_hub = true` (Vorgabe) zeigt die Console unter «atlas hub» die
  Bundles dieses Repositorys (`examples/hub`), etwa `ad-dienste` mit dem AD-Katalog. Der
  Schlüssel kommt aus `examples/hub/releases.json` des eigenen Checkouts. Die Einstellung
  steht in `/etc/atlas/atlas.env` und damit in `custom_data`: Auf einer laufenden VM ändert
  sie ein `tofu apply` nur durch Ersetzen der VM. Dort trägt man die beiden Zeilen von Hand
  nach und startet `atlas` neu.
- **Einrichtung der Worker** (`scripts/atlas-bootstrap.py`): meldet sich als
  Administrator an, legt je Worker das Credential-Bundle im Vault ab
  (`PUT /api/v1/secrets/m365-<name>`), erstellt den Console-Worker
  (`POST /api/v1/configured-workers`) und ein API-Token im Vault unter `atlas`. Danach
  löscht es die Datei mit den Zugangsdaten; sie liegen dann nur noch im Vault von atlas.
  Das Skript ist wiederholbar und gegen einen lokal gebauten atlas-Server aus diesem
  Repository geprüft.
- **Zugriff aus einem Firmennetz:** Proxys, die HTTPS prüfen (geprüft mit dem Netz der
  Bundesverwaltung), sperren eine Seite mit Caddys eigenem Zertifikat (`atlas_tls =
  "internal"`). Dann `atlas_tls = "letsencrypt"` setzen; 80 und 443 sind danach für alle
  offen, SSH bleibt auf `atlas_allowed_cidrs` beschränkt. Eine laufende VM lässt sich ohne
  Ersatz umstellen: `tofu apply -target=azurerm_network_security_group.atlas`, auf der VM
  die Zeile `tls internal` aus `/etc/caddy/Caddyfile` (und `/etc/atlas/Caddyfile`) entfernen,
  `systemctl reload caddy`. Weitere Adressen für `atlas_allowed_cidrs` gehen ebenso nur
  über die Netzwerksicherheitsgruppe.
- **Protokolle:** `/var/log/atlas-setup.log`, `journalctl -u atlas`, `journalctl -u caddy`.
- **Automatische Abschaltung** täglich um `atlas_auto_shutdown_time` (Vorgabe 19:00 UTC).
  Wieder starten mit `az vm start -g <ressourcengruppe> -n <vm>` oder im Azure-Portal.

## Active Directory (optional)

Mit `enable_ad_dc = true` entsteht im selben VNet eine Windows-Server-2022-VM:

- Ein Key Vault stellt ein selbstsigniertes LDAPS-Zertifikat aus (Namen: öffentlicher
  DNS-Name, `dc01.<ad_domain_name>`, Domänenname) und installiert es bei der
  Bereitstellung in `LocalMachine\My`.
- `scripts/ad-setup.ps1` installiert AD DS und die Gesamtstruktur `atlastest.internal`,
  nach dem Neustart `OU=AtlasService` mit dem Bind-Konto `svc-atlas-<zufall>` (Vollzugriff
  nur auf `OU=AtlasTest`, dazu *Replicating Directory Changes* für `sync`) sowie
  `OU=AtlasTest` mit Testpersonen und einer Testgruppe.
- Der atlas-Server erreicht den Domänencontroller als `ldaps://dc01.atlastest.internal:636`
  im VNet und vertraut dessen Zertifikat über den Systemspeicher. Der AD-Worker kennt
  keine Option, die Zertifikatsprüfung abzuschalten (`connector/ad/client.go`).
- Von aussen ist der Domänencontroller nur für `ad_allowed_cidrs` erreichbar (LDAPS,
  mit `ad_allow_rdp` auch RDP).

Ohne Domänencontroller startet der Server den AD-Worker mit `ATLAS_AD_MOCK=1` (ADR-0181).

## Exchange Server (optional)

Mit `enable_exchange_onprem = true` (braucht `enable_ad_dc`) entsteht ein Exchange Server SE
für das On-Premises-Backend des Exchange-Workers (ADR-0462, `"target": "onprem"`):

- VM `<umgebung>-exch` (`exchange_vm_size`, Vorgabe `Standard_D4s_v5` mit 16 GB, 256-GB-Platte),
  Windows Server 2022, Name `exch01.<ad_domain_name>`, interne Adresse `10.42.0.11`, DNS über
  den Domänencontroller. Von aussen erreichbar ist sie nicht, ausser mit `ad_allow_rdp` per RDP
  für `ad_allowed_cidrs`; die öffentliche Adresse dient den Downloads.
- `scripts/exchange-setup.ps1` installiert unbeaufsichtigt in vier Phasen mit Neustarts:
  Domänenbeitritt; Windows-Features, Visual C++ 2012/2013, URL Rewrite und das Image
  (`exchange_iso_url`, 6 GB, mit Fortsetzung geladen und an `exchange_iso_bytes` geprüft);
  UCMA, `/PrepareSchema`, `/PrepareAD /OrganizationName:AtlasTest` und die Mailbox-Rolle als
  Domänenadministrator; zuletzt das Dienstkonto `svc-exch-<zufall>` in *Organization
  Management* und ein Postfach für jede Person in `OU=AtlasTest`. Exchange SE läuft ohne
  Produktschlüssel als Testversion.
- Das dauert ein bis zwei Stunden nach dem Apply. `scripts/wait-exchange.sh` verfolgt es über
  den Run Command von Azure (kein eingehender Port nötig) und zeigt bei einem Fehler das Ende
  des Protokolls (`C:\AtlasSetup\exchange-phase<N>.log`).
- `tofu output exchange_onprem` nennt den Endpunkt `http://exch01.<ad_domain_name>/PowerShell/`
  (Kerberos) und das Dienstkonto; das Passwort gibt `tofu output -raw exchange_service_password`.
- Abschaltung täglich um `exchange_auto_shutdown_time` (Vorgabe 19:00 UTC).

Danach startet `onprem/install-worker.sh` den Exchange-Worker `ucc_exchange_onprem` auf `exch01`
selbst, als Dienstkonto (Kerberos ohne Passwort in Atlas): Es baut `atlas.exe`, bietet es 30
Minuten lang nur im VNet von der Atlas-VM an, erzeugt ein Worker-Token und führt
`onprem/exchange-worker.ps1` über den Run Command aus (Token und Passwort als geschützte
Parameter). `onprem/exchange-onprem-smoke.bpmn` prüft jede Operation von Funktionspostfach,
Postfachzugriff und Verteiler einmal hin und zurück und hinterlässt nichts.

**Geprüft am 2026-11-03:** Installation in rund 55 Minuten (Image in 7 Minuten), Worker auf
`exch01`, Rauchtest vollständig: Funktionspostfach anlegen und kennzeichnen, Vollzugriff und
«Senden als» erteilen, Postfach über die Kennzeichnung finden, Verteiler anlegen, Mitglied
aufnehmen und entfernen, Verteiler entfernen, Rechte entziehen, Postfach entfernen.

Danach über den Shop (Katalogvariante «Exchange Server», `install-m365-dienste.py`):
Funktionspostfach bestellt (Vollzugriff und «Senden als» für die Besitzerin aus der
Personensuche) und zurückgegeben, Postfachzugriff auf ein persönliches Postfach mit Prüfung der
Informationssicherheit erteilt und entzogen; alles gegen `exch01`, nichts blieb zurück.

Dabei gefunden:

- «Senden als» (`Add-ADPermission`) fand das Postfach nicht über seine Adresse; der Worker
  löst die Identität jetzt über `Get-Recipient` auf (`connector/exchange`, Branch
  `feat/m365-dienste-shared-tenant`). `install-worker.sh` baut dafür mit
  `ATLAS_REPO=<checkout>` aus jenem Branch.
- Atlas verteilte Exchange-Jobs nur nach Jobtyp, nicht nach Worker-Name: Der Online-Worker
  `ucc_exchange` übernahm einen Job für `ucc_exchange_onprem` und scheiterte mit «no worker
  registered». Behoben in PR #1523 (ein Worker erhält nur noch Jobs der Namen, die er meldet);
  mit diesem Stand lief der Rauchtest bei laufendem Online-Worker durch. Ohne ihn den
  Online-Worker für den Rauchtest kurz ausschalten (Console → Workers, `ucc_exchange`).
- Der Runner startet `atlas.exe` über `cmd.exe`: Windows PowerShell 5.1 bricht sonst mit
  `ErrorActionPreference Stop` an der ersten Protokollzeile auf stderr ab.

Eine laufende Umgebung erhält den Server, ohne dass die Atlas-VM ersetzt wird, gezielt:
`tofu apply -target=azurerm_virtual_machine_extension.exchange_setup
-target=azurerm_dev_test_global_vm_shutdown_schedule.exchange
-target=azurerm_network_interface_security_group_association.exchange`.

## Lizenzen

Lizenzen kann OpenTofu nicht kaufen. Mit `license_sku_ids` weist das Projekt freie
Lizenzen den Testpersonen zu (Skript `scripts/assign-licenses.sh` über `az rest`). Die
verfügbaren `skuId` zeigt:

```bash
az rest --method get --url https://graph.microsoft.com/v1.0/subscribedSkus --query "value[].{sku:skuPartNumber,id:skuId,frei:prepaidUnits.enabled}"
```

Absender des Mail-Workers ist die vorgesetzte Testperson (`mail_sender`); sie braucht
dafür ein Postfach. Teams-Phone-Ressourcenkonten brauchen die kostenlose Lizenz
*Microsoft Teams Phone Resource Account*, die im Admin Center bezogen wird.

## Kosten (Schätzung)

**Annahmen:** Region Switzerland North, nutzungsbasierte Preise (Pay-as-you-go) in USD,
ohne Rabatte und Mehrwertsteuer, Stand der Quellen unbekannt. Die Werte sind
**Schätzungen**: Der Linux-Preis der VM stammt aus Drittanbieter-Preislisten, der
Windows-Aufschlag, die Datenträger und die IP-Adressen aus allgemeinen Erfahrungswerten.
Massgebend ist der [Azure-Preisrechner](https://azure.microsoft.com/pricing/calculator/).

| Posten | Annahme | ca. USD |
|---|---|---|
| atlas-VM `Standard_B2ms` (Linux) | ca. 0.106 pro Stunde | 0.11 / Stunde |
| Domänencontroller `Standard_B2ms` (Windows) | Linux-Preis + Windows-Lizenz | ca. 0.20 / Stunde |
| Datenträger atlas (64 GB Standard SSD) | läuft auch bei gestoppter VM | ca. 5 / Monat |
| Datenträger DC (127 GB Standard SSD) | läuft auch bei gestoppter VM | ca. 10 / Monat |
| Öffentliche IP-Adresse (Standard, statisch) | je VM, auch bei gestoppter VM | ca. 3.5–4 / Monat |
| Key Vault, Datenverkehr | wenige Operationen, wenig Daten | unter 1 / Monat |
| Entra-Objekte, App-Registrierungen | im Tenant enthalten | 0 |

| Szenario | Laufzeit | ca. USD |
|---|---|---|
| Nur Tenant-Objekte (`enable_atlas_server = false`) | – | 0 (Azure) |
| atlas-Server, 2 Testtage rund um die Uhr | 48 h | ca. 6 |
| atlas-Server + DC, 2 Testtage rund um die Uhr | 48 h | ca. 16 |
| atlas-Server, Arbeitstage 8 h (22 Tage, Rest abgeschaltet) | 1 Monat | ca. 28 |
| atlas-Server + DC, Arbeitstage 8 h | 1 Monat | ca. 80 |
| atlas-Server + DC, durchgehend | 1 Monat | ca. 250 |

**Microsoft-365-Lizenzen** sind nicht eingerechnet. Ein Tenant aus dem Developer Program
oder ein Testabonnement kostet nichts; gekaufte Lizenzen (z. B. Microsoft 365 E3/E5 je
Testperson und Monat) übersteigen die Azure-Kosten schnell.

Die automatische Abschaltung (Vorgabe 19:00 UTC) gibt die VMs frei; danach fallen nur
noch Datenträger und IP-Adressen an. `tofu destroy` beendet alle Kosten.

## Sicherheit

- **Der State enthält alle erzeugten Geheimnisse** (private Schlüssel, Secrets,
  Passwörter). Er liegt standardmässig lokal in `terraform.tfstate` und gehört nicht in
  Git (`.gitignore`). Empfohlen ist die eingebaute State-Verschlüsselung von OpenTofu
  (Umgebungsvariable `TF_ENCRYPTION`, Schlüsselquelle z. B. `pbkdf2`); die genaue Syntax
  steht in der OpenTofu-Dokumentation unter *State and Plan Encryption*.
- **Kurze Laufzeit:** Zertifikate, Secrets und das API-Token laufen nach
  `credential_validity_days` (Vorgabe 14 Tage) ab.
- **Anwendungsberechtigungen gelten tenantweit** (`Mail.Send` darf als jedes Postfach
  senden). Ohne `shared_tenant` ist die Grenze allein der Testtenant; deshalb gegen einen
  auch produktiv genutzten Tenant nur mit `shared_tenant = true` anwenden, und auch dann
  bleibt der Entra-Worker tenantweit (siehe «Geteilter Tenant»).
- **Netz:** SSH und (mit `atlas_tls = "internal"`) HTTPS nur für `atlas_allowed_cidrs`.
  `atlas_tls = "letsencrypt"` öffnet 80 und 443 für das ganze Internet, weil Let's Encrypt
  von nicht veröffentlichten Adressen prüft; die Anmeldung bleibt Pflicht. Ob für
  `*.cloudapp.azure.com` die Ratenlimits von Let's Encrypt greifen, ist nicht geprüft.
- **Zugangsdaten auf den VMs:** Der Server erhält sie über Cloud-Init (nur für root
  lesbar, nach dem Bootstrap gelöscht); der Domänencontroller über die geschützten
  Einstellungen der Custom Script Extension. Für eine Testumgebung vertretbar, für
  Produktion nicht.

## Prüfung

`tests/environment.tftest.hcl` prüft die Logik ohne Tenant und ohne Azure (alle Provider
gemockt): Tenant-Sperren, Apps, Console-Worker und Bundles des Bootstraps, die
Server-Umgebung, Netzfreigaben je TLS-Variante, den AD-Schalter und die Grösse der
Cloud-Init-Daten.

```bash
tofu init -backend=false
```

```bash
tofu test
```

`tofu test` liest eine vorhandene `terraform.tfvars` mit ein. Das Testfile setzt deshalb alle
Werte, die eine echte Konfiguration üblicherweise setzt, selbst (vor allem
`license_sku_ids = []`): Provisioner laufen auch mit gemockten Providern, und
`scripts/assign-licenses.sh` würde sonst mit der Anmeldung von `az` den echten Graph aufrufen.
Wer neue Variablen mit Wirkung nach aussen ergänzt, setzt sie dort ebenfalls.

## Erfahrungen mit einem echten Tenant (2026-11-02)

Ein vollständiger Durchlauf im geteilten Tenant (`shared_tenant = true`, Business Basic,
Domänencontroller an), mit `examples/m365-dienste` installiert und über den Shop bestellt.
Was dabei auffiel und wie es gelöst ist; was nur von Hand ging, steht unter «Von Hand».

**Korrigiert im Projekt**

| Befund | Ursache | Lösung |
|---|---|---|
| `tofu test` mit OpenTofu: jeder Lauf übersprungen | Mock von `azuread_domains` ohne Pflichtattribute | Mock vollständig |
| `tofu test` rief mit echter `az`-Anmeldung `assignLicense` gegen Graph auf | `tofu test` liest `terraform.tfvars`; Provisioner laufen auch mit gemockten Providern | Testfile setzt alle Werte selbst (`license_sku_ids = []` …) |
| `ifconfig.me` lieferte IPv6, `/32` darauf wäre ein riesiger Bereich | CIDR-Prüfung nahm IPv6 und `::/0` an | nur IPv4-CIDR, `/0` abgelehnt; README: `curl -4` |
| Plan bricht ab: «`mail_nickname` is required for mail-enabled groups» | `azuread` hält den beim Planen unbekannten Namen für leer | zuerst `tofu apply -target=random_string.suffix` (Ablauf) |
| Atlas-Server richtet sich nicht ein | `AD_HOSTS_LINE=10.42.0.10 dc01…` ohne Anführungszeichen in `setup.env` | alle Werte in `'…'`, Test prüft die Form |
| Zertifikate und DC-Extension bei jedem Plan «geändert» | `end_date` lokal vs. UTC; Tag-Format von Azure | `ignore_changes` |
| LDAPS antwortet nicht (Ereignis 1220, Schannel 36886) | selbstsigniertes Zertifikat nicht in `LocalMachine\Root` | `ad-setup.ps1` legt es dort ab |
| `wait-atlas.sh` meldete Erfolg nach Zeitüberschreitung | `break` mit Exit 0 | Exit 1 mit Protokollauszug |
| `wait-ad.sh` gab das Bind-Passwort auf der Kommandozeile mit | `ldapwhoami -w` | Datei mit `-y` |
| `New-ManagementScope` abgelehnt | Exchange-Organisation «dehydrated» | Skript prüft `IsDehydrated`, README nennt `Enable-OrganizationCustomization` |
| SharePoint-Recht: Schleife «not provisioned yet» | 403 auf die private Gruppen-Website wurde als 404 behandelt; delegiert fehlt der Admin der Zugriff | Admin wird kurz Besitzer der Testgruppe, im `finally` wieder entfernt; echte Fehler werden gemeldet |
| `GET /me` 403 | `User.Read` nicht angefordert | Scope ergänzt |
| Verteiler entfernen, Mitglieder ändern: 403 | `BypassSecurityGroupManagerCheck` (und `-Type`) kommen mit *Security Group Creation and Membership* | Rolle in der Rollengruppe, gleicher Schreibbereich; Skript ergänzt fehlende Rollen |
| Personensuche im Bestellformular: 403 | der Entra-Worker war nicht für Formulare geöffnet | Bootstrap setzt `formLookups` |
| Suche im geteilten Tenant zeigte alle echten Konten | kein fester Filter möglich auf den Präfix | Testpersonen erhalten `department = <Umgebungs-ID>`, das Installationsskript filtert darauf |

**Von Hand (noch nicht automatisiert)**

- `Enable-OrganizationCustomization` einmal als Exchange-Administrator; danach brauchte die
  Delegierung von «Application Mail.Send» rund eine Stunde, bis `New-ManagementRoleAssignment`
  gelang («keine Delegierungsrollenzuweisung»).
- `Connect-MgGraph -UseDeviceCode` (SharePoint-Teil) braucht ein echtes Terminal; ohne eins
  meldet es nach 120 s «timed out», obwohl die Anmeldung im Browser gelang. `-Part SharePoint`
  wiederholt nur diesen Teil.
- Neue Rollen einer App wirken in Exchange erst nach 30 min bis 2 h; `Test-ServicePrincipalAuthorization`
  zeigt die Zuweisung sofort.
- Testpersonen, die sich nach der Installation zum ersten Mal anmelden, sind in keiner
  Zielgruppe; `install-m365-dienste.py` erneut ausführen nimmt sie auf. Besser wäre die Gruppenzuordnung bei der SSO-Anmeldung
  (Sicherheitsgruppe der Umgebung → Zielgruppe).
- Teams: ein erstes Team wurde von Hand angelegt; ob das nötig war, ist offen, denn danach
  schlug `PUT /groups/{id}/team` weiter fehl, und erst `POST /teams` (Entra-Worker) gelang.

**Noch offen**

- Ein normaler `tofu apply` nach Änderungen an `atlas-bootstrap.py` oder `atlas-setup.sh`
  ersetzt die Atlas-VM (Cloud-Init-Daten); `ignore_changes` wäre zu erwägen.
- M365-Gruppen (Teams) erhalten die Standard-Domain des Tenants (die eigene, nicht die `onmicrosoft.com`-Domain), nicht
  `MAIL_DOMAIN`.
- Am 2026-11-03 zusätzlich geprüft: Rückgabe des Verteilers, Postfachzugriff erteilen und
  entziehen, Lizenz, Abwesenheit; Zugriff aus dem Netz der Bundesverwaltung mit
  `atlas_tls = "letsencrypt"`.
- Ungetestet: Sitzungszimmer, Partnerzugang, übrige Aktionen an Bestellungen,
  Governance-Prüfung, Teams Phone.

## Bekannte Grenzen

- **Einmal gegen einen echten Tenant und ein echtes Azure-Abonnement gelaufen**
  (2026-11-02, geteilter Tenant, OpenTofu 1.13.2, `azuread` 3.10.0): `atlas-setup.sh`,
  `ad-setup.ps1`, Cloud-Init mit gzip, die Lizenzzuweisung über `az rest`, die Warteskripte
  und `shared-tenant-setup.ps1` laufen nach den Korrekturen im Abschnitt «Erfahrungen mit
  einem echten Tenant». Ein zweiter Durchlauf von Grund auf mit diesen Korrekturen steht aus.
- **Exchange Server (on-premises)** ist optional (`enable_exchange_onprem`, Abschnitt
  «Exchange Server (optional)») und einmal am 2026-11-03 installiert und mit dem Rauchtest geprüft.
- **Teams Phone:** Rufnummern, Calling Plans und Direct Routing richtet das Projekt nicht
  ein. Ob `Connect-MicrosoftTeams` über `User.ReadWrite.All` hinaus Graph-Berechtigungen
  verlangt, ist nicht geprüft (`teamsphone_graph_permissions`).
- **Ein Team** legt erst der Entra-Worker an (`create-team`).
- **Gruppenmitgliedschaften** ignoriert OpenTofu nach dem ersten Anlegen, damit die
  Worker sie ändern dürfen.

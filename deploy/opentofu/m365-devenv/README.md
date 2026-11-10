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
- optional einen **Active-Directory-Domänencontroller** im selben Netz und dazu einen
  **Exchange Server SE** für das On-Premises-Backend des Exchange-Workers und eine
  **SharePoint-Server-SE-Farm** mit SQL Server Developer auf einer VM.

Danach spielen zwei Skripte die Beispiele `examples/m365-dienste` und
`examples/lizenzmanagement` passend zum Tenant ein (siehe «Beispiele einspielen»).

Das Projekt ist für **Entwicklung und temporäre Tests** gedacht, nicht für den Betrieb.

## Inhalt

| Worker Type | Worker in der Console | Ziel | Berechtigungen der App |
|---|---|---|---|
| `entra` ([ADR-0172](../../../docs/adr/0172-entra-id-connector.md)) | `ucc_entra` | Microsoft Graph: Benutzer, Gruppen, Teams | `User.ReadWrite.All`, `Group.ReadWrite.All`, `TeamMember.ReadWrite.All`, `Channel.Create`, `TeamSettings.ReadWrite.All`, `User.Invite.All`, `LicenseAssignment.Read.All` (Lizenzbestand, `list-subscribed-skus`), `AuditLog.Read.All` (letzte Anmeldung, `signInActivity`; nur mit Entra ID P1/P2 im Tenant); Zertifikat |
| `exchange` ([ADR-0462](../../../docs/adr/0462-exchange-worker.md)) | `ucc_exchange` | Exchange Online | `Exchange.ManageAsApp` + Rolle *Exchange Administrator*; Zertifikat |
| `sharepoint` ([ADR-0141](../../../docs/adr/0141-sharepoint-connector.md)) | `ucc_sharepoint` | SharePoint Online | `Sites.ReadWrite.All` (`sharepoint_graph_permissions`; im geteilten Tenant `Sites.Selected`, mit `sharepoint_read_all_sites` zusätzlich `Sites.Read.All`); mit `sharepoint_admin_quota` die SharePoint-Berechtigung `Sites.FullControl.All` für `set-site-quota`; Zertifikat oder Secret (`sharepoint_credential`) |
| `teamsphone` ([ADR-0463](../../../docs/adr/0463-teams-phone-worker.md)) | `ucc_teams` | Teams Phone (PowerShell) | `User.ReadWrite.All` + Rolle *Teams Administrator*; Zertifikat |
| `mail` ([ADR-0079](../../../docs/adr/0079-outbound-mail-connector.md)) | `mail` | Versand über Microsoft Graph | `Mail.Send`; Secret (der Mail-Provider kennt kein Zertifikat) |
| `ad` ([ADR-0166](../../../docs/adr/0166-active-directory-connector.md)) | `ucc_ad` (`worker_names.ad`) | Active Directory über LDAPS | Bind-Konto mit Vollzugriff nur auf `OU=AtlasTest`; nur mit `enable_ad_dc` (sonst Mock-Modus, siehe «Active Directory») |
| `exchange`, Ziel `onprem` | `ucc_exchange_onprem` | Exchange Server SE auf `exch01` | Dienstkonto `svc-exch-<zufall>` in *Organization Management* (Kerberos); läuft auf `exch01` selbst, gestartet von `onprem/install-worker.sh`, nicht vom Bootstrap (siehe «Exchange Server») |

Die Worker-Namen entsprechen [`examples/m365-dienste`](../../../examples/m365-dienste/); dessen
Prozesse laufen damit ohne Anpassung. Das API-Token, das diese Prozesse im Vault unter
`atlas` erwarten, legt das Projekt ebenfalls an. Die Berechtigungen des Entra-Workers setzt
`entra_graph_permissions`; `LicenseAssignment.Read.All` und `AuditLog.Read.All` braucht das
Lizenzmanagement (Lizenzbestand und letzte Anmeldung).

**Zwei Abweichungen von den Beispielen, die von Hand zu erledigen sind:**

- [`examples/ad-dienste`](../../../examples/ad-dienste/) und der Lizenzmodus `ad-gruppe` von
  `m365-dienste` nennen den AD-Worker `ad_verzeichnis`, die Umgebung nennt ihn `ucc_ad`. Vor
  dem ersten `tofu apply` `worker_names = { ad = "ad_verzeichnis" }` setzen. Auf einer
  laufenden VM ändert das die Cloud-Init-Daten und ersetzt die VM; dort legt man den Worker
  stattdessen in der Console unter dem neuen Namen an (Worker-Typ AD, Verbindung
  `ldaps://dc01.<ad_domain_name>:636`, Zugangsdaten aus dem Vault-Eintrag `m365-ucc_ad`).
- **«Forms may search»** (`formLookups`, ADR-0473) schaltet der Bootstrap nur am Entra-Worker
  ein (Personensuche in den Bestellformularen). Die Personenfelder von `ad-dienste` suchen über
  den AD-Worker (dort schaltet ihn `scripts/install-ad-dienste.py` ein), die Live-Prüfung der Site-URL im Bestellformular der SharePoint-Teamwebsite
  fragt zusätzlich `ucc_sharepoint` und `ucc_exchange`. Bei diesen Workern den Schalter unter
  *Console → Workers* beim Bearbeiten des Workers einschalten; beim Anlegen bietet die Console
  ihn nicht an.

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
                                             │ Kerberos, nur im VNet
                                             ▼
                                   Exchange Server «exch01» (optional)
                                   └─ Worker ucc_exchange_onprem (atlas.exe)
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

Beispiele einspielen: `scripts/install-m365-dienste.py`, danach
`scripts/install-lizenzmanagement.py` (siehe «Beispiele einspielen»).

Mit `atlas_tls = "internal"` lehnt `curl` das Zertifikat ab, solange Caddys
Stammzertifikat nicht vertraut ist; es liegt auf dem Server unter
`/var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt`. Die
Installationsskripte nehmen es mit `--cafile` (z. B. nach `out/caddy-root.crt` kopiert).

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

**Was nicht im State steht und `tofu destroy` deshalb nicht entfernt:**

- **Was der Katalog im Tenant anlegt**: Funktionspostfächer, Verteiler, Sitzungszimmer und
  Raumlisten, Gerätepostfächer, Teams und Microsoft-365-Gruppen, SharePoint-Teamwebsites,
  Mail-Kontakte der Verteiler-Anmeldung und die **Partnerkonten** aus dem öffentlichen
  Formular. Am saubersten gibt man die Bestellungen vor dem Abbau im Shop zurück. Mit
  `NAME_PREFIX` (siehe «Beispiele einspielen») tragen Anzeigename und Alias dieser Objekte das
  Präfix der Umgebung, auch ein Partnerkonto; Benutzer und Gruppen in Entra findet
  `emergency-cleanup.sh <umgebungs-id>` deshalb ebenfalls. In Exchange verwaltete Gruppen
  (Verteiler, Raumlisten) und Mail-Kontakte löscht Microsoft Graph laut Microsoft nicht; sie
  werden in Exchange Online entfernt (`Remove-DistributionGroup`, `Remove-MailContact`), am
  einfachsten nach `Get-Recipient -Filter "Alias -like '<umgebungs-id>*'"`.
- **Was `shared-tenant-setup.ps1` in Exchange Online anlegt** (Management Scope,
  Rollengruppe, Rollenzuweisungen, Dienstprinzipale) und die Liste «Site-Inventar» auf der
  Test-Website: Die Exchange-Objekte entfernt `shared-tenant-cleanup.ps1`, die Liste geht mit
  der Website der Testgruppe.
- **Alles, was in Atlas liegt** (Applikationen, Kataloge, Bestellungen, Vault), liegt auf der
  Platte der Atlas-VM und geht mit ihr.
- Der Run Command `atlas-exchange-worker` auf `exch01` wird von `onprem/install-worker.sh`
  selbst wieder gelöscht; der Worker selbst geht mit der VM.

## Testszenario

Einen durchgehenden Test mit einer erfundenen Firma beschreibt
[TESTSZENARIO.md](TESTSZENARIO.md).
Den wiederholbaren Abnahmetest aller Kataloge, erzählt als Hochlauf eines KMU, den ein
Agent über MCP in mehreren Rollen durchführt, beschreibt [TESTLAUF-KATALOG.md](TESTLAUF-KATALOG.md).

## Beispiele einspielen

Die Pakete unter `examples/` lassen sich mit ihrem `installieren.sh` in jede Atlas-Instanz
einspielen. Für diese Umgebung gibt es drei Skripte, die die Pakete vorher an den Tenant
anpassen und als lokaler Administrator installieren (Passwort aus
`tofu output -json atlas_admin`, nie ausgegeben). Alle brauchen `python3` und `tofu`, laufen
aus `deploy/opentofu/m365-devenv` nach `wait-atlas.sh`, dürfen beliebig oft laufen und ändern
nichts in Azure oder im Tenant.

### `scripts/install-m365-dienste.py`

```bash
python3 scripts/install-m365-dienste.py --cafile out/caddy-root.crt
```

| Option | Vorgabe | Wirkung |
|---|---|---|
| `--cafile` | – | Caddys Stammzertifikat bei `atlas_tls = "internal"`; mit `letsencrypt` weglassen |
| `--sku` | erste SKU von `license_sku_ids` | Lizenz, die die drei Varianten (E3, E5, F3) in `M365Lizenzprofil` erhalten |
| `--mail-domain` | Initial-Domain des Tenants | `MAIL_DOMAIN` und `PARTNER_UPN_DOMAIN` |
| `--site-inventory` | – | `SITE_INVENTAR` (`<host>:/sites/<pfad>`), die Site mit der Liste «Site-Inventar»; ohne die Option fehlt die SharePoint-Teamwebsite im Katalog |
| `--no-onprem` | – | lässt die Exchange-Server-Variante weg, auch wenn `exch01` gebaut ist |
| `--tofu-dir`, `--example` | dieses Verzeichnis, `examples/m365-dienste` | woher die Ausgaben und das Paket kommen |

Was es tut:

1. Es legt die Gruppen an, die das Paket anspricht: `M365-Administration`, `M365-Telefonie`,
   `Informationssicherheit`, dazu die Zielgruppen und Genehmigenden aus `fragen.json`
   (`M365-Mitarbeitende`, `M365-UCC`, `M365-Facility`). Testpersonen, die sich schon einmal
   angemeldet haben, kommen in alle diese Gruppen; wer sich später zum ersten Mal anmeldet,
   sieht keinen Katalog, bis das Skript wieder läuft.
2. Es passt eine Kopie des Pakets an: Die drei Varianten erhalten die eine Lizenz des Tenants;
   jedes Personenfeld der Formulare erhält den festen Filter `department` = Umgebungs-ID, damit
   die Suche im geteilten Tenant nur die Testpersonen anbietet; das Bestellformular der Site
   erhält den SharePoint-Host des Tenants. **Weggelassen** werden immer `m365-engage` (braucht
   `Community.ReadWrite.All` auf dem ganzen Tenant), `m365-aufbewahrung` (braucht Exchange
   Online Plan 2) und die beiden Produkte der hybriden Identität (die Umgebung synchronisiert
   nichts), ohne Teams-Phone-Worker auch Telefonie, Anrufwarteschlange und Telefonzentrale,
   ohne `--site-inventory` die SharePoint-Teamwebsite. Ein Katalog ohne Produkte
   (`cat-m365-identitaet`) fällt weg.
3. Es lädt die Entscheidungsmodelle hoch und importiert die Applikation
   `beispiel-m365-dienste` als Quellbaum mit Informationsmodell und Architektur (ADR-0528).
   Fehlt der Capability-Landkarte des Servers ein Eintrag, übernimmt es den Vorschlag des
   Pakets über die API und nennt ihn im Protokoll; einen bestehenden Eintrag überschreibt es
   nicht, und eine Lücke ohne Vorschlag beendet es mit dem Hinweis, in der Console zu
   entscheiden. Danach veröffentlicht es Applikation und Katalog.
4. Es setzt **alle** Konstanten der Applikation neu: `NAME_PREFIX` (Umgebungs-ID mit
   Bindestrich, im geteilten Tenant; so liegt alles, was der Katalog anlegt, im Exchange-Scope
   und wird von den Abbauskripten gefunden), `MAIL_DOMAIN`, `USAGE_LOCATION`
   (`M365_USAGE_LOCATION`, Vorgabe `CH`), `PARTNER_UPN_DOMAIN`, `ATLAS_API_BASE`,
   `SHAREPOINT_HOST` und mit `--site-inventory` `SITE_INVENTAR`. Konstanten, die man von Hand
   gesetzt hat (etwa `LIZENZ_MODUS`), sind danach weg.
5. **Exchange-Server-Variante**, wenn `exch01` gebaut ist: eine zweite Applikation
   `beispiel-m365-dienste-onprem` mit Kopien der Prozesse und Formulare von Funktionspostfach,
   Verteiler, Postfachzugriff, Zusätzlicher Kalender, Sitzungszimmer und Gerät (Prozess-IDs `…_onprem`, Formulare
   `…-onprem`, Nachrichten `m365onprem.*`, Worker `ucc_exchange_onprem`, auch der aufgerufene
   Prozess `proc_m365_raumliste`) und der Katalog «Exchange Server (OnPrem)»
   (`cat-m365-onprem`, Rang 400, Zielgruppe `M365-Mitarbeitende`) mit den Produkten
   `…-onprem`. Genehmigung und Entscheidungen bleiben die der Online-Applikation. Konstanten:
   `NAME_PREFIX`, `MAIL_DOMAIN` = Domäne von `exch01`, `ATLAS_API_BASE` und
   `ORTSANGABEN = false`, weil `Set-Place` nur Exchange Online kennt.

### SharePoint-Teamwebsite

Das Produkt braucht eine Inventarliste. Im geteilten Tenant legt sie `shared-tenant-setup.ps1`
(Teil `SharePoint`) auf der Test-Website an: Liste «Site-Inventar» mit den Spalten `Alias`,
`SiteUrl`, `GroupId`, `Sprache`, `Position`, `Status` und `KontingentGB`. Das Skript gibt am
Schluss den Wert für `--site-inventory` aus. Dazu gehören:

- `sharepoint_read_all_sites = true`: Im geteilten Tenant hat die SharePoint-App nur
  `Sites.Selected` auf der Test-Website; `get-site` auf eine neu bestellte Site (Existenz,
  Kontingent) braucht `Sites.Read.All`, das alle Sites des Tenants lesen lässt.
- `sharepoint_admin_quota = true` (optional): `set-site-quota` setzt das Speicherkontingent
  selbst (siehe «Geteilter Tenant»); sonst erhält `M365-SharePoint` die Aufgabe
  «Speicherkontingent von Hand setzen». Diese Gruppe legt `install-m365-dienste.py` nicht an;
  sie ist in Atlas von Hand anzulegen, wenn jemand die Prüf- und Kontingentaufgaben der Site
  erhalten soll.
- «Forms may search» an `ucc_sharepoint` und `ucc_exchange` für die Live-Prüfung der URL
  (siehe «Inhalt»).

### `scripts/install-lizenzmanagement.py`

Das Lizenzmanagement (`examples/lizenzmanagement`: Lizenzverträge, Kontingentprüfung der
Lizenzbestellung, täglicher Abgleich, Verrechnung) kommt **nach** `install-m365-dienste.py`:

```bash
python3 scripts/install-lizenzmanagement.py --cafile out/caddy-root.crt --vertrag-pruefen
```

| Option | Vorgabe | Wirkung |
|---|---|---|
| `--cafile` | – | wie oben |
| `--sku` | erste SKU von `license_sku_ids` | Lizenz, die `LzSku` für E3, E5 und F3 kennt (dieselbe wie `M365Lizenzprofil`; jede Lizenz zählt dann als E3) |
| `--ea-jahrestag` | der nächste 1. Juli | `EA_JAHRESTAG` |
| `--ea-ende` | drei Jahre nach dem Jahrestag, minus ein Tag | `EA_ENDE` |
| `--vertrag-pruefen` | – | setzt in `beispiel-m365-dienste` `LIZENZ_VERTRAG_PRUEFEN = ein`, die übrigen Konstanten bleiben |
| `--standardvertrag` | – | `LIZENZ_VERTRAG_STANDARD` (`LV-…`), der Vertrag für Bestellungen, die keinen nennen |

Es legt die Gruppen Lizenz-Tenantverantwortung, Lizenz-Chef, Lizenzmanagement, Lizenz-Finanzen
und Lizenz-Integrations-Manager an (angemeldete Testpersonen kommen hinein), importiert und
veröffentlicht Applikation, Architektur und Katalog wie oben, setzt `ATLAS_API_BASE`,
`EA_JAHRESTAG`, `EA_ENDE` und die vier Gruppenkonstanten und startet das Tenant-Kontingent
(`proc_lz_tenantbuch`, ein zweiter Start wird erkannt). Mit `--vertrag-pruefen` braucht danach
jede Lizenzbestellung einen Lizenzvertrag.

**Nach jedem Lauf von `install-m365-dienste.py` erneut ausführen** (mit `--vertrag-pruefen`):
Jenes ersetzt alle Konstanten von `beispiel-m365-dienste` und schaltet damit
`LIZENZ_VERTRAG_PRUEFEN` wieder aus.

Die Berechtigungen, die das Lizenzmanagement am Entra-Worker braucht
(`LicenseAssignment.Read.All` für `list-subscribed-skus`, `AuditLog.Read.All` für
`signInActivity`), stehen in der Vorgabe von `entra_graph_permissions`; eine Umgebung, die vor
dieser Vorgabe gebaut wurde, erhält sie mit einem `tofu apply`. Die letzte Anmeldung liefert
Graph nur in einem Tenant mit Entra ID P1 oder P2.

### `scripts/install-ad-dienste.py`

Die Active-Directory-Dienste (`examples/ad-dienste`) brauchen `enable_ad_dc` und den Worker
`ad_verzeichnis` (siehe «Inhalt», `worker_names`):

```bash
python3 scripts/install-ad-dienste.py --cafile out/caddy-root.crt
python3 scripts/install-ad-dienste.py --dry-run   # nur anpassen und zeigen, was hochginge
```

| Option | Vorgabe | Wirkung |
|---|---|---|
| `--cafile` | – | wie oben |
| `--test-ou` | `tofu output -json ad_test_ou` (`OU=AtlasTest,DC=atlastest,DC=internal`) | Basis der Tabellen und der Konstanten |
| `--upn-suffix` | Initial-Domain des Tenants | `AD_UPN_SUFFIX` |
| `--dry-run` | – | passt das Paket an und gibt Dateien, DNs der Tabellen, Konstanten und Gruppen aus; meldet sich nirgends an |
| `--tofu-dir`, `--example` | dieses Verzeichnis, `examples/ad-dienste` | woher die Ausgaben und das Paket kommen |

Was es tut:

1. Es leitet das Verzeichnis aus der Umgebung ab wie der Bootstrap: Test-OU ist die Ausgabe
   `ad_test_ou` (`OU=AtlasTest` unter dem DN von `ad_domain_name`, `ad.tf`), die Domäne deren
   `DC=`-Teil.
2. Es passt eine Kopie des Pakets an: Jedes `DC=contoso,DC=local` in den Tabellen
   `AdBerechtigungen` und `AdOe` wird zur Test-OU, etwa
   `CN=SG-FIN-Fileshare-Finanzen-Aendern,OU=Berechtigungen,OU=Gruppen,OU=AtlasTest,DC=atlastest,DC=internal`
   und `OU=FIN,OU=Personen,OU=AtlasTest,DC=atlastest,DC=internal`. Ein DN ausserhalb der
   Gesamtstruktur beantwortet der Domänencontroller mit einem LDAP-Referral, und die Bestellung
   bleibt mit einem Incident stehen. Die Fallbacks der Konstanten in den Prozessen erhalten die
   Werte dieser Umgebung. Bleibt in irgendeiner hochzuladenden Datei ein `DC=contoso` stehen,
   bricht es ab, bevor etwas hochgeladen ist.
3. Es legt die Atlas-Gruppen `M365-Mitarbeitende` (Zielgruppe), `Informationssicherheit`,
   `AD-Administration`, `Identity Management` und `Mainframe` an; angemeldete Testpersonen
   kommen hinein, wie oben.
4. Es lädt `ad-berechtigungen`, `ad-namensregel` und `ad-oe` hoch, importiert und
   veröffentlicht die Applikation `beispiel-ad-dienste` und den Katalog mit den Platzhaltern
   `zielgruppe-ad`, `informationssicherheit` und `ad-administration`.
5. Es setzt die Konstanten `AD_DOMAIN_DN` (`DC=atlastest,DC=internal`), `AD_UPN_SUFFIX`,
   `AD_PERSONEN_OU` (die Test-OU selbst), `AD_GRUPPEN_OU`, `AD_ADMIN_OU`, `AD_SERVICE_OU`,
   `AD_TEST_OU`, `AD_EXTERN_OU`, `AD_KONTAKT_OU`, `AD_FUNKTION_OU`, `AD_ROLLEN_OU`,
   `AD_SCHULUNG_OU` (`OU=Gruppen|Admins|Servicekonten|Testkonten|Extern|Kontakte|Funktionskonten|Rollenkonten|Schulungskonten,OU=Atlas,OU=AtlasTest,…`),
   `AD_IM_GRUPPE = Identity Management`, `AD_MAINFRAME_GRUPPE = Mainframe` und
   `ATLAS_API_BASE`. **Andere Konstanten der Applikation bleiben.**
6. Es schaltet am Worker `ad_verzeichnis` «Forms may search» ein (die Personenfelder der
   Formulare suchen über ihn) und sagt es im Protokoll; fehlt der Worker, sagt es das.

Die OUs und Gruppen, die Tabellen und Konstanten nennen, legt `ad-setup.ps1` an (Phase 2 beim
Aufbau): `OU=Atlas` mit den neun OUs oben, `OU=Personen` mit `FIN`, `HR` und `IT`,
`OU=Gruppen` mit `OU=Berechtigungen` (`SG-FIN-Fileshare-Finanzen-Lesen`,
`SG-FIN-Fileshare-Finanzen-Aendern`, `SG-APP-CRM-Benutzer`) und `OU=Organisation`
(`SG-ORG-FIN-Alle`, `SG-ORG-FIN-Drucker`, `SG-ORG-HR-Alle`, `SG-ORG-IT-Alle`), alles unter
`OU=AtlasTest`, wo das Bind-Konto schreiben darf. Ein Domänencontroller, der vorher gebaut
wurde, erhält sie mit Phase 3 (idempotent; eine Änderung des Skripts ersetzt die VM nicht,
`ignore_changes = [custom_data]`):

```bash
env=$(tofu output -raw environment_id)
az vm run-command invoke -g "$env-rg" -n "$env-dc" --command-id RunPowerShellScript \
  --scripts @scripts/ad-setup.ps1 --parameters Phase=3
```

Wer andere Gruppen oder OUs in den Tabellen will, ändert Tabellen und `ad-setup.ps1`
gemeinsam; die Offline-Prüfung des Umschreibens hält beide zusammen:

```bash
python3 -m unittest discover -s scripts -p 'test_install_ad_dienste.py' -v
```

### `scripts/install-informationstransfer.py`

Der Informationstransfer (`examples/informationstransfer`) braucht `enable_ad_dc`, den Worker
`ad_verzeichnis` und je nach Postfachsystem `ucc_exchange_onprem` (Exchange Server) oder
`ucc_exchange`, dazu `ucc_entra` und für die Telefonnummer `ucc_teams`. Am besten nach
`install-ad-dienste.py`, dessen Gruppen unter `OU=Gruppen,OU=Atlas` die Übertragung der
Berechtigungen kopiert:

```bash
python3 scripts/install-informationstransfer.py --cafile out/caddy-root.crt
python3 scripts/install-informationstransfer.py --dry-run   # nur anpassen und zeigen, was hochginge
```

| Option | Vorgabe | Wirkung |
|---|---|---|
| `--cafile` | – | wie oben |
| `--test-ou` | `tofu output -json ad_test_ou` | `AD_PERSONEN_OU`; `AD_GRUPPEN_OU` ist `OU=Gruppen,OU=Atlas,<Test-OU>` |
| `--postfach` | `exchange-server`, wenn `exchange_onprem` gebaut ist, sonst `exchange-online` | `POSTFACH_SYSTEM` |
| `--dry-run` | – | passt das Paket an und gibt Dateien, Konstanten und Gruppen aus; meldet sich nirgends an |
| `--tofu-dir`, `--example` | dieses Verzeichnis, `examples/informationstransfer` | woher die Ausgaben und das Paket kommen |

Es legt die Atlas-Gruppen `Identity Management` (Zielgruppe und Genehmigung),
`Informationssicherheit`, `M365-Administration` und `M365-Telefonie` an, lädt `it-optionen` hoch,
importiert und veröffentlicht `beispiel-informationstransfer` mit dem Katalog, setzt die
Konstanten (`NAME_PREFIX` = Präfix dieser Umgebung; **andere Konstanten bleiben**) und schaltet am
Worker `ad_verzeichnis` «Forms may search» ein.

**`ENTRA_TRANSFER` bleibt `aus`.** Diese Umgebung hat kein Entra Connect: Der Hard Match über
`onPremisesImmutableId` kann sich nicht vollenden, und der Transfer löscht ein Cloud-Konto
endgültig. Wer ihn trotzdem durchspielt, setzt die Konstante in der Console für einen Test von
Hand auf `ein` und danach zurück; die Freigabe der Informationssicherheit steht vor jeder Löschung,
und mit `NAME_PREFIX` findet der Prozess nur die Testkonten dieser Umgebung. Der Entra-Worker
braucht dafür `User.DeleteRestore.All` und für die Cloud-Gruppen `Directory.Read.All`, die nicht in
der Vorgabe von `entra_graph_permissions` stehen.

```bash
python3 -m unittest discover -s scripts -p 'test_install_informationstransfer.py' -v
```

## Geteilter Tenant (`shared_tenant = true`)

Für einen Tenant, der auch produktiv genutzt wird, etwa den eigenen. Anwendungsberechtigungen
aus Entra gelten immer tenantweit. Deshalb erhalten die Apps, deren Rechte jedes Postfach
erreichen, in Entra **keine** solchen Rechte, sondern eingegrenzte Rechte in Exchange und
SharePoint:

| Worker | Ohne Schalter | Mit `shared_tenant = true` |
|---|---|---|
| `mail` | `Mail.Send` in Entra (jedes Postfach) | kein `Mail.Send` in Entra; Exchange **RBAC for Applications**: «Application Mail.Send» nur im Management Scope |
| `exchange` | Rolle *Exchange Administrator* | keine Verzeichnisrolle; Mitglied einer Exchange-Rollengruppe (*Mail Recipients*, *Mail Recipient Creation*, *Distribution Groups*, *Security Group Creation and Membership*, *Legal Hold*, *View-Only Recipients*) mit **Schreibbereich** = Management Scope |
| `sharepoint` | `Sites.ReadWrite.All` | `Sites.Selected` mit Schreibrecht **nur** auf der Test-Website; mit `sharepoint_read_all_sites = true` zusätzlich `Sites.Read.All` (liest jede Site des Tenants) |
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

Das Skript hat zwei Teile, die `-Part Exchange` oder `-Part SharePoint` einzeln wiederholt
(Vorgabe `All`):

- **Exchange:** prüft `IsDehydrated`, legt den Management Scope an, gibt der Mail-App
  «Application Mail.Send» in diesem Scope und nimmt die Exchange-App in die Rollengruppe mit
  Schreibbereich auf; fehlende Rollen der Rollengruppe ergänzt es.
- **SharePoint:** macht die angemeldete Person kurz zur Besitzerin der Testgruppe (im
  `finally` wieder entfernt), gibt der SharePoint-App `write` auf der Test-Website
  (`Sites.Selected`), legt die Liste «Site-Inventar» für die SharePoint-Teamwebsite des
  Katalogs an und gibt den Wert für `install-m365-dienste.py --site-inventory` aus.

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
- **Daten:** `/var/lib/atlas` (`--data-dir`), Umgebung in `/etc/atlas/atlas.env`, Programm
  `/usr/local/bin/atlas`.
- **Automatische Abschaltung** täglich um `atlas_auto_shutdown_time` (Vorgabe 19:00 UTC).
  Wieder starten mit `az vm start -g <ressourcengruppe> -n <vm>` oder im Azure-Portal.

### Atlas auf der VM aktualisieren

`atlas-setup.sh` installiert Atlas nur, solange `/usr/local/bin/atlas` fehlt. Ein geändertes
`atlas_version`, `atlas_install` oder `atlas_source_ref` ändert die Cloud-Init-Daten, und ein
`tofu apply` **ersetzt** die VM samt allen Daten. Eine laufende VM erhält eine neue Version
deshalb von Hand: lokal bauen (oder das Archiv eines Releases laden), kopieren, das bisherige
Programm aufheben, ersetzen, neu starten.

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o /tmp/atlas-linux ../../../cmd/atlas
```

```bash
ssh_cmd="$(tofu output -raw atlas_ssh)"
key="$(awk '{print $3}' <<<"$ssh_cmd")"; host="$(awk '{print $4}' <<<"$ssh_cmd")"
scp -i "$key" /tmp/atlas-linux "$host:/tmp/atlas"
```

```bash
$ssh_cmd 'sudo cp -p /usr/local/bin/atlas /usr/local/bin/atlas.vorher && sudo install -m 0755 /tmp/atlas /usr/local/bin/atlas && sudo systemctl restart atlas && /usr/local/bin/atlas version'
```

Die Daten unter `/var/lib/atlas` bleiben; die überwachten Worker starten mit dem neuen Programm.
Zurück geht es mit `atlas.vorher`. Danach mit `./scripts/wait-atlas.sh` warten (prüft
`/healthz`) und bei Bedarf die Beispiele erneut einspielen. Der Exchange-Worker auf `exch01`
hat ein eigenes `atlas.exe`; ihn aktualisiert `onprem/install-worker.sh`.

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

- Abschaltung täglich um `ad_auto_shutdown_time` (Vorgabe 19:00 UTC).

Ohne Domänencontroller legt der Bootstrap keinen AD-Worker an; der Server erhält
`ATLAS_AD_MOCK=1` in `/etc/atlas/atlas.env` (ADR-0181).

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

```bash
./scripts/wait-exchange.sh
```

```bash
./onprem/install-worker.sh
```

```bash
python3 -I onprem/run-smoke.py
```

`run-smoke.py` deployt den Rauchtest als Administrator, startet ihn für eine Testperson mit
Postfach auf `exch01` (Vorgabe: die erste, sonst `--person <upn>`), zeigt jeden Schritt und den
Incident, an dem die Instanz steht, und endet mit 0, wenn sie abgeschlossen ist.
`install-worker.sh` und `run-smoke.py` kennen kein `--cafile`; sie brauchen ein Zertifikat, dem
Python vertraut (`atlas_tls = "letsencrypt"` oder Caddys Stammzertifikat im Systemspeicher).

Das Worker-Token gilt **7 Tage**. Danach, und nach jeder Änderung am Exchange-Worker,
`install-worker.sh` erneut ausführen; es baut `atlas.exe` aus dem Checkout, in dem es liegt
(`ATLAS_REPO=<checkout>` baut aus einem anderen, etwa einem Branch mit einer Korrektur).

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
  löst die Identität jetzt über `Get-Recipient` auf (`connector/exchange/lookup.go`, inzwischen
  auf `main`).
- Atlas verteilte Exchange-Jobs nur nach Jobtyp, nicht nach Worker-Name: Der Online-Worker
  `ucc_exchange` übernahm einen Job für `ucc_exchange_onprem` und scheiterte mit «no worker
  registered». Behoben in PR #1523 (ein Worker erhält nur noch Jobs der Namen, die er meldet);
  mit diesem Stand lief der Rauchtest bei laufendem Online-Worker durch. Auf einem Atlas-Server
  ohne diese Korrektur den Online-Worker für den Rauchtest kurz ausschalten (Console → Workers,
  `ucc_exchange`).
- Der Runner startet `atlas.exe` über `cmd.exe`: Windows PowerShell 5.1 bricht sonst mit
  `ErrorActionPreference Stop` an der ersten Protokollzeile auf stderr ab.

Eine laufende Umgebung erhält den Server, ohne dass die Atlas-VM ersetzt wird, gezielt:
`tofu apply -target=azurerm_virtual_machine_extension.exchange_setup
-target=azurerm_dev_test_global_vm_shutdown_schedule.exchange
-target=azurerm_network_interface_security_group_association.exchange`.


## SharePoint Server (Worker)

Baut die Variable `enable_sharepoint_server` eine Farm, startet `onprem/install-sharepoint-worker.sh`
den Worker `ucc_sharepoint_onprem` (Typ `sharepointserver`, ADR-0540) auf dem Server
selbst, mit einem Token für 7 Tage wie beim Exchange-Worker. Das Skript liest
`tofu output -json sharepoint_server`; das Passwort des Dienstkontos (`runbook_account`) kommt aus der
Ausgabe `sharepoint_runbook_password` oder aus `SHAREPOINT_WORKER_PASSWORD`. Danach:

```bash
./onprem/install-sharepoint-worker.sh
python3 scripts/install-m365-dienste.py --cafile out/caddy-root.crt
```

Das Installationsskript nimmt das Produkt «Teamwebsite» nur in den Katalog `cat-m365-onprem` auf, wenn
`sharepoint_server.enabled` wahr ist (`--no-sharepoint-server` lässt es weg); ohne Exchange Server entsteht
der Katalog dann nur mit diesem Produkt. In Atlas braucht der Worker kein Zugangsdatum, weil die Cmdlets als
Dienstkonto laufen.

**Nicht geprüft:** Weder `install-sharepoint-worker.sh` noch `sharepoint-worker.ps1` noch das Produkt sind
an einer Farm gelaufen. Die Cmdlets stammen aus der Dokumentation von Microsoft, die nur Versionen bis 2019
nennt. Nicht an einem Server bestätigt sind `CreateDefaultAssociatedGroups`, `EnsureUser` für AD-Gruppen und
das Laden der Befehle (`Add-PSSnapin` oder `Import-Module SharePointServer`) unter Subscription Edition. Eine
Ausgabe `sharepoint_runbook_password` gibt es im Zweig `feat/devenv-sharepoint-server` noch nicht.
## SharePoint Server (optional)

Mit `enable_sharepoint_server = true` (braucht `enable_ad_dc`) entsteht eine SharePoint Server
Subscription Edition als Einzelserver-Farm:

- VM `<umgebung>-sp` (`sharepoint_server_vm_size`, Vorgabe `Standard_D4s_v5` mit 4 Kernen und
  16 GB, dem Minimum von Microsoft für diese Rolle; 256-GB-Platte), Azure-Image
  `MicrosoftSharePoint:MicrosoftSharePointServer:spse`, Name `sp01.<ad_domain_name>`, interne
  Adresse `10.42.0.12`, DNS über den Domänencontroller. Von aussen erreichbar ist sie nicht,
  ausser mit `ad_allow_rdp` per RDP für `ad_allowed_cidrs`; die öffentliche Adresse dient den
  Downloads.
- `scripts/sharepoint-setup.ps1` installiert unbeaufsichtigt in vier Phasen mit Neustarts:
  Domänenbeitritt; Prüfung des Images und AD-PowerShell; die Konten `svc-sp-farm-<zufall>`,
  `svc-sp-app-<zufall>` und `svc-sp-run-<zufall>` in `OU=AtlasTest` und SQL Server 2022
  Developer vom Download bei Microsoft; zuletzt die Farm (`New-SPConfigurationDatabase`,
  Rolle `SingleServerFarm`), die Zentraladministration auf Port 9999, die Webanwendung
  `http://sp01.<ad_domain_name>` mit einer Websitesammlung an der Wurzel und die Rechte des
  Runbook-Kontos (lokaler Administrator, `Add-SPShellAdmin`, zweiter Besitzer der
  Websitesammlung). Die Passwörter erzeugt OpenTofu (`random_password.sharepoint`); sie stehen
  in keinem Protokoll.
- `scripts/wait-sharepoint.sh` verfolgt die Installation über den Run Command von Azure (kein
  eingehender Port nötig, `WAIT_MINUTES`, Vorgabe 240) und zeigt bei einem Fehler das Ende des
  Protokolls (`C:\AtlasSetup\sharepoint-phase<N>.log`).
- `tofu output sharepoint_server` nennt VM, Webanwendung und Runbook-Konto, aber kein Passwort.

**180 Tage ohne Produktschlüssel.** SharePoint läuft als Testversion und braucht keinen
Produktschlüssel; nach 180 Tagen ist die Farm abgelaufen. Dann die VM neu aufbauen oder einen
Schlüssel eintragen. SQL Server Developer ist nur für Entwicklung und Tests lizenziert.

**Automatische Abschaltung.** Die VM stoppt täglich zur Zeit von `ad_auto_shutdown_time` in UTC,
zusammen mit dem Domänencontroller, ohne den sie nicht arbeitet. Die Variable darf deshalb nicht
leer sein, sonst lehnt der Plan die VM ab. Trifft die Abschaltung eine laufende Installation,
setzt die Phase beim nächsten Start neu an; dazu auch den Domänencontroller starten.

**Nur gezielt anwenden.** Diese Umgebung darf für den SharePoint Server ausschliesslich mit
`-target` auf genau diese Ressourcen angewendet werden. Ein Apply ohne `-target` kann die
bestehenden VMs `atlas`, `dc` und `exch` verändern:

```bash
tofu apply \
  -target=terraform_data.sharepoint_guard \
  -target=random_password.sharepoint \
  -target=azurerm_network_security_group.sharepoint \
  -target=azurerm_public_ip.sharepoint \
  -target=azurerm_network_interface.sharepoint \
  -target=azurerm_network_interface_security_group_association.sharepoint \
  -target=azurerm_windows_virtual_machine.sharepoint \
  -target=azurerm_virtual_machine_extension.sharepoint_setup \
  -target=azurerm_dev_test_global_vm_shutdown_schedule.sharepoint
```

Der Plan dazu zeigt auch die Abhängigkeiten (Ressourcengruppe, Subnetz, Domänencontroller); bei
diesen darf keine Änderung stehen. Eine spätere Änderung am Setup-Skript ersetzt die VM nicht und
startet die Erweiterung nicht erneut.

Das Image verlangt nach allem, was öffentlich dazu zu finden ist, keinen Marketplace-Plan. Vor
dem ersten Apply lässt sich das prüfen; `null` heisst, dass kein Plan nötig ist:
`az vm image show --urn MicrosoftSharePoint:MicrosoftSharePointServer:spse:1.0.0 --query plan`.

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

Der optionale Exchange Server (`Standard_D4s_v5`, Windows, 256-GB-Platte) ist nicht
geschätzt; er hat doppelt so viele Kerne und doppelt so viel Speicher wie der Domänencontroller.
Dasselbe gilt für den optionalen SharePoint Server (gleiche Grösse und Platte): Die VM kostet nur
im laufenden Zustand, Platte und öffentliche IP-Adresse auch abgeschaltet; dazu kommt keine
Lizenz für SharePoint (180-Tage-Testversion) oder SQL Server Developer.

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
  bleibt der Entra-Worker tenantweit (siehe «Geteilter Tenant»). Ebenfalls tenantweit, auch
  im geteilten Tenant: `sharepoint_read_all_sites` (liest jede Site) und
  `sharepoint_admin_quota` (Vollzugriff auf jede Site); beide sind deshalb aus.
- **Netz:** SSH und (mit `atlas_tls = "internal"`) HTTPS nur für `atlas_allowed_cidrs`.
  `atlas_tls = "letsencrypt"` öffnet 80 und 443 für das ganze Internet, weil Let's Encrypt
  von nicht veröffentlichten Adressen prüft; die Anmeldung bleibt Pflicht. Ob für
  `*.cloudapp.azure.com` die Ratenlimits von Let's Encrypt greifen, ist nicht geprüft.
- **Zugangsdaten auf den VMs:** Der Server erhält sie über Cloud-Init (nur für root
  lesbar, nach dem Bootstrap gelöscht); der Domänencontroller über die geschützten
  Einstellungen der Custom Script Extension. Für eine Testumgebung vertretbar, für
  Produktion nicht.

## Skripte im Überblick

| Skript | Wer startet es | Wofür |
|---|---|---|
| `scripts/atlas-setup.sh` | Cloud-Init beim ersten Start der Atlas-VM | Pakete, Vertrauen in das LDAPS-Zertifikat, Atlas, Dienste, Bootstrap; Protokoll `/var/log/atlas-setup.log` |
| `scripts/atlas-bootstrap.py` | `atlas-setup.sh` | Worker, Vault-Bundles und API-Token `atlas` in der Console; «Forms may search» am Entra-Worker |
| `scripts/assign-licenses.sh` | OpenTofu (`license_sku_ids`) | Lizenzen der Testpersonen über `az rest` |
| `scripts/ad-setup.ps1` | Custom Script Extension des Domänencontrollers | AD DS, `OU=AtlasService` mit Bind-Konto, `OU=AtlasTest` mit Testpersonen und Testgruppe, LDAPS-Zertifikat |
| `scripts/exchange-setup.ps1` | Custom Script Extension von `exch01` | Exchange Server SE in vier Phasen, Dienstkonto, Postfächer der Testpersonen |
| `scripts/sharepoint-setup.ps1` | Custom Script Extension von `sp01` | Domänenbeitritt, Konten in `OU=AtlasTest`, SQL Server Developer, SharePoint-Farm mit Webanwendung und Websitesammlung |
| `scripts/wait-sharepoint.sh` | von Hand | verfolgt die Installation auf `sp01` über den Run Command (`WAIT_MINUTES`, Vorgabe 240) |
| `scripts/wait-atlas.sh` | von Hand nach `tofu apply` | wartet auf `/healthz` und `/var/lib/atlas-setup.done` (`WAIT_MINUTES`, Vorgabe 30); Exit 1 mit Protokollauszug bei Zeitüberschreitung |
| `scripts/wait-ad.sh` | von Hand | wartet auf LDAPS mit dem Zertifikat aus `out/` und, mit `ldapwhoami`, auf die Anmeldung des Bind-Kontos (Passwort über eine Datei) |
| `scripts/wait-exchange.sh` | von Hand | verfolgt die Installation auf `exch01` über den Run Command (`WAIT_MINUTES`, Vorgabe 150) |
| `onprem/install-worker.sh` | von Hand nach `wait-exchange.sh` | startet `ucc_exchange_onprem` auf `exch01` (Token 7 Tage) |
| `onprem/exchange-worker.ps1` | `install-worker.sh` über den Run Command | lädt `atlas.exe`, schreibt `C:\Atlas\worker.json`, Starttask als Dienstkonto |
| `onprem/install-sharepoint-worker.sh` | von Hand nach dem Bau der Farm | startet `ucc_sharepoint_onprem` auf dem SharePoint-Server (Token 7 Tage) |
| `onprem/sharepoint-worker.ps1` | `install-sharepoint-worker.sh` über den Run Command | wie der Exchange-Worker, Typ `sharepointserver` |
| `onprem/run-smoke.py` | von Hand | Rauchtest `exchange-onprem-smoke.bpmn` gegen `exch01` |
| `scripts/shared-tenant-setup.ps1` | von Hand nach `tofu apply`, geteilter Tenant | Rechte in Exchange und SharePoint, Liste «Site-Inventar» (`-Part`) |
| `scripts/install-m365-dienste.py` | von Hand | `examples/m365-dienste` und seine Exchange-Server-Variante einspielen |
| `scripts/install-lizenzmanagement.py` | von Hand, nach `install-m365-dienste.py` | `examples/lizenzmanagement` einspielen |
| `scripts/purge-deleted.sh` | von Hand nach `tofu destroy` | Papierkorb von Entra nach Präfix leeren (erst auflisten, mit `--yes` löschen) |
| `scripts/emergency-cleanup.sh` | von Hand, ohne State | Ressourcengruppen, gelöschte Key Vaults, App-Registrierungen, Benutzer und Gruppen nach Präfix |
| `scripts/shared-tenant-cleanup.ps1` | von Hand, geteilter Tenant | Rollenzuweisungen, Rollengruppen, Dienstprinzipale und Scopes in Exchange Online nach Präfix (`-Yes` löscht) |

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
- Ebenfalls am 2026-11-03 geprüft (Einzelheiten in
  [`examples/m365-dienste/README.md`](../../../examples/m365-dienste/README.md), «Was geprüft
  ist und was nicht»): Sitzungszimmer bestellen und zurückgeben (noch ohne Raumliste),
  Partnerzugang über den öffentlichen Link, Governance-Prüfung von Hand gestartet.
- Ungetestet: Raumliste und Ortsangaben, Lizenz wechseln (der Testtenant hat nur eine SKU),
  das Lizenzmanagement gegen den Tenant, `set-site-quota`, die übrigen Aktionen an
  Bestellungen, Teams Phone.

## Bekannte Grenzen

- **Einmal gegen einen echten Tenant und ein echtes Azure-Abonnement gelaufen**
  (2026-11-02, geteilter Tenant, OpenTofu 1.13.2, `azuread` 3.10.0): `atlas-setup.sh`,
  `ad-setup.ps1`, Cloud-Init mit gzip, die Lizenzzuweisung über `az rest`, die Warteskripte
  und `shared-tenant-setup.ps1` laufen nach den Korrekturen im Abschnitt «Erfahrungen mit
  einem echten Tenant». Ein zweiter Durchlauf von Grund auf mit diesen Korrekturen steht aus.
- **Exchange Server (on-premises)** ist optional (`enable_exchange_onprem`, Abschnitt
  «Exchange Server (optional)») und einmal am 2026-11-03 installiert und mit dem Rauchtest geprüft.
- **SharePoint Server** ist optional und noch nie gegen Azure gelaufen: Die OpenTofu-Logik ist
  nur mit gemockten Providern geprüft, `sharepoint-setup.ps1` nur auf seine Syntax.
- **Teams Phone:** Rufnummern, Calling Plans und Direct Routing richtet das Projekt nicht
  ein. Ob `Connect-MicrosoftTeams` über `User.ReadWrite.All` hinaus Graph-Berechtigungen
  verlangt, ist nicht geprüft (`teamsphone_graph_permissions`).
- **Ein Team** legt erst der Entra-Worker an (`create-team`).
- **Nicht im Katalog dieser Umgebung:** Viva-Engage-Community, Aufbewahrung (Litigation Hold)
  und die hybride Identität (siehe «Beispiele einspielen»); ohne Teams-Phone-Worker auch die
  Telefonie-Produkte.
- **Sitzungszimmer in der Raumsuche:** Raumlisten und Ortsangaben erscheinen in Outlook laut
  Microsoft erst nach 24 bis 48 Stunden. `Set-Place` gibt es nur in Exchange Online; die
  Exchange-Server-Variante setzt `ORTSANGABEN = false` und führt nur die Raumliste.
- **Gruppenmitgliedschaften** ignoriert OpenTofu nach dem ersten Anlegen, damit die
  Worker sie ändern dürfen.

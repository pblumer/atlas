# Offene produktseitige Punkte für einen Einsatz in der Bundesverwaltung

**Aktualisiert: 2026-10-24.** Arbeitsliste zum [ISDS-Konzept](isds-konzept.md). Sie enthält **nur, was am
Produkt selbst zu tun ist** — betriebs- und projektseitige Aufgaben stehen als
Massnahmen M-01…M-19 im Konzept, Kapitel 6.4.

Jeder Punkt nennt das Restrisiko, das er schliesst, den heutigen Stand mit
Quelle, und was fehlt. Die Priorität ist aus der Sicht einer Einführung im Bund
vergeben, nicht aus der Produktroadmap:

- **P1 — Blocker**: ohne diesen Punkt ist ein Produktivbetrieb mit erhöhtem
  Schutzbedarf nicht vertretbar; für «normal» braucht es eine
  Ausnahmebewilligung plus die kompensierenden Massnahmen aus Kapitel 6.4.
- **P2 — vor breiterem Rollout**: mit Kompensation tragbar, aber jede
  Kompensation ist organisatorisch und damit fehleranfällig.
- **P3 — Reifegrad**: erhöht die Prüfbarkeit und senkt den Aufwand jeder
  künftigen ISDS-Prüfung.

> **O-07, O-03 und O-04 sind ausgearbeitet** im
> [Zugriffsschutz-Konzept](zugriffsschutz-konzept.md): Schnittstelleninventar,
> der `/mcp`-Befund im Detail, acht Massnahmen mit Aufwand und ein Stufenplan bis
> zur technischen Grundlage eines PoC. O-01 und O-02 wurden später teilweise
> implementiert und werden im aktualisierten Konzept mit ADR-0475 weitergeführt.

| Nr. | Punkt | Prio | Risiko | Stand heute | Was fehlt |
|-----|-------|------|--------|-------------|-----------|
| O-01 | **Föderierte Authentisierung** (OIDC/SAML, Ziel eIAM) | P1 | R-03 | Lokale Passwörter (bcrypt, Session-Cookie) **und**, wo eine Betreiberin einen Anbieter konfiguriert, OpenID Connect. `Source`/`ExternalID` tragen die Verknüpfung zum Subjekt des Anbieters; die Naht ist weiterhin eine einzige — eine föderierte Anmeldung endet in derselben Session wie eine lokale. Die vier Rollen aus [ADR-0209](../adr/0209-roles-per-endpoint-group.md), auf die Claims abgebildet werden, existieren seit M9 | **Beide Schritte sind gebaut** (M12, [`ADR-0210`](../adr/0210-federated-authentication.md)): Atlas ist OIDC-Relying-Party — Anmeldefluss mit PKCE, Prüfung des ID-Tokens gegen die JWKS des Anbieters, Kontoanlage beim ersten Login, Schaltfläche auf der Anmeldemaske — und eine Administratorin kann einen Claim auf Rollen und Gruppen abbilden (`/api/v1/settings/oidc-mapping`, Console → Administration → Organization → Single sign-on). Die Abbildung ist aus, bis jemand sie einschaltet; ein Claim vergibt nur durch Treffer, nie durch Abwesenheit, und `user` bleibt ein Boden. Ohne konfigurierten Anbieter ändert sich nichts. Offen: die vom Anbieter ausgelöste Abmeldung, SAML falls eIAM es verlangt, und die Doku für den Betrieb hinter einem authentisierenden Proxy |
| O-02 | **Objekt- und aktionsbezogene Autorisierung** | P1 | R-04/R-09 | Endpoint-Rollen `admin`, `modeler`, `operator`, `productmanager`, `feedreader`, `user` (ADR-0209), Projekt-/Worker-Freigaben (ADR-0071/0205), eingeschränkte Variablen- und Task-Sichtbarkeit (ADR-0275) und erste Policy-/Delegationskette für SharePoint (ADR-0475). Policy-Entscheid und Freigaben liegen als dauerhafte Fakten vor; der Modus `shadow` ist **keine** Durchsetzung | Andere instanzbezogene Lesewege, fachliche Objektgrenzen und Worker-Typen systematisch autorisieren; `service/enforce` bleibt kooperativ, da das Provider-Credential beim Worker liegt; Delegation nicht auf alle Worker übertragen, solange kein passender PEP existiert. In PROD Altrollen rezertifizieren |
| O-03 | **Sicherheits-Audit-Log und Aufbewahrung** | P1 | R-13 | Stabile `auth.*`-Einträge (ADR-0197); hinzugekommen sind dauerhafte Policy-Entscheide und Action-Approvals im WAL/State (ADR-0475), sowie ein gesondertes diagnostisches Event-Log für Nachrichten und Signale mit TTL (ADR-0472) | Zentrale Weiterleitung, Alarmierung, Retention und Zugriff auf jedes Nachweisformat regeln. HTTP-Access-Log braucht eine Proxy- oder gleichwertige Ingress-Lösung; atlas selbst erzeugt kein vollständiges HTTP-Access-Log. Auth-/Worker-/Secret-/Backup-Änderungen und Provideraktionen auf vollständige Audit-Abdeckung prüfen; Event-Log ist kein SIEM |
| O-04 | **Anmelde-Härtung** | P1 | R-03, R-12 | **Erledigt, soweit ohne Föderation sinnvoll** (ADR-0197): Drosselung je Absenderadresse *und* je Konto vor dem Kontolookup, ohne Enumerationssignal; erfolgreiche Anmeldung setzt das Kontobudget zurück; jede Verweigerung protokolliert. Mindestlänge weiterhin 8 Zeichen | Konfigurierbare Passwortregeln; MFA — beides sinnvoller über O-01 (Föderation) als über lokale Passwörter |
| O-05 | **Löschnachweis über die ganze Kette** | P1 | R-06 | Retention und WAL-Kompaktierung (ADR-0115/0144/0131); für **deklarierte** Personendaten ermöglicht ADR-0314 kryptografische Löschung über den Schlüssel der betroffenen Person mit `personal_data.erased`-Audit-Eintrag. Nicht deklarierte Daten bleiben davon unberührt | Aufbewahrungs-/Kompaktierungsfristen, Export, Backups und neu Autorisierungs-/Approval-Fakten sowie explizite Attachment-Variablen einbeziehen; pro Datenkategorie den Lösch- und Auskunftsnachweis führen und juristisch prüfen, ob Schlüsselvernichtung genügt |
| O-06 | **Umfassende Verschlüsselung ruhender Daten** | P2 | R-05 | Vault-Secrets sind mit AES-256-GCM geschützt (ADR-0069/0070). **Deklarierte Personendaten** werden mit einem personenbezogenen Schlüssel chiffriert und sind kryptografisch löschbar (ADR-0314); übrige WAL-/State-/Sidecar-Daten bleiben im Klartext | Datenträgerverschlüsselung, Dateirechte, Schlüssel- und Restore-Konzept nachweisbar vorgeben; bei erhöhtem Schutzbedarf prüfen, ob weitere datenfamilienbezogene Verschlüsselung nötig ist; deklarationsfreie Felder ausdrücklich testen |
| O-07 | ~~**Authentisierung für `/mcp` und `/metrics`**~~ | — | R-08 | **Erledigt.** `/mcp` ist transportseitig durch `--auth` geschützt und ein Werkzeugaufruf handelt mit dem Credential des Aufrufers (ADR-0196); `/metrics` verlangt ein API-Token mit Geltungsbereich `metrics` (ADR-0198). Welche Route offen ist, ist deklariert und per Test gegen eine ausgeschriebene Liste gehalten (ADR-0199) | — |
| O-08 | **Lieferkettennachweis** | P2 | R-11 | Releases mit `SHA256SUMS`; CGO-frei, `-trimpath`; CI prüft Build, Vet, Format, Race, 95 % Coverage — aber **keine** Signatur, **kein** SBOM, **keine** Schwachstellenprüfung der Abhängigkeiten | Release-Signierung (z. B. cosign), SBOM (CycloneDX/SPDX) je Release, `govulncheck` und ein SAST-Lauf in der CI, dokumentierter reproduzierbarer Build |
| O-09 | **Mandanten-/Klassifizierungstrennung** | P2 | Kap. 4 | Eine Installation = ein Datenverzeichnis, ein Benutzerverzeichnis, ein Autorisierungsmodell | Trennung von Fachbereichen innerhalb einer Installation, oder eine ausdrückliche Produktaussage «eine Installation je Schutzbedarfsklasse» samt Betriebsmuster |
| O-10 | **Hochverfügbarkeit** | P2 | R-07 | Single-Writer, ein Prozess pro Datenverzeichnis; Replikation ist als ADR-0175 **Proposed** beschrieben, WAL-Replikation steht offen auf der Roadmap | Umsetzung der replizierten Partitionszellen oder ein dokumentiertes, getestetes Aktiv/Passiv-Muster mit Fencing (damit nie zwei Prozesse dasselbe Verzeichnis öffnen) |
| O-11 | **Produktreife 1.0** | P2 | R-01 | `0.x`: On-Disk-Format instabil, kein Downgrade, Sicherheitsfixes nur im nächsten Release, kein LTS (`SECURITY.md`, `docs/install.md`) | Stabilitätszusage für On-Disk-Format und API, Migrationspfad zwischen Releases, veröffentlichte Sicherheits-Support-Policy mit Reaktionszeiten |
| O-12 | **Archivierungsschnittstelle** | P3 | Kap. 9 | Export über API, Instanz-Snapshots (ADR-0109), Backup (ADR-0107), OpenSearch (ADR-0114) — alles generisch, nichts BAR-spezifisch | Ein Export, der Geschäftsfälle in einem archivtauglichen, selbstbeschreibenden Format ablegt (Metadaten, Prüfsummen, Ablieferungseinheit) |
| O-13 | **Schlüsselrotation im Vault** | P3 | 6.2 | `keyId` ist vorhanden und erkennt einen Schlüsselwechsel; die Rotation selbst ist als Folgearbeit dokumentiert (ADR-0069) | Ein Rotationslauf (Neuversiegeln unter neuem Schlüssel) inkl. Betriebsanleitung |
| O-14 | **Durable Sessions und Sitzungsverwaltung** | P3 | R-12 | Sessions nur im Arbeitsspeicher, 12 h fest, Neustart meldet alle ab (ADR-0044) | Konfigurierbare Lebensdauer, optionale Persistenz, Übersicht und gezieltes Beenden aktiver Sitzungen (auch als Reaktionsmittel bei einem Vorfall) |
| O-15 | **Konfigurations-Check gegen die Vorgaben** | P3 | alle | Alle Einstellungen sind in `docs/install.md` dokumentiert, die Prüfung ist manuell | Ein `atlas check`-Lauf, der die sicherheitsrelevante Konfiguration bewertet (`--auth`, Vault-Schlüsselquelle, offene Endpunkte, Skriptsprachen, Retention) — macht die jährliche Prüfung nach Kapitel 8.3 reproduzierbar |
| O-16 | **Policy-Durchsetzung auf weitere Worker erweitern** | P1 | R-04/R-09 | ADR-0475 ist **Partial/Proposed**; Microsoft-Kontoverknüpfung, versionierte Policy, PAP/PDP/PEP, Signatur/Fingerprint, Freigabe und Broker für beaufsichtigte SharePoint-Worker sind implementiert; externem Worker wird kein Token vermittelt | Per Worker-Typ festlegen, ob `service`, `shadow`, `enforce` oder echte Delegation zulässig ist; Egress-/Providerrechte, externe Worker, dynamische Namen, Credential-Isolation und Fail-closed-Tests abarbeiten, bevor breitere Sicherheitseigenschaften behauptet werden |
| O-17 | **Identitäts- und Genehmigungsdaten steuern** | P2 | R-03/R-06/R-13 | Linked Microsoft Accounts und reservierte Vault-Credentials; Entscheidungen/Freigaben werden persistent und im Replay rekonstruiert (ADR-0475). Console erlaubt eigene Genehmigungen (PR #1329) | Verzeichnis der Bearbeitungen, Aufbewahrung und Zugang der Genehmigungsdaten, Reauth/Austritt, Session-Entzug, Refresh-Token-Rotation, Notfallverfahren und Provider-API-Scope pro Installation abnehmen |
| O-18 | **WAL-arme Dokument- und Formularflüsse** | P1 | R-06 | Remote Entra-Form-Auswahl nutzt nicht-durable Worker-Queries; nur die gewählte ID wird Prozessvariable (ADR-0473). Der Mail-Worker kann Anhänge prüfen und direkt nach S3 exportieren, aber `get-attachment` speichert Base64 explizit als Variable (ADR-0478) | Modellierungsregeln und Negativtests für WAL, Snapshots, Exporte, sensible Metadaten, ClamAV-Limits und Berechtigungen. Paperless-ngx nach ADR-0479 ist **nur geplant**, NATEL go (PR #1333) nur ein experimentelles Mock-Fundament, kein registrierter produktiver Worker |
| O-19 | **Release-Architektur als Nachweis abnehmen** | P2 | R-04/R-11 | Beim Publish/Import werden logische und physische Snapshots mit dem Release abgelegt; SVG/ArchiMate-Export, Endpunkt-Redaktion und workerberichtete Endpunkte vorhanden (ADR-0471, S1/S2/S2b) | Mit Firewall-/Provider-Inventar und Betriebstopologie abgleichen; Zugriff auf Endpunkte und Instanzdaten prüfen; kein Ersatz für aktive Netzwerkerkennung. Confluence-Veröffentlichung (S3/S4) ist noch nicht umgesetzt |
| O-20 | **Echte Provider-Abnahmetests** | P2 | R-04/R-09/R-11 | Viele Worker besitzen Mock-/Integrationstests. Exchange, Teams Phone, Google Contacts und neue Mail-/SharePoint-Operationen haben teils explizit ungeprüfte Annahmen zu Providerverhalten und Tenantrechten | Pro Provider und Auth-Methode reale Sandbox-/Tenant-Tests, Scope-Review, Retry/Idempotenz, Quoten und Fehlerpfade dokumentieren; nur getestete Operationen in den konkreten Schutzobjekt-Geltungsbereich aufnehmen |

## Reihenfolge-Vorschlag (Stand 2026-10-24)

1. **Vor einem schutzwürdigen PoC:** O-05, O-16, O-18. Insbesondere die
   anwendungs- und workerbezogenen Grenzen, Datenflüsse und WAL-/Löschketten
   sind am konkreten Prozess zu belegen.
2. **Föderation und Rollen abnehmen:** O-01 und O-02 sind **teilweise
   implementiert**, aber IdP-Konfiguration, Claims, Rezertifizierung und
   Provider-Scope bleiben zwingende Betriebs- und Abnahmeaufgaben.
3. **Nachweise aufbauen:** O-03, O-08, O-17, O-19 und O-20 — Audit,
   Supply-Chain, Identitätslebenszyklus, Architekturabgleich und
   echte Provider-Testprotokolle.
4. **Betriebsmodell festlegen:** O-06, O-09, O-10, O-11 sowie O-12–O-15
   abhängig von Verfügbarkeit, Schutzbedarf und Archivierung.

**Abgrenzung:** «umgesetzt» bezeichnet eine Funktion im Repository, nicht
eine positive Bewertung durch ISBO, DSB, Penetrationstest oder die Freigabe
eines konkreten Bundes-Schutzobjekts. Für den aktuellen Gesamtstand siehe
[ISDS-Konzept](isds-konzept.md) und
[Zugriffsschutz-Konzept](zugriffsschutz-konzept.md).

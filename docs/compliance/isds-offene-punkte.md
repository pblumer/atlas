# Offene produktseitige Punkte für einen Einsatz in der Bundesverwaltung

Arbeitsliste zum [ISDS-Konzept](isds-konzept.md). Sie enthält **nur, was am
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
> zur Tauglichkeit für einen produktiven PoC. O-01 und O-02 bleiben dort
> ausdrücklich draussen, bekommen aber die Grundlage, auf der sie aufsetzen.

| Nr. | Punkt | Prio | Risiko | Stand heute | Was fehlt |
|-----|-------|------|--------|-------------|-----------|
| O-01 | **Föderierte Authentisierung** (OIDC/SAML, Ziel eIAM) | P1 | R-03 | Nur lokale Passwörter (bcrypt, Session-Cookie). Das Datenmodell hält die Haken `Source`/`ExternalID` bereit, die Authentisierungsgrenze ist bewusst austauschbar gebaut (ADR-0044) | Ein OIDC-Relying-Party-Fluss hinter derselben `*Principal`-Grenze; Rollen-/Gruppen-Mapping aus Claims; Doku für den Betrieb hinter einem authentisierenden Proxy |
| O-02 | **Feingranulare Autorisierung** | P1 | R-04 | Für **Maschinen** ist es gelöst: ein API-Token trägt einen fail-closed begrenzten Geltungsbereich und ist nie admin (ADR-0194). Für **Personen** wird weiterhin nur `admin` erzwungen; Projekt-Sichtbarkeit mit `viewer`/`editor` (ADR-0071) und Gruppen (ADR-0180) existieren. `POST /api/v1/deployments` ist für jeden angemeldeten Benutzer offen. **Konnektoren stehen ganz ausserhalb dieses Modells** (nachgemessen 2026-08-27): der Datensatz kennt keinen Eigentümer, und jedes angemeldete Konto kann jeden Konnektor auflisten, ändern und löschen sowie auf einem fremden ein Inbound-Abonnement anlegen. Bei der Zustellung trägt `PublishInbound` nur Nachrichtenname und Korrelationsschlüssel — der Name ist die ganze Autorisierung | Rollen pro Endpunktgruppe (deployen, starten, Laufzeitdaten lesen, Aufgaben bearbeiten, administrieren); Trennung «Modellierer» / «Betrieb» / «Sachbearbeiter»; Sichtbarkeit von Prozessvariablen an eine Berechtigung binden; **Eigentümer und Freigabeliste am Konnektor samt Anspruch auf den Nachrichtennamen** — Konzept M11 und [`ADR-draft-connector-ownership-and-event-delivery`](../adr/draft-connector-ownership-and-event-delivery.md) |
| O-03 | **Sicherheits-Audit-Log** | P1 | R-13 | **Weitgehend erledigt** (ADR-0197): stabile `auth.*`-Ereignisse für An-/Abmeldung, Fehlversuch mit Grund, Drosselung, Autorisierungsverweigerung, Konto-Lebenszyklus, Passwortsetzung und Deploy-Token — je Zeile mit Akteur und Absenderadresse, ohne Secret, maschinenlesbar mit `--log-format=json` | Noch nicht abgedeckt: Secret- und Connector-Änderungen, Backup/Restore und Deploy als eigene Ereignisse; ein HTTP-Access-Log im Produkt (bleibt Proxy-Aufgabe, M-06) |
| O-04 | **Anmelde-Härtung** | P1 | R-03, R-12 | **Erledigt, soweit ohne Föderation sinnvoll** (ADR-0197): Drosselung je Absenderadresse *und* je Konto vor dem Kontolookup, ohne Enumerationssignal; erfolgreiche Anmeldung setzt das Kontobudget zurück; jede Verweigerung protokolliert. Mindestlänge weiterhin 8 Zeichen | Konfigurierbare Passwortregeln; MFA — beides sinnvoller über O-01 (Föderation) als über lokale Passwörter |
| O-05 | **Löschnachweis über die ganze Kette** | P1 | R-06 | Retention löscht Zustandsdatensätze endgültig (ADR-0115/0144); die Ereignisse verbleiben im WAL bis `--compact-wal` greift | Kopplung von Aufbewahrungsfrist und Kompaktierung, sodass «gelöscht» belegbar ist; ein Löschbericht/Endpunkt, der pro Instanz zeigt, wo noch Kopien liegen (WAL, Export, Snapshot) |
| O-06 | **Verschlüsselung ruhender Daten** | P2 | R-05 | Nur Vault-Secrets sind verschlüsselt (AES-256-GCM, ADR-0069/0070); WAL, State-Store und Sidecar-Stores liegen im Klartext | Entweder Verschlüsselung auf Produktebene (mindestens für Variablen-/Historienfamilien) oder eine geprüfte, dokumentierte Anforderung an Datenträgerverschlüsselung samt Schlüsselverwaltung |
| O-07 | ~~**Authentisierung für `/mcp` und `/metrics`**~~ | — | R-08 | **Erledigt.** `/mcp` ist transportseitig durch `--auth` geschützt und ein Werkzeugaufruf handelt mit dem Credential des Aufrufers (ADR-0196); `/metrics` verlangt ein API-Token mit Geltungsbereich `metrics` (ADR-0198). Welche Route offen ist, ist deklariert und per Test gegen eine ausgeschriebene Liste gehalten (ADR-0199) | — |
| O-08 | **Lieferkettennachweis** | P2 | R-11 | Releases mit `SHA256SUMS`; CGO-frei, `-trimpath`; CI prüft Build, Vet, Format, Race, 95 % Coverage — aber **keine** Signatur, **kein** SBOM, **keine** Schwachstellenprüfung der Abhängigkeiten | Release-Signierung (z. B. cosign), SBOM (CycloneDX/SPDX) je Release, `govulncheck` und ein SAST-Lauf in der CI, dokumentierter reproduzierbarer Build |
| O-09 | **Mandanten-/Klassifizierungstrennung** | P2 | Kap. 4 | Eine Installation = ein Datenverzeichnis, ein Benutzerverzeichnis, ein Autorisierungsmodell | Trennung von Fachbereichen innerhalb einer Installation, oder eine ausdrückliche Produktaussage «eine Installation je Schutzbedarfsklasse» samt Betriebsmuster |
| O-10 | **Hochverfügbarkeit** | P2 | R-07 | Single-Writer, ein Prozess pro Datenverzeichnis; Replikation ist als ADR-0175 **Proposed** beschrieben, WAL-Replikation steht offen auf der Roadmap | Umsetzung der replizierten Partitionszellen oder ein dokumentiertes, getestetes Aktiv/Passiv-Muster mit Fencing (damit nie zwei Prozesse dasselbe Verzeichnis öffnen) |
| O-11 | **Produktreife 1.0** | P2 | R-01 | `0.x`: On-Disk-Format instabil, kein Downgrade, Sicherheitsfixes nur im nächsten Release, kein LTS (`SECURITY.md`, `docs/install.md`) | Stabilitätszusage für On-Disk-Format und API, Migrationspfad zwischen Releases, veröffentlichte Sicherheits-Support-Policy mit Reaktionszeiten |
| O-12 | **Archivierungsschnittstelle** | P3 | Kap. 9 | Export über API, Instanz-Snapshots (ADR-0109), Backup (ADR-0107), OpenSearch (ADR-0114) — alles generisch, nichts BAR-spezifisch | Ein Export, der Geschäftsfälle in einem archivtauglichen, selbstbeschreibenden Format ablegt (Metadaten, Prüfsummen, Ablieferungseinheit) |
| O-13 | **Schlüsselrotation im Vault** | P3 | 6.2 | `keyId` ist vorhanden und erkennt einen Schlüsselwechsel; die Rotation selbst ist als Folgearbeit dokumentiert (ADR-0069) | Ein Rotationslauf (Neuversiegeln unter neuem Schlüssel) inkl. Betriebsanleitung |
| O-14 | **Durable Sessions und Sitzungsverwaltung** | P3 | R-12 | Sessions nur im Arbeitsspeicher, 12 h fest, Neustart meldet alle ab (ADR-0044) | Konfigurierbare Lebensdauer, optionale Persistenz, Übersicht und gezieltes Beenden aktiver Sitzungen (auch als Reaktionsmittel bei einem Vorfall) |
| O-15 | **Konfigurations-Check gegen die Vorgaben** | P3 | alle | Alle Einstellungen sind in `docs/install.md` dokumentiert, die Prüfung ist manuell | Ein `atlas check`-Lauf, der die sicherheitsrelevante Konfiguration bewertet (`--auth`, Vault-Schlüsselquelle, offene Endpunkte, Skriptsprachen, Retention) — macht die jährliche Prüfung nach Kapitel 8.3 reproduzierbar |

## Reihenfolge-Vorschlag

1. **O-03, O-04** — kleiner Aufwand, schliessen die Nachweislücke, die bei jeder
   Prüfung zuerst auffällt.
2. **O-02** dann **O-01** — erst die Rollen definieren, die eine externe Identität
   danach zugewiesen bekommt; umgekehrt entsteht ein Mapping ins Leere.
3. **O-05, O-08** — Datenschutz-Nachweis und Lieferkette; beides ist ohne
   Codeänderung am Kern machbar.
4. **O-06, O-07, O-09** — Härtung, die vom gewählten Betriebsmuster abhängt.
5. **O-10, O-11** — bestimmen, für welche Verfügbarkeits- und
   Schutzbedarfsklassen Atlas überhaupt in Frage kommt.

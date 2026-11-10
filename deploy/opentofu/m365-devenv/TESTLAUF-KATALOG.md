# Testlauf Produktkatalog über MCP

Ein wiederholbarer Abnahmetest aller Kataloge dieser Umgebung. Ein Agent führt ihn über MCP
durch und spielt dabei die Rollen von der Mitarbeiterin bis zum Vorgesetzten, je Rolle mit
einer eigenen MCP-Verbindung. Der Lauf bestellt, genehmigt, ändert, gibt zurück und prüft
jeden Schritt an Atlas: an Bestellung, Aufgabe, Instanz, Ergebnis der Aktion und Incident.

Aufruf an den Agenten:

> Nimm `deploy/opentofu/m365-devenv/TESTLAUF-KATALOG.md` und führe den Testlauf via MCP durch.

Das Szenario mit der Beispielfirma ([TESTSZENARIO.md](TESTSZENARIO.md)) bleibt der Test von
Hand, der zeigt, wie es sich für die Mitarbeitenden in Outlook und Teams anfühlt. Dieser
Testlauf prüft die Breite: jedes Produkt, jeder Genehmigungsweg, jede Kundenaktion.

## Was der Start des Laufs erlaubt

Wer den Lauf startet, gibt damit sein Einverständnis für genau das, was dieses Drehbuch im
Tenant und im Active Directory anlegt, ändert und wieder entfernt. Alles trägt den Präfix
der Umgebung und die Laufkennung. Nicht darin enthalten und weiterhin nur mit
ausdrücklicher Zustimmung:

- jede Änderung an Azure-Ressourcen (`tofu`, VMs, Netzwerk),
- neue Rechte der Entra-App,
- Objekte ohne Präfix oder ausserhalb von `OU=AtlasTest`,
- Umwege um einen Fehler herum.

## Regeln für den Agenten

1. **Jede Rolle nur über ihre Verbindung.** Was Anna tut, geht über `atlas-anna`, was Max
   genehmigt, über `atlas-max`. Nie bestellt oder genehmigt eine Verbindung für eine andere
   Person, ausser der Fall verlangt ausdrücklich «Bestellen für» (ADR-0349).
2. **Laufkennung in jedem Namen.** `RUN` = `T` + Datum und Uhrzeit in UTC, etwa
   `T20261110-0930`. Jeder Anzeigename, Alias und jede Beschreibung beginnt mit `RUN`, damit
   sich Läufe nicht stören und das Aufräumen eindeutig ist.
3. **Erst lesen, dann handeln.** Vor einer Bestellung das Formular und die Varianten aus dem
   Katalog lesen, vor einer Aktion `atlas_order_line_actions`. Felder nicht raten.
4. **Warten mit Grenze.** Nach jedem Schritt den Fortschritt der Position lesen, bis sie den
   erwarteten Stand erreicht, höchstens 15 Minuten (SharePoint und Exchange 30). Länger
   heisst: Fall fehlgeschlagen, Protokoll, weiter mit dem nächsten unabhängigen Fall.
5. **Fehler nicht umgehen.** Bei einem Incident, einer Ablehnung, die nicht erwartet war,
   oder einem HTTP-Fehler: Ausgabe ins Protokoll, Ursache benennen, soweit sie aus Atlas
   lesbar ist, keine Reparatur. Ein Incident wird nicht aufgelöst, ausser der Fall sagt es.
6. **Keine Geheimnisse.** Passwörter, Initialpasswörter und Tokens erscheinen weder im Chat
   noch im Protokoll. Ein Fall, der ein Passwort erzeugt, prüft nur, dass es erzeugt wurde.
7. **Aufräumen gehört dazu.** Was der Lauf bestellt, gibt er in Phase 9 zurück, auch nach
   Fehlern. Was sich nicht zurückgeben lässt, steht im Protokoll.

## Voraussetzungen

| Was | Prüfung |
|---|---|
| VMs `atlas`, `dc`, `exch` laufen, Atlas antwortet | `atlas_info` über `atlas-admin` |
| Worker `entra`, `exchange`, `sharepoint`, `mail`, `ad` verbunden | `atlas_workers` |
| Applikationen installiert: M365-Dienste (mit Exchange-Server-Variante), AD-Dienste, Lizenzmanagement, Informationstransfer | `atlas_list_applications`, `atlas_list_catalogs` |
| Konstante `ORTSANGABEN` = `false` in `beispiel-m365-dienste` | `atlas_application_constants` |
| Freie Lizenzen: mindestens 4 | Lizenzmanagement, Bestand |
| Shop über MCP nicht abgeschaltet | Server ohne Opt-out (siehe ADR-Entwurf «Shop über MCP») |

### MCP-Verbindungen je Rolle

Jede Verbindung zeigt auf `https://<atlas_url>/mcp` und meldet sich einmal per OAuth an. Die
Anmeldung macht ein Mensch im Browser mit dem Konto der Testperson; die Passwörter liefert
`tofu output -json test_user_passwords`. Danach behält Claude Code die Anmeldung.

```sh
claude mcp add --transport http atlas-admin https://<atlas_url>/mcp
claude mcp add --transport http atlas-anna  https://<atlas_url>/mcp
claude mcp add --transport http atlas-beat  https://<atlas_url>/mcp
claude mcp add --transport http atlas-carla https://<atlas_url>/mcp
claude mcp add --transport http atlas-max   https://<atlas_url>/mcp
```

Anschliessend in Claude Code `/mcp`, jede Verbindung wählen und anmelden: `atlas-admin` als
lokaler Administrator, die anderen mit «Sign in with Microsoft» als die jeweilige Person.

### Rollen

Die Installationsskripte nehmen jede angemeldete Testperson in alle Gruppen auf. Für einen
Test der Genehmigungswege braucht es getrennte Rollen. Vor dem ersten Lauf setzt
`atlas-admin` die Mitgliedschaften so:

| Verbindung | Person | Rolle im Test | Atlas-Gruppen |
|---|---|---|---|
| `atlas-anna` | Anna Muster | Mitarbeiterin, bestellt fast alles | Zielgruppen: `M365-Mitarbeitende`, `M365-UCC` |
| `atlas-beat` | Beat Beispiel | Mitarbeiter, Empfänger von «Bestellen für» und Gast | Zielgruppen |
| `atlas-carla` | Carla Probe | Fachstellen | `M365-Facility`, `M365-Administration`, `Informationssicherheit`, `AD-Administration`, `Lizenzmanagement` |
| `atlas-max` | Max Vorgesetzt | Vorgesetzter von Anna, Beat und Carla (Entra `manager`) | Zielgruppen |
| `atlas-admin` | lokaler Administrator | Betrieb: Vorprüfung, HR-Ereignis, Prüfungen, Aufräumen | admin |

Kennt eine Gruppe ihre Mitglieder nicht über MCP, setzt sie ein Mensch in der Console. Das
Protokoll nennt dann den Stand.

## Ablauf

Jeder Fall hat eine Kennung. Ein Fall ist **bestanden**, wenn alle Erwartungen eintreffen,
**fehlgeschlagen**, wenn eine nicht eintrifft, und **übersprungen**, wenn eine Voraussetzung
fehlt (Grund ins Protokoll).

### Phase 0: Vorprüfung (`atlas-admin`)

| Fall | Schritt | Erwartung |
|---|---|---|
| V1 | `atlas_info`, `atlas_workers` | Version notiert; alle fünf Worker verbunden |
| V2 | `atlas_list_incidents`, `atlas_incident_summary` | Ausgangsbestand notiert: nur neue Incidents zählen als Befund |
| V3 | `atlas_list_tasks` | offene Aufgaben notiert; alte Aufgaben bleiben unberührt |
| V4 | `atlas_application_constants` für die vier Applikationen | `NAME_PREFIX`, `ORTSANGABEN = false`, `ENTRA_TRANSFER = aus` |

### Phase 1: Sichtbarkeit (jede Rolle)

| Fall | Rolle | Schritt | Erwartung |
|---|---|---|---|
| S1 | Anna | Kataloge lesen | sieht M365 für Mitarbeitende, Kommunikation (UCC), Exchange und SharePoint Server (OnPrem), Active Directory, Lizenzmanagement: Verträge, Informationstransfer |
| S2 | Anna | Katalog HR-Eintritte | nicht sichtbar oder nicht bestellbar (`orderableBy` = Applikation) |
| S3 | Anna | Katalog Lizenzmanagement: Betrieb | nicht sichtbar (Gruppe Tenant-Verantwortung) |
| S4 | Max | Kataloge lesen | dieselben Zielgruppen-Kataloge wie Anna |

### Phase 2: Bestellen mit allen Genehmigungswegen

Genehmigung über `proc_m365_genehmigung_vorgesetzter` und `superior` landet bei Max,
`role` bei Carla, `none` geht direkt in die Bereitstellung.

| Fall | Rolle | Produkt | Angaben | Weg | Erwartung |
|---|---|---|---|---|---|
| B1 | Anna | `m365-team` | `RUN Team`, privat, Besitzerin Anna | Max genehmigt | Team in Entra, Position gehalten |
| B2 | Anna | `m365-sharepoint-site` | `RUN Site`, de-CH, privat | Max genehmigt | Site angelegt, im Site-Inventar |
| B3 | Anna | `m365-verteiler` | `RUN Verteiler`, nur intern | keine | Verteiler in Exchange Online |
| B4 | Anna | `m365-funktionspostfach` | `RUN Info` | Max genehmigt | Postfach angelegt |
| B5 | Anna | `m365-kalender` | `RUN Kalender` | Max genehmigt | Kalender angelegt |
| B6 | Anna | `m365-raum` (UCC) | `RUN Raum`, 6 Plätze, Ort `Bern, 1. Stock` | Carla (Facility) genehmigt | Raumpostfach, in der Raumliste |
| B7 | Anna | `m365-geraet` (UCC) | `RUN Beamer` | Carla genehmigt | Gerätepostfach |
| B8 | Anna für Beat | `m365-postfachzugriff` | Beat auf `RUN Info`, 30 Tage | Max genehmigt | Beat hat Vollzugriff; Empfänger ist Beat (ADR-0349) |
| B9 | Anna | `m365-lizenz`, Variante `standard` | für Beat, falls er keine hat, sonst übersprungen | Max genehmigt | Lizenz zugewiesen, Postfach vorhanden |
| B10 | Anna | Exchange Server: Verteiler (OnPrem) | `RUN Verteiler OnPrem` | keine | Verteiler auf `exch01` |
| B11 | Anna | `ad-sicherheitsgruppe` | `RUN Gruppe` | Max genehmigt (`superior`) | Gruppe in `OU=AtlasTest` |
| B12 | Anna | `ad-recht-fileshare-finanzen-lesen` | für sich, 30 Tage | Carla (AD-Administration) genehmigt | Mitglied der Berechtigungsgruppe |
| B13 | Anna | `ad-testkonto` | `RUN` | keine | Konto in `OU=AtlasTest`, Passwort erzeugt, nicht angezeigt |
| B14 | Anna | `lz-lizenzvertrag` | `RUN Vertrag`, 2 Lizenzen | keine | Vertrag gehalten, Kontingent gebucht |
| B15 | Anna | SharePoint Server: Teamwebsite | `RUN SP` | wie Produkt | nur mit `enable_sharepoint_server`, sonst übersprungen |

Je Fall prüfen: Bestellung und Position lesen, die Aufgabe der genehmigenden Rolle in deren
Aufgabenliste finden, genehmigen, dann den Fortschritt bis «gehalten» verfolgen. Am Ende
`atlas_order_line_outcomes`: die Bereitstellung `completed`, kein neuer Incident.

### Phase 3: Ablehnen und zurückziehen

| Fall | Rolle | Schritt | Erwartung |
|---|---|---|---|
| A1 | Anna, Max | `m365-team` `RUN Abgelehnt` bestellen, Max lehnt mit Begründung ab | Position abgelehnt, nichts im Tenant, Anna sieht die Begründung |
| A2 | Anna | `m365-funktionspostfach` `RUN Storno` bestellen und vor der Genehmigung stornieren | Position storniert, Aufgabe bei Max verschwunden |
| A3 | Anna | eine noch nicht genehmigte Position in den Angaben korrigieren | neue Angaben gelten, die Genehmigung sieht sie |
| A4 | Anna | Aufgabe von Max über Annas Verbindung abschliessen | verweigert: Anna ist nicht die Genehmigende |
| A5 | Anna | `m365-eintritt` bestellen | verweigert: nur die Applikation bestellt es (`orderableBy`) |

### Phase 4: Kundenaktionen auf gehaltenen Positionen

Je Aktion vorher `atlas_order_line_actions` (verfügbar?), danach `atlas_order_line_outcomes`
(`completed`).

| Fall | Position | Aktion | Erwartung |
|---|---|---|---|
| K1 | B1 Team | `mitglied` (Beat dazu) | Beat Mitglied des Teams |
| K2 | B1 Team | `kanal` (`RUN Kanal`) | Kanal angelegt |
| K3 | B1 Team | `sichtbarkeit` (öffentlich) | Team öffentlich |
| K4 | B2 Site | `besitzer`, `kontingent`, `gast` | Besitzer gesetzt, Kontingent geändert, Gast eingeladen (Gast aus Annas Testdomäne) |
| K5 | B3 Verteiler | `mitglied-aufnehmen`, dann `mitglied-entfernen` (Beat) | Mitglied erst da, dann weg |
| K6 | B4 Postfach | `zugriff-erteilen`, dann `zugriff-entziehen` (Beat) | Berechtigung erst da, dann weg |
| K7 | B5 Kalender | `berechtigung-erteilen`, dann `berechtigung-entziehen` | wie K6 |
| K8 | B9 Lizenz | `abwesenheit`, dann `abwesenheit-beenden` | Abwesenheitsnotiz an, dann aus |
| K9 | B9 Lizenz | `alias`, dann `alias-weg` (`run-alias`) | Proxyadresse da, dann weg |
| K10 | B9 Lizenz | `weiterleitung`, dann `weiterleitung-aus` | Weiterleitung gesetzt, dann aufgehoben |
| K11 | B9 Lizenz | `stellvertretung`, dann `stellvertretung-weg` | Stellvertretung gesetzt, dann aufgehoben |
| K12 | B9 Lizenz | `kontingent` | Kontingent geändert |
| K13 | B11 Gruppe | `mitglied`, `mitglied-weg`, `verantwortung` | Mitgliedschaft und Verantwortung in AD |
| K14 | B13 Testkonto | `entsperren`, `passwort` | ausgeführt; kein Passwort im Protokoll |
| K15 | B14 Vertrag | `aufstocken`, dann `senken` | Kontingent erst höher, dann wie vorher |
| K16 | Beat auf B1 | eine Aktion auf Annas Position | verweigert oder nur, wenn Beat Empfänger ist (ADR-0429 §10) |

### Phase 5: Betriebsaktionen (`atlas-carla`, Operator)

| Fall | Schritt | Erwartung |
|---|---|---|
| O1 | `atlas_ask_order_line_action` mit `trigger = operator` auf B9: `wechseln` zu `erweitert`, sofern frei, sonst übersprungen | Variante gewechselt |
| O2 | dieselbe Aktion mit gleicher `commandId` wiederholen | erste Antwort zurück, nichts doppelt |

### Phase 6: Eintritt und Austritt über das HR-Ereignis (`atlas-admin`)

| Fall | Schritt | Erwartung |
|---|---|---|
| H1 | `atlas_trigger_start` auf `proc_m365_hr_eintritt`, Trigger `hr.eintritt`, Personalnummer `RUN-1`, Vorname `Test`, Nachname `RUN`, OE `IT`, Variante `standard`, Vorgesetzter Max | Konto in Entra, Eintrittspaket bestellt im Namen der Applikation |
| H2 | Aufgaben «Initialpasswort» und «Zugangsdaten» bei `M365-Administration` (Carla) erledigen | Paket gehalten; kein Passwort im Protokoll |
| H3 | `atlas_publish_message` `hr.austritt` mit Personalnummer `RUN-1` | Konto gesperrt, Paket zurückgegeben |

### Phase 7: Informationstransfer und Lizenzmanagement

| Fall | Rolle | Schritt | Erwartung |
|---|---|---|---|
| I1 | Anna | `it-informationstransfer`, Variante `abteilungswechsel`, Quelle das Konto aus H1, Ziel Beat; vor H3 ausführen | Gruppenmitgliedschaften übertragen, Quelle markiert; Entra-Transfer bleibt aus (`ENTRA_TRANSFER = aus`) |
| L1 | `atlas-admin` | Bestand des Lizenzmanagements nach B9 und B14 lesen | Zahlen stimmen mit den Bestellungen |

### Phase 8: Rückgaben

Jede gehaltene Position aus Phase 2 zurückgeben, in umgekehrter Reihenfolge (abhängige zuerst:
B8 vor B4, B12 vor B11).

| Fall | Schritt | Erwartung |
|---|---|---|
| R1 | Rückgabe je Position | Rückbau `completed`; Objekt im Tenant oder AD entfernt oder im Papierkorb |
| R2 | Rückgabe einer Position, von der eine andere abhängt, vor dieser | verweigert mit Hinweis auf die abhängige Position |
| R3 | `m365-sharepoint-site`: `loeschen`, dann `wiederherstellen`, dann Rückgabe | Site erst im Papierkorb, dann zurück, dann entfernt |

### Phase 9: Abschluss (`atlas-admin`)

| Fall | Schritt | Erwartung |
|---|---|---|
| Z1 | Incidents gegen den Ausgangsbestand aus V2 | keine neuen; jeder neue steht mit Instanz und Meldung im Protokoll |
| Z2 | offene Aufgaben gegen V3 | keine neuen |
| Z3 | Bestellungen des Laufs | alle Positionen zurückgegeben, abgelehnt oder storniert |
| Z4 | Restobjekte mit `RUN` im Namen | keine; sonst Liste ins Protokoll für das Aufräumskript |

## Protokoll

Der Agent schreibt das Protokoll nach `deploy/opentofu/m365-devenv/out/testlauf/<RUN>.md`.
`out/` wird nie committet. Darin:

1. Kopf: `RUN`, Atlas-Version und Revision, Start und Ende in UTC, aktive Worker.
2. Tabelle aller Fälle: Kennung, Ergebnis, Dauer, Bestell- und Instanzschlüssel.
3. Je fehlgeschlagenem Fall: Schritt, erwartete und tatsächliche Antwort, Incident-Meldung,
   vermutete Ursache, als Vermutung gekennzeichnet.
4. Liste der Restobjekte, falls Phase 9 welche findet.

Im Chat nennt der Agent am Ende nur die Zusammenfassung und den Pfad.

## Grenzen

- Was die Mitarbeitenden in Outlook, Teams oder im Explorer sehen, prüft der Lauf nicht. Er
  prüft Atlas und, wo ein Worker es liest, das Zielsystem.
- `Set-Place` antwortet in dieser Umgebung mit HTTP 500 (siehe [TESTSZENARIO.md](TESTSZENARIO.md));
  Ort und Stockwerk eines Raums prüft der Lauf darum nicht in Places.
- Nicht im Katalog dieser Umgebung und darum nicht im Lauf: Telefonie, Anrufwarteschlange,
  Telefonzentrale (kein Teams-Phone-Worker), Viva Engage, Aufbewahrung, hybride Identität.
- Ein Lauf braucht je nach Exchange-Antwortzeiten zwei bis vier Stunden. Die VMs fahren um
  22:00 UTC herunter; ein Lauf sollte bis 18:00 UTC begonnen haben.

# Testlauf Produktkatalog: Hochlauf eines KMU

Ein wiederholbarer Abnahmetest aller Kataloge dieser Umgebung, erzählt als das, wofür ein
kleines oder mittleres Unternehmen den Katalog tatsächlich braucht: den **Hochlauf** von der
Gründung bis zum Alltag. Die *Helvetia Werkstatt AG* (Planung und Ausführung von
Innenausbauten, Sitz Bern) zieht auf Microsoft 365 und wächst in vier Wochen von null auf
zwölf Mitarbeitende. Ein Agent führt den Lauf über MCP durch und spielt die Personen vom
Geschäftsführer bis zum Mitarbeiter, je Person mit einer eigenen MCP-Verbindung. Jeder
Schritt wird an Atlas geprüft: an Bestellung, Aufgabe, Instanz, Ergebnis der Aktion und
Incident.

Aufruf an den Agenten:

> Nimm `deploy/opentofu/m365-devenv/TESTLAUF-KATALOG.md` und führe den Testlauf via MCP durch.

Der Lauf hat einen zweiten Nutzen: Er ist zugleich ein **Leitfaden für den Hochlauf eines
KMU** mit Atlas (Abschnitt «Vom Test zum Leitfaden»). Der Test von Hand mit derselben Firma,
der zeigt, wie es sich in Outlook und Teams anfühlt, steht in [TESTSZENARIO.md](TESTSZENARIO.md).

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

1. **Jede Person nur über ihre Verbindung.** Was Anna tut, geht über `atlas-anna`, was Max
   genehmigt, über `atlas-max`. Keine Verbindung bestellt oder genehmigt für eine andere
   Person, ausser der Fall verlangt ausdrücklich «Bestellen für» (ADR-0349).
2. **Laufkennung in jedem Namen.** `RUN` = Datum und Uhrzeit in UTC, etwa `1110-0930`; die
   Firma heisst im Lauf `HW<RUN>`, etwa `HW1110-0930 Projekte`. Personalnummern sind
   `<RUN>-01` bis `<RUN>-12`. So stören sich Läufe nicht, und das Aufräumen ist eindeutig.
3. **Erst lesen, dann handeln.** Vor einer Bestellung Formular und Varianten aus dem Katalog
   lesen, vor einer Aktion `atlas_order_line_actions`. Felder nicht raten; fehlt eine Angabe,
   die das Drehbuch nicht nennt, eine plausible für die Firma wählen und notieren.
4. **Warten mit Grenze.** Nach jedem Schritt den Fortschritt der Position lesen, bis sie den
   erwarteten Stand erreicht, höchstens 15 Minuten (SharePoint und Exchange 30). Länger
   heisst: Fall fehlgeschlagen, Protokoll, weiter mit dem nächsten unabhängigen Fall.
5. **Fehler nicht umgehen.** Bei einem Incident, einer unerwarteten Ablehnung oder einem
   HTTP-Fehler: Ausgabe ins Protokoll, Ursache benennen, soweit sie aus Atlas lesbar ist,
   keine Reparatur. Ein Incident wird nicht aufgelöst, ausser der Fall sagt es.
6. **Keine Geheimnisse.** Passwörter, Initialpasswörter und Tokens erscheinen weder im Chat
   noch im Protokoll. Ein Fall, der ein Passwort erzeugt, prüft nur, dass es erzeugt wurde.
7. **Aufräumen gehört dazu.** Was der Lauf bestellt, gibt er in der letzten Phase zurück,
   auch nach Fehlern. Was sich nicht zurückgeben lässt, steht im Protokoll.
8. **Eine Bestellung nicht blind wiederholen.** `atlas_place_order` hat keinen
   Idempotenzschlüssel: Nach einem Fehler erst `atlas_list_orders` lesen, ob die Bestellung
   doch angekommen ist. Aktionen und Änderungen tragen `commandId` und `changeId`; eine
   Wiederholung nimmt denselben Wert. Die Werte enthalten `RUN`, etwa `HW<RUN>-W6`: Atlas
   merkt sich einen Wert über den Lauf hinaus und beantwortet einen alten erneut, statt die
   Aktion auszuführen.

## Werkzeuge

Die Shop-Werkzeuge handeln immer als die Person der Verbindung, mit deren Rechten
(ADR-Entwurf `docs/adr/0550-mcp-shop-customer-side.md`). Ein Server mit
`--mcp-shop=false` lehnt sie mit 403 ab; dann bricht der Lauf in Phase 0 ab.

| Schritt im Drehbuch | Werkzeug |
|---|---|
| Kataloge lesen | `atlas_shop_catalogs`, `atlas_shop_catalog` |
| Person oder Wert in einem Formular suchen | `atlas_shop_form_query` |
| bestellen, «Bestellen für» | `atlas_place_order` |
| Bestellungen und Positionen lesen | `atlas_list_orders`, `atlas_get_order` |
| Fortschritt einer Position | `atlas_order_line_progress` |
| offene Aufgaben der eigenen Bestellungen | `atlas_shop_tasks` |
| genehmigen und ablehnen | `atlas_list_approvals`, `atlas_decide_approvals` |
| Aufgaben einer Fachstelle (Initialpasswort usw.) | `atlas_list_tasks`, `atlas_claim_task`, `atlas_complete_task` |
| stornieren | `atlas_cancel_order`, `atlas_cancel_order_line` |
| Angaben korrigieren | `atlas_amend_order_line` |
| zurückgeben | `atlas_return_order_line` |
| Aktion einer gehaltenen Position | `atlas_order_line_actions`, dann `atlas_ask_order_line_action` mit `trigger = customer` (Person) oder `operator` (Carla), Ergebnis mit `atlas_order_line_outcomes` |
| HR-Ereignis | `atlas_trigger_start` (Eintritt), `atlas_publish_message` (Austritt; mit `messageId` auch `timeToLiveMs`, sonst wird die Nachricht abgelehnt) |
| Applikation aus dem atlas hub | `atlas_hub_list_releases`, `atlas_hub_preview_release`, `atlas_hub_import_release`, `atlas_list_applications` |
| OE-Tabelle | `atlas_get_decision_model`, `atlas_upload_decision_model`, `atlas_deploy_decision`, `atlas_try_decision` |
| Prüfungen | `atlas_info`, `atlas_workers`, `atlas_list_incidents`, `atlas_incident_summary`, `atlas_application_constants`, `atlas_instance_timeline` |

## Voraussetzungen

| Was | Prüfung |
|---|---|
| VMs `atlas`, `dc`, `exch` laufen, Atlas antwortet | `atlas_info` über `atlas-admin` |
| Worker `entra`, `exchange`, `sharepoint`, `mail`, `ad` verbunden | `atlas_workers` |
| Applikationen installiert: M365-Dienste (mit Exchange-Server-Variante), AD-Dienste, Lizenzmanagement | `atlas_list_applications`, `atlas_list_catalogs` |
| Informationstransfer **nicht** installiert: der Lauf installiert ihn in Phase 0 aus dem atlas hub. Ist er von einem früheren Lauf aus dem hub da, ist H3 ein Import ohne Änderung; stammt er aus `install-informationstransfer.py` ohne `--nach-hub`, die Applikation vorher in der Console löschen | `atlas_list_applications` |
| Hub-Register gepinnt und erreichbar | `atlas_hub_list_releases` antwortet ohne 502/503 |
| Konstante `ORTSANGABEN` = `false` in `beispiel-m365-dienste` | `atlas_application_constants` |
| Konstante `LIZENZ_VERTRAG_PRUEFEN` = `ein` in `beispiel-m365-dienste`; sonst belastet ein Eintritt keinen Vertrag, und E4 scheitert | `atlas_application_constants` |
| Freie Lizenzen: mindestens 13 (12 Eintritte, eine Reserve) | Lizenzmanagement, Bestand |
| Shop über MCP nicht abgeschaltet | Server ohne Opt-out (`mcpShop` in `atlas_info`) |

### MCP-Verbindungen je Person

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

### Besetzung

Die vier Testkonten spielen die Personen, die in einem KMU den Hochlauf tragen. Wer über das
HR-Ereignis eintritt, ist Empfänger und meldet sich im Lauf nicht an.

| Verbindung | Testkonto | Rolle in der Firma | Atlas-Gruppen |
|---|---|---|---|
| `atlas-max` | Max Vorgesetzt | Geschäftsführer; Vorgesetzter von Anna, Beat und Carla (Entra `manager`), genehmigt | `M365-Mitarbeitende`, `M365-UCC`, `Lizenz-Chef` |
| `atlas-anna` | Anna Muster | Office-Managerin: bestellt für die Firma | `M365-Mitarbeitende`, `M365-UCC`, `Identity Management`, `Lizenz-Integrations-Manager` |
| `atlas-carla` | Carla Probe | IT-Verantwortliche: alle Fachstellen | M365-Zielgruppen und `M365-Facility`, `M365-Telefonie`, `M365-Administration`, `Informationssicherheit`, `AD-Administration`, `Identity Management`, `Mainframe`, `Lizenzmanagement`, `Lizenz-Tenantverantwortung`, `Lizenz-Finanzen`, `Lizenz-Integrations-Manager` |
| `atlas-beat` | Beat Beispiel | Mitarbeiter der ersten Stunde, Empfänger von «Bestellen für» | `M365-Mitarbeitende`, `M365-UCC` |
| `atlas-admin` | lokaler Administrator | HR-System und Betrieb: Eintritte, OE-Tabelle, Prüfungen | admin, Mitgliedschaften unverändert |

Die Operator-Aktionen (A4, W6, W7) setzen die Atlas-Rolle `operator` voraus. Weder Carla
noch Anna haben sie, und `scripts/testlauf-rollen.py` setzt sie nicht; bis das so ist,
führt `atlas-admin` diese Fälle aus, und das Protokoll vermerkt es.

Die Installationsskripte nehmen jede angemeldete Testperson in alle Gruppen auf. Die
Trennung oben setzt `scripts/testlauf-rollen.py` (nur Atlas-Gruppen); nach jedem Lauf eines
Installationsskripts erneut ausführen, `--dry-run` zeigt nur, was sich ändern würde.

### Die Belegschaft

Eintritt über das HR-Ereignis (Phase 3 und 5), Organisationseinheiten wie im Unternehmen:

| Nr. | Person | OE | Funktion | Woche |
|---|---|---|---|---|
| 01 | Claudia Brunner | GL | Geschäftsleitung, Finanzen | 1 |
| 02 | Reto Keller | GL | Geschäftsleitung, Verkauf | 1 |
| 03 | Sandra Meier | PROJ | Projektleiterin | 1 |
| 04 | Luca Bernasconi | PROJ | Projektleiter | 1 |
| 05 | Nina Frei | PROJ | Bauzeichnerin | 1 |
| 06 | Martina Huber | ADM | Personal (HR) | 1 |
| 07 | Daniel Wyss | ADM | Empfang | 1 |
| 08 | Marco Steiner | IT | Informatik | 1 |
| 09 | Elena Rossi | PROJ | Projektleiterin, zweites Team | 3 |
| 10 | Jonas Weber | PROJ | Bauleiter | 3 |
| 11 | Lea Schmid | PROJ | Bauzeichnerin | 3 |
| 12 | Samuel Graf | ADM | Lernender kaufmännisch | 3 |

## Ablauf

Jeder Fall hat eine Kennung. Ein Fall ist **bestanden**, wenn alle Erwartungen eintreffen,
**fehlgeschlagen**, wenn eine nicht eintrifft, und **übersprungen**, wenn eine Voraussetzung
fehlt (Grund ins Protokoll). Die Spalte «Prüft» zeigt, welche Eigenschaft des Katalogs der
Fall abdeckt; die Abdeckungstabelle am Ende fasst sie zusammen.

### Phase 0: Vor dem Start (`atlas-admin`)

| Fall | Schritt | Erwartung |
|---|---|---|
| V1 | `atlas_info`, `atlas_workers` | Version notiert; alle fünf Worker verbunden |
| V0 | `atlas_info` | `mcpShop` ist `true`; sonst Abbruch |
| V2 | `atlas_list_incidents`, `atlas_incident_summary` | Ausgangsbestand notiert: nur neue Incidents zählen als Befund |
| V3 | `atlas_list_tasks` | offene Aufgaben notiert; alte Aufgaben bleiben unberührt |
| V4 | `atlas_application_constants` der drei installierten Applikationen | `NAME_PREFIX`, `ORTSANGABEN = false`; `ENTRA_TRANSFER = aus` prüft H5 |
| V5 | `scripts/testlauf-rollen.py --dry-run` | 0 Änderungen; sonst ohne `--dry-run` ausführen und notieren |
| H1 | `atlas_hub_list_releases` | `io.github.pblumer.atlas.informationstransfer` gelistet, Version wie in `examples/hub/releases.json` |
| H2 | `atlas_hub_preview_release` mit dieser Version | Applikation `beispiel-informationstransfer`, Katalog `cat-it-identity` mit `it-informationstransfer`, Needs `ad`, `entra`, `exchange`, `teamsphone`; Digest notiert |
| H3 | `atlas_hub_import_release` mit dem Digest, zuerst als Trockenlauf, dann `dryRun = false` | Plan ohne `changed`, `elsewhere` oder offene Lücke; Import ohne 409; `cataloguesNotImported` nennt `cat-it-identity` |
| H4 | Shell: `python3 scripts/install-informationstransfer.py --nach-hub` | veröffentlicht die Applikation, importiert den Katalog mit Zielgruppe Identity Management, setzt die Konstanten (`ENTRA_TRANSFER = aus`), Gruppen und die Formularsuche am Worker `ad_verzeichnis` |
| H5 | `atlas_list_applications`, `atlas_application_constants`; Anna: `atlas_shop_catalogs` | Applikation veröffentlicht, `AD_PERSONEN_OU` ist die Test-OU, `NAME_PREFIX` gesetzt, `ENTRA_TRANSFER = aus`; Anna sieht «Informationstransfer» |

Den Teamsphone-Worker hat diese Umgebung nicht: Der Import nennt ihn als nicht erfüllten Need,
das ist erwartet. Die Telefonnummer geht im Lauf ohnehin als Aufgabe an `M365-Telefonie`.

### Phase 1: Tag 1, die Firma bekommt ihre Grundausstattung

Max hat entschieden: Microsoft 365 für alle, ein Team je Bereich, eine gemeinsame Ablage,
Sitzungszimmer zum Buchen. Anna bestellt, Max genehmigt, Carla genehmigt als Fachstelle.

| Fall | Person | Schritt | Erwartung | Prüft |
|---|---|---|---|---|
| S1 | Anna | Kataloge lesen | sieht M365 für Mitarbeitende, Kommunikation (UCC), Exchange und SharePoint Server (OnPrem), Active Directory, Lizenzmanagement: Verträge, Informationstransfer | Zielgruppen |
| S2 | Beat | Kataloge lesen | die M365-Kataloge und Active Directory, das dieselbe Zielgruppe hat | Zielgruppen |
| S3 | Anna | Katalog HR-Eintritte, Lizenzmanagement: Betrieb | nicht sichtbar oder nicht bestellbar | `orderableBy`, Zielgruppe |
| G1 | Anna | `lz-lizenzvertrag` `HW<RUN> Vertrag`, 15 Lizenzen | Vertrag gehalten, Kontingent gebucht | ohne Genehmigung |
| G2 | Anna | `m365-team` `HW<RUN> Geschaeftsleitung`, privat | Max genehmigt; Team in Entra | Vorgesetzten-Genehmigung |
| G3 | Anna | `m365-team` `HW<RUN> Projekte`, privat | wie G2 | `multipleAllowed` |
| G4 | Anna | `m365-team` `HW<RUN> Administration`, privat | wie G2 | |
| G5 | Anna | `m365-team` `HW<RUN> Alle`, öffentlich | wie G2; Team öffentlich | Sichtbarkeit |
| G6 | Anna | `m365-team` `HW<RUN> Alle` ein zweites Mal | Max lehnt mit Begründung «gibt es schon» ab; nichts im Tenant, Anna sieht die Begründung | Ablehnung |
| G7 | Anna | `m365-sharepoint-site` `HW<RUN> Projektablage`, de-CH, privat | Max genehmigt; Site im Site-Inventar | |
| G8 | Anna | `m365-verteiler` `HW<RUN> Alle`, nur intern | Verteiler in Exchange Online | ohne Genehmigung |
| G9 | Anna | `m365-funktionspostfach` `HW<RUN> Info` und `HW<RUN> HR` | Max genehmigt; zwei Postfächer | |
| G10 | Anna | `m365-raum` `HW<RUN> Grosser Saal`, zunächst 10 Plätze, `Bern, 1. Stock` | Position wartet auf Carla (Facility) | Fachstellen-Genehmigung |
| G11 | Anna | G10 vor der Genehmigung korrigieren: 12 Plätze | Carla sieht 12, genehmigt; Raumpostfach in der Raumliste | Angaben korrigieren |
| G12 | Anna | `m365-raum` `HW<RUN> Fokusraum` versehentlich mit 40 Plätzen, dann stornieren und mit 4 Plätzen neu | erste Position storniert, Aufgabe bei Carla weg; zweite gehalten | Storno |
| G13 | Anna | `m365-geraet` `HW<RUN> Beamer` | Carla genehmigt; Gerätepostfach | |
| G14 | Anna | `m365-kalender` `HW<RUN> Ferienplan` | Max genehmigt; Kalender angelegt | |
| G15 | Anna | Exchange Server: Verteiler `HW<RUN> Werkstatt` (OnPrem) | Verteiler auf `exch01` | On-Premises-Variante |
| G16 | Anna | `ad-sicherheitsgruppe` `HW<RUN> Projekte` | Max genehmigt (`superior`); Gruppe in `OU=AtlasTest` | Active Directory |
| G17 | Anna | SharePoint Server: Teamwebsite `HW<RUN> Archiv` | nur mit `enable_sharepoint_server`, sonst übersprungen | SharePoint Server |
| G18 | Anna | Aufgabe von Max über ihre eigene Verbindung abschliessen | verweigert: Anna ist nicht die Genehmigende | Rechte |
| G19 | Anna | `m365-eintritt` direkt bestellen | verweigert: nur die Applikation bestellt es | `orderableBy` |

Je Bestellung prüfen: Position lesen, die Aufgabe in der Liste der genehmigenden Person
finden und erledigen, den Fortschritt bis «gehalten» verfolgen, zuletzt
`atlas_order_line_outcomes` (Bereitstellung `completed`, kein neuer Incident).

### Phase 2: Tag 2, die Organisation in Atlas abbilden (`atlas-admin`)

| Fall | Schritt | Erwartung | Prüft |
|---|---|---|---|
| O1 | Objekt-IDs der Teams aus G2 bis G5 und des Verteilers G8 lesen | IDs notiert | |
| O2 | `M365OeMitgliedschaften` mit GL, PROJ, ADM und IT bereitstellen: GL → Geschaeftsleitung + Alle, PROJ → Projekte + Alle, ADM → Administration + Alle, IT → Alle; Verteiler Alle für alle OE | neue Version bereitgestellt; `atlas_try_decision` für `PROJ` liefert die IDs | Entscheidungstabelle |

### Phase 3: Woche 1, das Kernteam tritt ein

Das HR-System meldet acht Eintritte (01 bis 08). `atlas-admin` spielt das HR-System,
Carla die M365-Administration.

| Fall | Person | Schritt | Erwartung | Prüft |
|---|---|---|---|---|
| E1 | admin | je Person `atlas_trigger_start` auf `proc_m365_hr_eintritt`, Trigger `hr.eintritt`, mit Personalnummer, Name, OE, Variante `standard`, Vorgesetzter Max | acht Konten in Entra, je ein Eintrittspaket im Namen der Applikation | HR-Ereignis |
| E2 | Carla | Aufgaben «Initialpasswort» und «Zugangsdaten» je Person | Pakete gehalten; kein Passwort im Protokoll. Das Formular verlangt das Passwort als Eingabe, und über MCP stünde es im Chat: Der Agent erzeugt es lokal, legt es unter `out/testlauf/` ab und erledigt die Aufgabe über die REST-API, ohne den Wert auszugeben | Aufgaben der Fachstelle |
| E3 | admin | Mitgliedschaften lesen | jede Person in den Teams ihrer OE und im Verteiler Alle | OE-Zuordnung |
| E4 | admin | Lizenzbestand | acht Lizenzen mehr belegt, Vertrag G1 belastet | Lizenzmanagement |

### Phase 4: Woche 2, arbeitsfähig werden

| Fall | Person | Schritt | Erwartung | Prüft |
|---|---|---|---|---|
| A1 | Anna für Daniel | `m365-postfachzugriff` auf `HW<RUN> Info`, 365 Tage | Max genehmigt; Daniel hat Vollzugriff; Empfänger ist Daniel | Bestellen für |
| A2 | Anna für Martina | `m365-postfachzugriff` auf `HW<RUN> HR` | wie A1 | |
| A3 | Anna | G9 Info: `zugriff-erteilen` für Reto (Verkauf liest mit) | Berechtigung gesetzt | Kundenaktion |
| A4 | Carla | Claudias Eintrittslizenz: `stellvertretung` für Martina (Operator) | Martina ist Stellvertreterin für Claudias Kalender | Operator-Aktion |
| A5 | Anna | G14 Ferienplan: `berechtigung-erteilen` für alle (Verteiler) | Berechtigung gesetzt | |
| A6 | Anna | G3 Projekte: `kanal` `Kunde Muster AG` | Kanal angelegt | |
| A7 | Anna | G7 Projektablage: `gast` für einen Architekten (externe Adresse der Testdomäne) | Gast eingeladen | Gastzugang |
| A8 | Anna für Sandra | `ad-recht-fileshare-finanzen-lesen`, 30 Tage | Carla (AD-Administration) genehmigt; Mitglied der Berechtigungsgruppe | befristetes Recht |
| A9 | Anna | G16 Gruppe: `mitglied` Sandra, Luca, Nina; `verantwortung` an Sandra | Mitgliedschaft und Verantwortung in AD | |
| A10 | Anna | `ad-testkonto` für Marco | Konto in `OU=AtlasTest`; `entsperren` und `passwort` ausgeführt, kein Passwort im Protokoll | Aktionen mit Geheimnis |
| A11 | Beat | eine Aktion auf Annas Team G3 | verweigert, Beat ist weder Besteller noch Empfänger | Rechte |

### Phase 5: Woche 3, die Firma wächst

Ein Grossauftrag: ein zweites Projektteam und ein Lernender.

| Fall | Person | Schritt | Erwartung | Prüft |
|---|---|---|---|---|
| W1 | admin | Eintritte 09 bis 12 wie E1 und E2 | vier Konten, Pakete gehalten | |
| W2 | Anna | `m365-team` `HW<RUN> Projekt Bahnhof`, Besitzerin Elena | Max genehmigt; Team angelegt | |
| W3 | Anna | W2: `mitglied` Jonas und Lea | Mitglieder im Team | |
| W4 | Anna | G7 Projektablage: `kontingent` erhöhen | Kontingent geändert | |
| W5 | Anna | G1 Vertrag: `aufstocken` auf 20 | Kontingent höher | Lizenzvertrag |
| W6 | Carla | Marcos Eintrittslizenz: `wechseln` zu `erweitert` (Operator), sofern frei | Variante gewechselt | Operator-Aktion |
| W7 | Carla | W6 mit derselben `commandId` wiederholen | erste Antwort zurück, nichts doppelt | Idempotenz |
| W8 | Anna | `m365-raum` `HW<RUN> Werkstattbüro`, 6 Plätze, `Bern, Erdgeschoss` | Carla genehmigt | |

### Phase 6: Woche 4, Alltag und Veränderungen

| Fall | Person | Schritt | Erwartung | Prüft |
|---|---|---|---|---|
| M1 | Anna | siehe Phase 6b, Fall T4 | | |
| M2 | Anna | G9 Info: `zugriff-entziehen` für Reto | Berechtigung weg | |
| M3 | Anna | G8 Verteiler: `mitglied-entfernen` Luca | Luca nicht mehr Mitglied | |
| M4 | Anna | G5 Alle: `sichtbarkeit` privat | Team privat | |
| M5 | Anna | Beats Lizenz (falls im Shop gehalten, sonst übersprungen): `abwesenheit`, dann `abwesenheit-beenden` | Abwesenheitsnotiz an, dann aus | Kundenaktion des Empfängers |
| M6 | admin | `atlas_publish_message` `hr.austritt` für Luca (04) | Konto gesperrt, Paket zurückgegeben | Austritt |
| M7 | Anna | G12 Fokusraum zurückgeben (Umbau) | Rückbau `completed`, Raumpostfach weg | Rückgabe |
| M8 | – | entfällt: Atlas prüft Abhängigkeiten nur innerhalb einer Bestellung (siehe Grenzen). G9 zurückzugeben, solange A1 besteht, gelingt und löscht das Postfach; danach sind M2 und der Rückbau von A1 nicht mehr möglich | | |
| M9 | Anna | G7 Projektablage: `loeschen`, dann `wiederherstellen` | Site im Papierkorb, dann zurück | |

### Phase 6b: Informationstransfer

Quelle und Ziel eines Transfers sucht das Bestellformular im Active Directory, unter
`AD_PERSONEN_OU` (der Test-OU). Die Eintrittspersonen sind reine Cloud-Konten und die vier
Testpersonen bleiben unverändert; darum legt der Lauf seine Transferkonten selbst an, über die
AD-Dienste. Diese Konten haben kein Postfach: Die Postfach-Optionen (Adresse, Inhalt,
Kontingent) prüft der Lauf nur als Ablehnung (R5). Bestellt wird von Anna (Zielgruppe Identity
Management), genehmigt von Carla (`GRUPPE_IDENTITY_MANAGEMENT`).

| Fall | Person | Schritt | Erwartung | Prüft |
|---|---|---|---|---|
| T0 | Anna | `ad-externes-konto` `HW<RUN> Erika Extern` (Max genehmigt als Vorgesetzter des Sponsors), drei `ad-testkonto` `HW<RUN>-T1`, `HW<RUN>-T2`, `HW<RUN>-T3` mit Ablaufdatum in 30 Tagen; Initialpasswörter wie in E2, nie im Protokoll | vier Konten unter der Test-OU, in `atlas_shop_form_query` des Feldes `quelle` von `it-informationstransfer` auffindbar | Zusammenspiel der Kataloge |
| T0b | Anna | G16 Gruppe: `mitglied` Erika Extern | Erika ist Mitglied von `HW<RUN> Projekte` | Vorbereitung |
| T1 | Anna, Carla | Variante `internalisierung`: Quelle Erika Extern, Ziel T1; `optRechte` und `optKuerzel` = ja, alle anderen nein; Carla genehmigt | Genehmigung zeigt die Hinweise der Regel; T1 ist Mitglied von `HW<RUN> Projekte`; Kürzel auf T1; Quelle trägt «Ressourcen transferiert»; Position `done`, danach selbst zurückgegeben | Szenario |
| T2 | Anna, Carla | Variante `externalisierung`: Quelle T2, Ziel Erika Extern, `optRechte` = ja | wie T1 für die Rechte, ohne Telefonie-Aufgabe | Szenario |
| T3 | Anna, Carla | Variante `amtswechsel`: Quelle T3, Ziel T1, `optKuerzel` = ja | Kürzel übertragen, Quelle vermerkt | Szenario |
| T4 | Anna, Carla | Variante `abteilungswechsel` (früher M1): Quelle T1, Ziel T2, `optRechte` = ja | Rechte von T1 auf T2, Quelle vermerkt; `ENTRA_TRANSFER` bleibt aus | Szenario |
| T5 | Anna, Carla | `internalisierung` T2 → T3, `optRechte` = ja; Carla **lehnt ab** mit Begründung | Position abgelehnt, Anna liest die Begründung; an T3 nichts geändert | Genehmigung, Ablehnung |
| R1 | Anna | alle Optionen nein | sofort abgelehnt, Grund «Keine Ressource gewählt»; keine Genehmigung bei Carla | Regel |
| R2 | Anna | Quelle = Ziel (T3) | abgelehnt, Grund Quelle und Ziel gleich | Regel |
| R3 | Anna | `abteilungswechsel` T3 → T2 mit `optEntra` = ja | abgelehnt mit den Gründen «Entra nur bei Internalisierung» und «Entra ausgeschaltet» | Regel |
| R4 | Anna | `amtswechsel` Quelle Erika Extern (nach T1 vermerkt) → T3 | abgelehnt, Grund «schon transferiert» | Regel |
| R5 | Anna | `amtswechsel` T3 → T2 mit `optMail` = ja | abgelehnt, Grund «Kein Postfach» | Regel |

Je Ablehnung durch die Regel: Die Position endet abgelehnt, ohne Aufgabe bei Identity Management,
und `atlas_get_order` zeigt die Gründe. Die Freigabe der Informationssicherheit für einen
Entra-Transfer prüft der Lauf nicht: Sie kommt nur mit `ENTRA_TRANSFER = ein`, und das löscht in
dieser Umgebung ohne Entra Connect ein Cloud-Konto endgültig (siehe Grenzen).

### Phase 7: Rückbau und Abschluss

Nicht Teil eines echten Hochlaufs, aber Pflicht im Test: Die Umgebung ist danach wie vorher.

| Fall | Person | Schritt | Erwartung |
|---|---|---|---|
| Z1 | admin | `hr.austritt` für alle übrigen Personalnummern | Konten gesperrt, Pakete zurückgegeben, Lizenzen frei |
| Z2 | Anna | erst wenn jedes Eintrittspaket aus Z1 zurückgegeben ist (das Paket prüft alle fünf Minuten): jede noch gehaltene Position zurückgeben, abhängige zuerst (A1 und A2 vor G9, A8 vor G16, die Transferkonten aus T0 vor G16) | Rückbau `completed` |
| Z3 | Anna | G1 Vertrag: `senken` auf den Stand vor dem Lauf, dann zurückgeben | Kontingent wie vorher |
| Z4 | admin | `M365OeMitgliedschaften` auf die Version vor dem Lauf zurücksetzen | Platzhalter wieder aktiv |
| Z5 | admin | Incidents gegen V2, Aufgaben gegen V3 | keine neuen; sonst jede mit Instanz und Meldung im Protokoll |
| Z6 | admin | Restobjekte mit `HW<RUN>` oder `<RUN>-` | nur die Teams aus G2–G5 und W2, archiviert: Die Rückgabe eines Teams archiviert es, sie löscht es nicht. Sonst keine; jedes Restobjekt ins Protokoll für das Aufräumskript |

## Abdeckung

| Eigenschaft des Katalogs | Fälle |
|---|---|
| Zielgruppen und Sichtbarkeit | S1, S2, S3 |
| Genehmigung durch Vorgesetzten, Rolle, Fachstelle, keine | G2, G10, A8, G1, G8 |
| Ablehnung, Storno, Korrektur vor Genehmigung | G6, G12, G11 |
| Bestellen für eine andere Person | A1, A2, A8, A10 |
| Kundenaktionen | A3, A5–A7, A9, W3–W5, M2–M5, M9 |
| Operator-Aktionen und Idempotenz | A4, W6, W7 |
| Rechte: fremde Aufgabe, fremde Position, `orderableBy` | G18, A11, G19 |
| HR-Eintritt und -Austritt, OE-Tabelle | O2, E1–E3, W1, M6, Z1 |
| Lizenzmanagement | G1, E4, W5, Z3 |
| Active Directory | G16, A8–A10 |
| Exchange und SharePoint Server (On-Premises) | G15, G17 |
| Atlas hub: Applikation installieren | H1–H5 |
| Informationstransfer: vier Szenarien, Regel, Genehmigung und Ablehnung | T0–T5, R1–R5 |
| Rückgabe und Reihenfolge | M7, Z2 |

## Protokoll

Der Agent schreibt das Protokoll nach `deploy/opentofu/m365-devenv/out/testlauf/<RUN>.md`.
`out/` wird nie committet. Darin:

1. Kopf: `RUN`, Atlas-Version und Revision, Start und Ende in UTC, aktive Worker.
2. Tabelle aller Fälle: Kennung, Ergebnis, Dauer, Bestell- und Instanzschlüssel.
3. Je fehlgeschlagenem Fall: Schritt, erwartete und tatsächliche Antwort, Incident-Meldung,
   vermutete Ursache, als Vermutung gekennzeichnet.
4. Liste der Restobjekte, falls Phase 7 welche findet.

Im Chat nennt der Agent am Ende nur die Zusammenfassung und den Pfad.

## Vom Test zum Leitfaden

Die Phasen sind die Entscheidungen, die ein echtes KMU beim Hochlauf trifft. Wer Atlas für
ein Unternehmen einführt, kann sie als Leitfaden nehmen:

| Phase | Was das Unternehmen vorher entscheidet |
|---|---|
| 1 Grundausstattung | Namensregel (Präfix), welche Bereiche ein Team bekommen, öffentlich oder privat, wer Räume genehmigt, Lizenzbedarf für das erste Jahr |
| 2 Organisation | OE-Struktur, welche OE in welche Teams und Verteiler gehört |
| 3 Kernteam | Eintrittsdaten aus dem HR, Lizenzvariante je Funktion, wer Zugangsdaten übergibt |
| 4 arbeitsfähig | Funktionspostfächer und wer sie liest, Stellvertretungen der Geschäftsleitung, Gäste, Ablagerechte und ihre Frist |
| 5 Wachstum | wann ein Projekt ein eigenes Team bekommt, Lizenzreserve, Raumbedarf |
| 6 Alltag | Abteilungswechsel, Austritte, Aufräumen nicht mehr gebrauchter Objekte |

## Grenzen

- Was die Mitarbeitenden in Outlook, Teams oder im Explorer sehen, prüft der Lauf nicht. Er
  prüft Atlas und, wo ein Worker es liest, das Zielsystem. Das zeigt der Test von Hand
  ([TESTSZENARIO.md](TESTSZENARIO.md), Bürotag).
- `Set-Place` antwortet in dieser Umgebung mit HTTP 500; Ort und Stockwerk eines Raums
  prüft der Lauf darum nicht in Places.
- Nicht im Katalog dieser Umgebung und darum nicht im Lauf: Telefonie, Anrufwarteschlange,
  Telefonzentrale (kein Teams-Phone-Worker), Viva Engage, Aufbewahrung, hybride Identität.
- Informationstransfer: Die Transferkonten haben kein Postfach, darum prüft der Lauf Adresse,
  Inhalt und Kontingent nur als Ablehnung der Regel. Den Entra-Transfer und die Freigabe der
  Informationssicherheit prüft er nicht (`ENTRA_TRANSFER` bleibt `aus`, kein Entra Connect).
  Auch die Telefonnummer nicht: Der Teilprozess ruft zuerst Teams Phone auf und gibt erst danach
  der Telefonie eine Aufgabe. Ohne Teams-Phone-Worker wartet der Job ohne Incident, und der
  Transfer bleibt stehen.
- Gasteinladungen (A7) lehnt dieser Tenant ab, solange er nur ein Testabonnement hat
  (Graph 403 «Guest invitations not allowed for your company»); A7 scheitert darum hier.
- Der HR-Eintritt setzt in Entra keinen Vorgesetzten. Genehmigungen für Bestellungen, deren
  Empfänger über den Eintritt kam (A1, A2, W2), gehen darum an die Ersatzgruppe, und Carla
  genehmigt statt Max.
- Abhängigkeiten zwischen Bestellungen kennt Atlas nicht: `requires` gilt nur innerhalb
  einer Bestellung, und A1 nennt das Postfach als Text. Ein Funktionspostfach lässt sich darum
  zurückgeben, solange ein Zugriff darauf besteht; die Reihenfolge in Z2 hält der Agent ein.
- Marcos Lizenzwechsel (W6) legt eine zweite Position für dieselbe Lizenz an. Gibt Z1 das
  Eintrittspaket zurück, ist die Lizenz weg, und die Rückgabe der W6-Position scheitert mit
  Entra 400 «User does not have a corresponding license». Der Fall gehört ins Protokoll.
- Exchange Online braucht für `Remove-Mailbox` manchmal länger als die zwei Minuten des
  Workers und entfernt das Postfach trotzdem. Ein Worker vor dem Fix «A removal Exchange
  Online made after the worker stopped waiting counts as made» parkt die Rückgabe dann mit
  404 und hält den Leistungsschalter für alle Exchange-Jobs offen.
- Ein Lauf braucht je nach Exchange-Antwortzeiten drei bis fünf Stunden. Die VMs fahren um
  22:00 UTC herunter; ein Lauf sollte bis 16:00 UTC begonnen haben.

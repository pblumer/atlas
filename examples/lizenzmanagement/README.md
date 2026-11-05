# Lizenzmanagement – Microsoft-365-Lizenzverträge mit Kontingent, Verrechnung und Abo-Karenz

Atlas vergibt im Beispiel `m365-dienste` Microsoft-365-Lizenzen pro Person, führt aber kein
Kontingent. Dieses Paket ergänzt das: Jeder Kunde und jedes Projekt erhält einen
**Lizenzvertrag** mit PSP-Element und einer Menge pro SKU (E3, E5, F3), den die
Tenant-verantwortliche Person genehmigt. Jede Lizenzbestellung im Microsoft-365-Katalog zieht
davon ab; fehlt der Platz, wird nicht still überzogen. Täglich gleicht Atlas die Verträge mit
dem Tenant ab und sagt dem Chef, wann im EA nachzukaufen ist; monatlich verrechnet es die
bezogenen Lizenzen pro PSP-Element. Vor dem EA-Jahrestag bereitet es die neue Menge vor und
führt in der Abo-Karenz von 90 Tagen überzählige Lizenzen geordnet zurück, nach einer
Entzugsregel, die jeder Kunde als Entscheidungstabelle selbst bestimmt.

Das Paket ist eine eigene Applikation «Beispiel: Lizenzmanagement» (Schlüssel
`beispiel-lizenzmanagement`) mit eigenem Katalog. `m365-dienste` fragt bei jeder
Lizenzbestellung nur bei ihr nach, ob der Vertrag noch Platz hat (siehe «Anbindung an
M365-Dienste»).

## Inhalt

| Datei | Inhalt |
|---|---|
| `atlas.json` | Manifest der Applikation: 13 Prozesse, 22 Formulare, 9 Entscheidungen samt Modellen, Informationsmodell und Architektur (Format 4, ADR-0528) |
| `katalog.json` | Das Katalogdokument: zwei Kataloge, ein Produkt «Lizenzvertrag Microsoft 365» mit sieben Operationen |
| `fragen.json`, `antworten.beispiel.json` | Was die Platzhalter des Katalogs fragen (die beiden Zielgruppen), und eine Vorlage für die Antworten |
| `*.bpmn` | Die Prozesse, je mit Dokumentation an Prozess und Elementen |
| `lz-*.dmn` | Die neun Entscheidungstabellen |
| `lz-*.form.json` | Bestell-, Aktions-, Start- und Aufgabenformulare |
| `installieren.sh`, `installieren.ps1` | Installation: Entscheidungsmodelle, Applikation, Architektur, Veröffentlichung, Katalog, Tenant-Kontingent starten |
| `architektur/` | Informationsmodell, Realisierungen und Vorschläge für Capabilities und einen Wertstrom |

## Die Prozesse

Die Nummern P1–P9 sind die des Konzepts («Lizenzkontingente im EA mit Atlas», Tab
«Prozesse»).

| Konzept | Prozess | Datei | Auslöser | Wer handelt |
|---|---|---|---|---|
| P1 Lizenzvertrag bestellen | `proc_lz_vertrag` (provision) | `vertrag.bpmn` | Bestellung im Shop | Integrations Manager, Tenant-verantwortliche Person |
| P2 Lizenzvertrag ändern | `proc_lz_vertrag` (aufstocken, senken, verlaengern, deprovision, entzugsregel, verrechnung) | `vertrag.bpmn` | Aktion am Vertrag im Shop | Integrations Manager, Tenant-verantwortliche Person |
| – | `proc_lz_vertragsbuch` | `vertragsbuch.bpmn` | gestartet von P1 | automatisch: hält den Stand eines Vertrags |
| P3 Kontingent prüfen | `proc_lz_kontingent` | `kontingent.bpmn` | Call Activity aus P4 | automatisch; Überzug: Tenant-verantwortliche Person |
| – | `proc_lz_buchen` | `buchen.bpmn` | Call Activity jedes Prozesses, der bucht | automatisch: fragt ein Buch und wartet auf die Antwort |
| P4 Lizenz pro Person | `proc_m365_lizenz` | `../m365-dienste/lizenz.bpmn` | Bestellung, wechseln, zurückgeben | Person, vorgesetzte Person |
| P5 Bestand abgleichen | `proc_lz_bestand`, `proc_lz_bestand_aufgaben` | `bestand.bpmn`, `bestand-aufgaben.bpmn` | täglich 06:00 UTC | automatisch; Chef beim Nachkauf, Lizenzmanagement bei Abweichungen |
| – | `proc_lz_tenantbuch` | `tenantbuch.bpmn` | einmal nach der Installation | automatisch: hält das Tenant-Kontingent |
| P6 Verrechnung | `proc_lz_verrechnung`, `proc_lz_verrechnung_vertrag` | `verrechnung.bpmn`, `verrechnung-vertrag.bpmn` | am 1. des Monats 07:00 UTC | automatisch; Chef erhält die Übersicht |
| P7 Jahrestag vorbereiten | `proc_lz_jahrestag` | `jahrestag.bpmn` | P5, 90 Tage vor dem Jahrestag | Chef, Integrations Manager |
| P8 Rückführung in der Karenz | `proc_lz_karenz`, `proc_lz_karenz_vertrag` | `karenz.bpmn`, `karenz-vertrag.bpmn` | P7, wenn die neue Menge kleiner ist | automatisch; Lizenzmanagement bei Problemen |
| P9 Bestand übernehmen | `proc_lz_uebernahme` | `uebernahme.bpmn` | von Hand, einmal beim Start | Lizenzmanagement |

Entscheidungstabellen: `LzSku` (Variante bzw. SKU-ID → Kürzel E3/E5/F3), `LzPspPruefung`,
`LzKontingent`, `LzAbgleich`, `LzNachkaufSchwelle`, `LzPreise`, `LzExportForm`,
`LzEntzugStandard`, `LzKundeZuordnung`. Was in einer Tabelle als Beispielwert oder Vorschlag
steht (Preise, Schwellen, das Format des PSP-Elements, die Zuordnung zu Kunden, die
Standard-Entzugsregel), sagt ihr Kommentar; alles ist vor dem Einsatz zu ersetzen.

## Wo der Stand liegt: Vertragsbücher als lange laufende Instanzen

Die zentrale Frage dieses Pakets ist, wo Menge, belegt und reserviert eines Vertrags, die
Bezüge der Personen und der Tagesstand des Tenants über viele Instanzen hinweg liegen, und wie
eine Reservation sicher ist, wenn zwei Bestellungen gleichzeitig den letzten Platz wollen.

**Entschieden: eine lange laufende Instanz pro Vertrag, die ihren Stand selbst hält und
Buchungen als Nachrichten annimmt.** `proc_lz_vertragsbuch` startet, wenn ein Vertrag genehmigt
ist, und läuft, solange der Vertrag besteht. Der ganze Stand ist eine Variable `buch`:
Vertrag, Bezüge (Position im Shop, SKU, Objekt-ID des Kontos, von, bis), Reservationen und der
Verlauf der Änderungen. Belegt und reserviert sind gezählte Einträge, keine Zähler. Das
Tenant-Kontingent (`proc_lz_tenantbuch`) ist dasselbe für den Tenant: der Stand pro Tag und
SKU, der letzte Stand jedes Vertrags, die offenen Nachkäufe, der nächste Jahrestag.

Ein Buch kennt nur automatische Schritte: Es wartet an einem Nachrichtenereignis
(`lz.buch.anfrage`, Schlüssel Vertragsnummer bzw. `TENANT`), wendet die Buchung in **einer**
Script-Task an, antwortet und wartet wieder. Wer bucht, ruft `proc_lz_buchen` als Call Activity
auf: Es veröffentlicht die Anfrage über `POST /api/v1/messages` mit Lebensdauer und
`messageId` und wartet an einer Empfangsaufgabe auf `lz.buch.antwort` mit der Kennung der
Anfrage. Daraus folgt:

- **Race-frei.** Die Engine verarbeitet einen Befehl nach dem anderen, und eine Script-Task
  liest und schreibt in einem einzigen Befehl. Die Reservation prüft frei = Menge − belegt −
  reserviert und bucht in derselben Auswertung. Kommt eine zweite Anfrage, während das Buch
  noch die erste bearbeitet, wartet sie im Puffer (ADR-0370), bis das Buch wieder wartet. Der
  Test `TestTheContractBookGivesTheLastSeatOnce` schickt drei Anfragen auf zwei Plätze, bevor
  das Buch eine beantwortet hat: zwei erhalten einen Platz, eine «voll».
  `TestTwoOrdersAskingThroughTheHelperGetOneSeat` prüft dasselbe über den ganzen Weg mit
  Anfrage, Puffer und Antwort.
- **Event-sourced.** Jede Buchung ist eine Nachricht und ein Variablenereignis im Log; der
  Stand nach einem Neustart ist der Stand vor ihm. Was wann gebucht wurde, zeigt die Zeitachse
  der Instanz.
- **Kein Plattformbau.** Alles ist Modellierung mit Bausteinen, die Atlas hat.

Geprüfte Alternativen:

| Weg | Warum nicht |
|---|---|
| Datenobjekt oder Datenspeicher (ADR-0053, 0230, 0259) | Ein Datenobjekt lebt in einer Instanz; ein `dataStore` ist zur Laufzeit nur ein Name für Architektur und Informationsmodell, nicht lesbar oder schreibbar |
| Positionen und Bestand des Shops | Der Shop zählt Halter eines Produkts, listet sie aber nicht mit Variablen; ein Vertrag hätte keine Menge, gegen die geprüft wird |
| Jede Aktion des Produkts an die lange laufende Instanz liefern (ADR-0428, per-position) | Eine Genehmigung in derselben Instanz würde während Tagen alle Reservationen dieses Vertrags anhalten. Die Aktionen laufen deshalb als eigene Instanzen (per-operation, ADR-0425) und buchen im Buch |
| Variablen einer Instanz über `POST /api/v1/instances/{key}/variables` setzen | Rolle admin, eine Korrektur durch eine Person, kein Vergleichen-und-Setzen: zwei Bestellungen überschrieben sich |
| SharePoint-Liste (wie «Site-Inventar» in `m365-dienste`) | Kein Vergleichen-und-Setzen (kein ETag), ein Worker und eine Site mehr, der Stand ausserhalb von Atlas |
| Tabelle in einer Datenbank über den SQL-Worker | Das einzige echte atomare `UPDATE … WHERE frei`, aber ein System mehr; für ein Beispiel ohne Datenbank zu viel. Wer eine Datenbank hat, kann die Buchungen dort führen |
| Nachricht aus dem Modell werfen statt über die API | Eine geworfene Nachricht hat keine Lebensdauer (ADR-0370) und trägt alle Variablen der Instanz als Nutzlast; eine Antwort, die vor der Empfangsaufgabe eintrifft, ginge verloren |

Grenzen: Ein Buch hält seinen Stand in einer Variable (Grenze 1 MiB, ADR-0294); bei rund
150 Byte pro Bezug reicht das für einige Tausend Bezüge pro Vertrag. Eine lange laufende
Instanz bleibt auf ihrer Version; eine korrigierte Buchungslogik erreicht laufende Bücher erst
mit einer Migration (ADR-0162). Applikationskonstanten liest eine Instanz beim Start; deshalb
stehen Jahrestag und Ende der EA-Laufzeit im Tenant-Kontingent und nicht nur in den Konstanten.
Steht ein Buch mit einem Incident, erhalten seine Fragenden nach zehn Minuten `keine-antwort`.

## Anbindung an M365-Dienste (P4)

`m365-dienste/lizenz.bpmn` ist erweitert, aber nur aktiv mit der Applikationskonstante
**`LIZENZ_VERTRAG_PRUEFEN = ein`** in der Applikation M365-Dienste (Fallback `aus`): Ohne das
Lizenzmanagement bleibt `m365-dienste` installierbar und verhält sich wie vorher.

- **Bestellen:** Das Bestellformular `m365-lizenz-bestellung` fragt neben der Person den
  Lizenzvertrag (`lizenzvertrag`, leer: `LIZENZ_VERTRAG_STANDARD`). Vor dem Lizenzprofil ruft
  eine Call Activity `proc_lz_kontingent` (P3) auf; «abgelehnt» meldet die Position als
  fehlgeschlagen. Nach dem Zuweisen liest `get-user` die Objekt-ID, und `proc_lz_buchen` bucht
  die Belegung.
- **Zurückgeben:** Die Rückgabe entzieht die Variante, die das Konto hält (nach einem Wechsel
  nicht mehr die der Position), fragt das Tenant-Kontingent, in welchem Vertrag die Position
  belegt ist (`wo`), und gibt die Belegung dort frei.
- **Wechseln (neu):** Aktion «Lizenz wechseln» (`m365.lizenz.wechseln`, Formular
  `m365-lizenz-wechsel`): auf eine teurere Variante mit Genehmigung, gegen den Vertrag geprüft;
  im Modus `direkt` tauscht ein einziger `assign-license` die SKUs, die Belegung wird umgebucht.

Wie eine Applikation die andere aufruft: Eine Call Activity nennt den aufgerufenen Prozess über
seine id, und Atlas löst sie über alle Applikationen eines Servers auf. Fehlt der Prozess, gibt
es keinen Fehler beim Deployen; das Token bliebe zur Laufzeit stehen. Darum der Schalter. Der
Rückweg ist ein Shop-Befehl: P2 «senken» und «beenden» und P8 geben Lizenzen mit
`atlas:shopTask mode="command" product="m365-lizenz" action="deprovision"` über die Position im
Shop zurück, damit Entra, Bestand und Vertragsbuch in einem Weg nachgeführt werden.

## Die Kataloge

| Katalog | Rang | Zielgruppe | Produkte |
|---|---|---|---|
| Lizenzmanagement: Verträge (`cat-lz-kunden`) | 700 | `{{zielgruppe}}` (Integrations Manager der Kunden) | Lizenzvertrag Microsoft 365 |
| Lizenzmanagement: Betrieb (`cat-lz-betrieb`) | 710 | `{{tenant-verantwortung}}` | Lizenzvertrag Microsoft 365 |

Der Integrations Manager eines Vertrags ist die Person, für die er bestellt wird (das Feld «Für
wen» des Shops). Atlas hält sie als Kennung des Atlas-Kontos, nicht als Adresse: Mails löst der
Mail-Worker beim Senden auf, Aufgaben gehen an das Konto (ADR-0314).

## Applikationskonstanten

| Konstante | Fallback | Wofür |
|---|---|---|
| `ATLAS_API_BASE` | `http://127.0.0.1:8080` | Adresse der eigenen API für die REST-Tasks |
| `EA_JAHRESTAG` | `2027-07-01` | der nächste Jahrestag im EA (nur beim Start des Tenant-Kontingents gelesen) |
| `EA_ENDE` | `2029-06-30` | Ende der EA-Laufzeit; spätestes Vertragsende |
| `KARENZ_TAGE` | `90` | Abo-Karenz nach dem Jahrestag |
| `ENTZUG_VORLAUF_TAGE`, `KONTROLLE_TAGE` | `30`, `14` | Entzug und Kontrolle vor dem Ende der Karenz |
| `JAHRESTAG_VORLAUF_TAGE` | `90` | ab wann P5 die Vorbereitung des Jahrestags startet |
| `RESERVATION_TAGE` | `7` | nach wie vielen Tagen eine nicht belegte Reservation verfällt |
| `GENEHMIGUNG_FRIST`, `KORREKTUR_FRIST`, `UEBERZUG_FRIST` | `P7D`, `P10D`, `P3D` | Fristen der Aufgaben (Kalendertage statt Arbeitstage) |
| `GRUPPE_TENANT`, `GRUPPE_CHEF`, `GRUPPE_LIZENZMANAGEMENT`, `GRUPPE_FINANZEN` | `Lizenz-Tenantverantwortung`, `Lizenz-Chef`, `Lizenzmanagement`, `Lizenz-Finanzen` | wer die Aufgaben erhält und an wen Mails gehen |

In `m365-dienste`: `LIZENZ_VERTRAG_PRUEFEN` (`aus` / `ein`) und `LIZENZ_VERTRAG_STANDARD`.

Die Gruppen sind Konstanten, nicht Platzhalter des Katalogs: Ein Platzhalter steht nur im
Katalogdokument, und der Katalog braucht nur die beiden Zielgruppen.

## Worker und Berechtigungen

- **`ucc_entra`** (Entra): `list-subscribed-skus` (P5) braucht `LicenseAssignment.Read.All`;
  `get-user` mit `signInActivity` (P8) braucht `User.Read.All`, `AuditLog.Read.All` und
  Entra ID P1; `list-users` mit `assignedLicenses` (P9) `User.Read.All`.
- **Secret `atlas`:** ein API-Token der Rolle operator für die REST-Tasks (Nachrichten,
  Trigger, Positionen). Für eine eigene Entzugsregel eines Kunden zusätzlich die Rolle
  modeler: Eine Business Rule Task nennt ihre Entscheidung fest im Modell; die Tabelle, die erst
  der Vertrag nennt, wertet Atlas über `POST /api/v1/decisions/evaluate` aus.
- **`mail`:** der Mail-Worker für Bestätigungen, Hinweise und den Export.

## Installation

```sh
cp antworten.beispiel.json antworten.json   # Zielgruppen eintragen
ATLAS_TOKEN=atlasat_… ./installieren.sh https://atlas.intern.example.ch antworten.json
```

Die Schritte: die neun Entscheidungsmodelle hochladen, die Applikation als Quellbaum
importieren (Lücken der Capability-Landkarte mit `LANDKARTE=uebernehmen`, `weglassen` oder
`fragen`), veröffentlichen, den Katalog mit den Gruppen ausfüllen und importieren, das
Tenant-Kontingent starten. Danach die Konstanten setzen und in `m365-dienste`
`LIZENZ_VERTRAG_PRUEFEN=ein`. Zuerst einen Lizenzvertrag bestellen, dann Lizenzen.
Bestehende Lizenzen übernimmt P9 «Bestand übernehmen» (in der Console starten).

## Vereinfachungen gegenüber dem Konzept

- **P1/P2:** Fristen in Kalendertagen (7 statt 5 Arbeitstage); nach der Frist erhält der Chef
  einen Hinweis, die Aufgabe bleibt bei der Tenant-verantwortlichen Person. Eine abgelehnte
  Bestellung meldet die Position als fehlgeschlagen; die Positionsroute nimmt keinen Grund, er
  steht in der Instanz. «Neues Ende zulässig?» ist eine Bedingung statt einer Tabelle.
- **P3:** «Vertrag lesen» und «Platz reservieren» sind ein Schritt im Vertragsbuch, weil nur so
  nichts zwischen Prüfen und Buchen kommt; `LzKontingent` entscheidet danach den Weg.
- **P4:** Das Formular fragt die Vertragsnummer als Text; eine Auswahl der aktiven Verträge
  bräuchte eine Formularquelle, die Instanzen liest (ADR-0473 kennt nur Entra und AD). Die
  Genehmigung eines teureren Wechsels hat M365-Administration statt der vorgesetzten Person. In
  den Gruppenwegen ist der Wechsel eine Aufgabe. Schlägt nach der Reservation etwas fehl, wird
  die Reservation nicht sofort freigegeben; sie verfällt nach `RESERVATION_TAGE`.
- **P5:** Täglich 06:00 UTC (Cron), nicht in lokaler Zeit.
- **P6:** Am 1. des Monats statt am ersten Arbeitstag. Der Export steht im Text der Mail, weil
  der Mail-Worker keinen Anhang versendet. Ein Bezug, der in der Karenz noch läuft, wird bis zu
  seinem Ende verrechnet.
- **P7:** Eine unbeantwortete Aufgabe «Weiterführen erfragen» gilt nicht nach 14 Tagen von
  selbst als «beenden»; sie hat eine Fälligkeit, und der Chef entscheidet. Die Frist «30 Tage
  vor dem Jahrestag» steht im Formular, weil eine Fälligkeit nur eine Dauer ab dem Start sein
  kann.
- **P8:** Die betroffenen Personen erhalten keine eigene Mail; der Integrations Manager erhält
  die Liste. Eine Überbelegung zum Ende der Karenz ist ein Incident am Gateway «Überbelegung
  gelöst?», das erst weiterläuft, wenn jemand `ueberbelegungGeloest = true` setzt.
- **P9:** `LzKundeZuordnung` liest Organisationseinheit und Firma (`companyName`) statt der
  Domain der Adresse, die Teil des Anmeldenamens und damit ein Personendatum ist. Die
  Bestätigung je Kunde geht an das Lizenzmanagement, weil ein Kunde ohne Vertrag noch keinen
  Integrations Manager hat.
- **Rechte auf Entscheidungstabellen:** «Atlas gibt dem Integrations Manager nur die Tabelle
  seines eigenen Vertrags frei» setzt ein Recht pro Entscheidung voraus, das Atlas nicht hat.
  Offen; bis dahin pflegt die Tabelle, wer Modellierer ist.

## Personendaten

Kein Buch hält Namen, Anmeldenamen oder Adressen. Ein Bezug kennt die Position im Shop und die
Objekt-ID des Kontos in Entra ID; sie nennt ohne Zugriff auf das Verzeichnis niemanden und ist
deshalb nicht als Personendatum deklariert. Dasselbe gilt für die Merkmale, die P8 für die
Entzugsregel liest (Organisationseinheit, Funktion, Art des Kontos, letzte Anmeldung) und für
die Liste der Konten mit Lizenz in P9. Der Integrations Manager ist eine Kennung des
Atlas-Kontos.

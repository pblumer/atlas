# Testszenario «Helvetia Werkstatt AG»

Ein durchgehender Test der Beispielanwendung *M365-Dienste* in dieser Umgebung: Eine kleine,
erfundene Firma wird über den Shop aufgebaut, ihre Mitarbeitenden treten über das HR-Ereignis
ein, danach wird ein Bürotag durchgespielt. Das Szenario zeigt, ob die Produkte im
Zusammenspiel tragen, nicht nur einzeln.

Denselben Hochlauf der Firma führt ein Agent automatisch und mit allen Produkten über MCP
durch: [TESTLAUF-KATALOG.md](TESTLAUF-KATALOG.md). Dieser Test von Hand bleibt für das, was der
Agent nicht sieht: Outlook, Teams und den Explorer (Bürotag).

Bestellt wird im Shop als Testperson **Anna Muster**. Alle Objekte tragen den Präfix der
Umgebung (`atlas-test-<zufall>-`) und fallen damit unter die Aufräumskripte
(Abschnitt «Abbauen» im [README](README.md)). Alle Personen sind erfunden.

## Die Firma

| Person | Organisationseinheit | Rolle |
|---|---|---|
| Claudia Brunner | GL | Geschäftsleitung |
| Reto Keller | GL | Geschäftsleitung |
| Sandra Meier | PROJ | Projekte |
| Luca Bernasconi | PROJ | Projekte |
| Nina Frei | PROJ | Projekte |
| Martina Huber | ADM | Personal (HR) |
| Daniel Wyss | ADM | Empfang |
| Marco Steiner | IT | Informatik |

## Ablauf

### 1. Grundausstattung im Shop

| Schritt | Produkt | Angaben | Stand 2026-11-09 |
|---|---|---|---|
| Teams | `m365-team` | HW Geschaeftsleitung, HW Projekte, HW Administration (privat); HW Alle (öffentlich) | erledigt |
| Ablage | `m365-sharepoint-site` | HW Projektablage (`hw-projektablage`, de-CH, privat) | erledigt |
| Verteiler | `m365-verteiler` | HW/Alle, Besitzerin Anna, nur intern | offen |
| Funktionspostfächer | `m365-funktionspostfach` | ADM/Info und ADM/HR, Besitzerin Anna | offen |
| Räume | `m365-raum` (Katalog UCC) | «Grosser Saal», 12 Plätze, `Bern, 1. Stock`; «Fokusraum», 4 Plätze, `Bern, 2. Stock` | offen |

### 2. Zuordnung der Organisationseinheiten

Die Entscheidungstabelle `M365OeMitgliedschaften` (`m365-oe-mitgliedschaften.dmn`) enthält im
Paket Platzhalter. Für das Szenario bekommt sie die Einheiten GL, PROJ, ADM und IT mit den
Objekt-IDs der Teams und Gruppen aus Schritt 1:

| Einheit | Mitgliedschaften |
|---|---|
| GL | HW Geschaeftsleitung, HW Alle |
| PROJ | HW Projekte, HW Alle |
| ADM | HW Administration, HW Alle |
| IT | HW Alle |

### 3. Eintritte über das HR-Ereignis

Je Person ein Aufruf der Trigger-Route, wie ihn ein HR-System machen würde (Rolle `operator`):

```text
POST /api/v1/processes/proc_m365_hr_eintritt/triggers/hr.eintritt
```

Nutzlast laut Prozess: `triggerId`, `personalnummer`, `vorname`, `nachname`, `oe`,
`eintrittsdatum`, `variante` und optional `vorgesetzter` (UPN). Jeder Eintritt legt ein Konto
in Entra ID an und belegt eine Lizenz; es müssen also genügend frei sein.

Die Aufgaben zu Initialpasswort und Zugangsdaten erledigt ein Mitglied von
`M365-Administration`. Passwörter gehören nicht in Tickets oder Protokolle.

### 4. Zugriffe

| Produkt | Angaben |
|---|---|
| `m365-postfachzugriff` | Daniel Wyss auf ADM/Info |
| `m365-postfachzugriff` | Martina Huber auf ADM/HR |
| `m365-kalender` | Stellvertretung: Martina Huber für den Kalender von Claudia Brunner |
| Partnerzugang (`m365-engage`) | ein externer Gast |

### 5. Bürotag

Mit den Konten der Mitarbeitenden:

- einen Raum buchen und die Annahme durch das Raumpostfach sehen,
- eine Teams-Besprechung ansetzen,
- die Projektablage über OneDrive im Explorer synchronisieren,
- eine Abwesenheitsnotiz setzen.

### 6. Austritt und Aufräumen

`hr.austritt` mit der Personalnummer sperrt das Konto und gibt das Paket zurück. Die restlichen
Objekte entfernen die Aufräumskripte beim Abbau der Umgebung.

## Bekannte Grenzen dieser Umgebung

- `Set-Place` über die Exchange-REST-Schnittstelle antwortet immer mit HTTP 500. Darum steht
  die Konstante `ORTSANGABEN` hier auf `false` (Typ `json`); `install-m365-dienste.py` setzt
  alle Konstanten neu, sie muss danach wieder gesetzt werden. Ort und Stockwerk eines Raums
  erscheinen deshalb nicht in Places.
- Ein neuer Raum erscheint erst nach 24 bis 48 Stunden in der Raumsuche von Outlook.
- Die VMs fahren täglich um 22:00 UTC herunter und müssen vor dem Test gestartet werden.

# Bewerbermanagement — eine kleine Applikation 📦

Das durchgängige Beispiel aus dem Handbuch-Kapitel
**[„Werkstatt: eine kleine Applikation bauen"](/handbuch.html#werkstatt)**. Es
zeigt nicht ein BPMN-Muster, sondern wie ein **Prozess-Developer** aus mehreren
Artefakten *eine Applikation* baut: zwei Prozesse, drei Formulare, eine
DMN-Entscheidung — gemeinsam publiziert, gemeinsam versioniert.

Die anderen Beispiele im Repository zeigen je ein Element gut. Dieses zeigt, was
zwischen den Elementen passiert: den **Schnitt** (welcher Schritt gehört in
welches Artefakt), den **Datenvertrag** zwischen zwei Prozessen und die
**Fehlerpfade**, die ein Modell erst produktionstauglich machen.

## Die Artefakte

| Datei | Artefakt | Rolle in der Applikation |
|------|----------|--------------------------|
| [`bewerbung.bpmn`](bewerbung.bpmn) | Prozess `bw-bewerbung` | Hauptprozess: Eingang → Vorprüfung → Interviews → Entscheidung → Zusage/Absage |
| [`interview.bpmn`](interview.bpmn) | Prozess `bw-interview` | Wiederverwendbarer Teilprozess: **eine** Interviewrunde, aufgerufen als Call-Activity |
| [`vorpruefung.dmn`](vorpruefung.dmn) | Entscheidung `bw-vorpruefung` | Einladungsregeln: wer eingeladen wird und in **wie vielen Runden** |
| [`bewerbung-eingang.form.json`](bewerbung-eingang.form.json) | Formular `bw-bewerbung-eingang` | Start-Formular — zugleich der Datenvertrag des Prozesses |
| [`interview-feedback.form.json`](interview-feedback.form.json) | Formular `bw-interview-feedback` | Feedback je Interviewrunde |
| [`entscheidung.form.json`](entscheidung.form.json) | Formular `bw-entscheidung` | Einstellungsentscheidung mit Fazit aus allen Runden |

Alle Artefakte tragen das Präfix `bw-`. Prozess-, Formular- und Entscheidungs-Ids
leben pro Server in *einem* Namensraum; ein Präfix je Applikation ist die
billigste Kollisionsvermeidung, die es gibt.

## Der Hauptprozess

```
Start "Bewerbung eingegangen"          (Formular bw-bewerbung-eingang)
 → [Mockup] Eingangsbestätigung senden
 → [DMN]    Vorprüfung                 → pruefung {empfehlung, runden}
 → (X) Einladen?
       einladen → Interviews
       absagen (Default) ──────────────────┐
 → [Call, Multi-Instance seriell]           │  einmal je Eintrag in
      Interview durchführen → bw-interview  │  pruefung.runden,
      sammelt bewertungen                   │  sammelt je Runde eine Bewertung
 → [Script] Interviews auswerten → interviewFazit
 → 🧑 Einstellung entscheiden           (Formular bw-entscheidung)
      └─ Boundary-Timer P3D, nicht unterbrechend → Erinnerung senden
 → (X) Zusage?
       ja   → [Mockup] Vertrag im HR-System anlegen → Ende "Eingestellt"
                └─ Error-Boundary HR_UNERREICHBAR
                     → 🧑 Vertrag manuell anlegen → Ende "Manuell eingestellt"
       nein (Default) ──────────────────────┤
                                            └→ (X) → [Mockup] Absage senden
                                                       → Ende "Abgelehnt"

⟳ Event-Subprozess "Rückzug" (Nachricht bw-rueckzug, unterbrechend)
     kann JEDERZEIT eintreffen → Rückzug vermerken → Ende "Zurückgezogen"
```

## Was das Beispiel zeigt

| Thema | Wo im Modell | Die Lektion |
|-------|--------------|-------------|
| **Regel ≠ Ablauf** | `Vorprüfung` (DMN) statt Gateway-Kaskade | Was sich häufiger ändert als der Ablauf, gehört in eine Entscheidungstabelle, die der Fachbereich lesen kann |
| **Die Entscheidung steuert die Struktur** | DMN-Ausgabe `runden` (eine Liste) speist `inputCollection` | Eine dritte Interviewrunde einzuführen heisst: eine Zeile in der Tabelle ändern — nicht das Modell |
| **Zerlegen** | `Interview durchführen` als Call-Activity auf `bw-interview` | Ein eigenständig deployter Prozess ist einzeln testbar, einzeln versionierbar und von mehreren Aufrufern nutzbar |
| **Datenvertrag** | `propagateAll…="false"` + `ioMapping` | Hinein gehen `name`, `stelle`, `runde`; heraus kommt `bewertung`. Alles andere ist Privatsache des Teilprozesses |
| **Jeder Pfad erfüllt den Vertrag** | `Ohne Feedback bewerten` im Teilprozess | Auch der Fristablauf schreibt eine `bewertung` — sonst fehlt dem Aufrufer ein Listeneintrag |
| **Umsysteme simulieren** | fünf `atlas:mockupConnector`-Tasks (vier im Haupt-, einer im Teilprozess) | Die Applikation läuft end-to-end, bevor eine einzige echte Anbindung existiert (ADR-0120) |
| **Fehler sind Fachlichkeit** | `failRate` + Error-Boundary → „Vertrag manuell anlegen" | Ein ausgefallenes Umsystem wird zu einer Aufgabe für einen Menschen, nicht zu einem Incident |
| **Erinnern statt abbrechen** | Boundary-Timer, `cancelActivity="false"` | Nicht unterbrechend, weil eine offene Bewerbung nicht verfallen darf |
| **Was jederzeit passieren kann** | Event-Subprozess `Rückzug` | Ein Ereignis ohne festen Platz im Ablauf hängt am ganzen Prozess, nicht an einem Schritt |
| **Scope-Regeln** | `ioMapping`-Output am Event-Subprozess | Was ein Subprozess schreibt, verschwindet mit ihm — nur eine Output-Zuordnung hebt es heraus (ADR-0074) |

## Installieren und laufen lassen

Der bequemste Weg ist der Knopf **„Applikation installieren"** im Handbuch unter
[Werkstatt](/handbuch.html#werkstatt): er legt Applikation, Formulare,
Entscheidung und beide Prozesse an und publiziert sie in einem Rutsch.

Von Hand über die REST-API — dieselbe Reihenfolge, die auch der Knopf geht:

```bash
BASE=http://localhost:8080/api/v1

# 1. Die Applikation — die Klammer um alles Weitere
APP=$(curl -sX POST $BASE/applications -H 'Content-Type: application/json' \
        -d '{"name":"Bewerbermanagement"}' | jq -r .id)

# 2. Die DMN-Entscheidung: Modell hochladen, Referenz in die Applikation legen
curl -sX POST "$BASE/dmn-models?name=bw-vorpruefung" \
     -H 'Content-Type: application/xml' --data-binary @vorpruefung.dmn
curl -sX POST $BASE/dmnrefs -H 'Content-Type: application/json' \
     -d "{\"name\":\"bw-vorpruefung\",\"modelRef\":\"bw-vorpruefung\",\"projectId\":\"$APP\"}"

# 3. Die Formulare (schema = der Inhalt der .form.json, id/name daraus)
curl -sX POST $BASE/forms -H 'Content-Type: application/json' \
     -d "{\"id\":\"bw-bewerbung-eingang\",\"projectId\":\"$APP\",\"schema\":$(cat bewerbung-eingang.form.json)}"
# … ebenso bw-interview-feedback und bw-entscheidung

# 4. Die beiden Prozesse als Entwürfe der Applikation
curl -sX POST "$BASE/drafts?projectId=$APP" \
     -H 'Content-Type: application/xml' --data-binary @bewerbung.bpmn
curl -sX POST "$BASE/drafts?projectId=$APP" \
     -H 'Content-Type: application/xml' --data-binary @interview.bpmn

# 5. Publizieren: alles zusammen deployen und als Release festhalten
curl -sX POST $BASE/applications/$APP/publish \
     -H 'Content-Type: application/json' -d '{"note":"Erste Version"}'
```

Eine Instanz starten (der Key kommt aus der Publish-Antwort):

```bash
curl -sX POST $BASE/processes/<KEY>/instances -H 'Content-Type: application/json' -d '{
  "variables": {"name":"Ada Lovelace","email":"ada@example.org",
                "stelle":"Software Engineer","abschluss":"master","erfahrungJahre":6}
}'
```

Mit `abschluss: "master"` und sechs Jahren Erfahrung entscheidet die DMN-Tabelle
auf **zwei** Runden: im [Posteingang](/#/tasks) wartet nacheinander zweimal
„Feedback erfassen", danach „Einstellung entscheiden". Mit `abschluss: "keiner"`
läuft die Bewerbung direkt in die Absage.

Den Rückzug auslösen — jederzeit, solange die Instanz läuft:

```bash
curl -sX POST $BASE/messages -H 'Content-Type: application/json' \
     -d '{"name":"bw-rueckzug","correlationKey":"ada@example.org"}'
```

> **„Vertrag im HR-System anlegen" schlägt in etwa jedem fünften Lauf fehl.** Das
> ist Absicht (`failRate="0.2"`): dann übernimmt die Error-Boundary und die
> Aufgabe „Vertrag manuell anlegen" landet im Posteingang. Für einen
> vorführsicheren Lauf `failRate` auf `0` setzen.

## Weiterbauen

- **Echte Anbindungen** statt Mockups: `Eingangsbestätigung senden` und
  `Absage senden` an einen Mail-Connector hängen, `Vertrag im HR-System anlegen`
  an einen REST-Connector. Es ändert sich je ein Element — der Rest des Modells
  bleibt, wie er ist.
- **Eine dritte Runde** für Führungsstellen: eine Zeile in `vorpruefung.dmn`.
- **Absagen mit Frist**: einen Timer vor `Absage senden`, damit keine Absage
  innerhalb einer Stunde nach dem Gespräch rausgeht.
- **Anschluss ans Onboarding**: `Eingestellt` startet den Prozess aus
  [`examples/onboarding`](../onboarding/) — dort fängt der nächste Lebenszyklus an.

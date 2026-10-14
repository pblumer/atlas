# Lehrgang: Mitarbeitereintritt — der Leitfall 🎓

Der **Leitfall des Lehrgangs für die Prozessentwicklung**: ein einziger,
alltäglicher Prozess — ein Mitarbeitereintritt —, an dem alle sechs
Referenzmodule hängen. Wo die anderen Beispiele je ein Element gut zeigen und
die [Werkstatt](../bewerbermanagement/) zeigt, wie man *eine Applikation baut*,
zeigt dieser Fall, wie sich **dieselbe Applikation durch alle sechs Module
zieht**: Modellieren, Testen, Ausliefern, Weiterentwickeln, ein eigener Worker,
und Überwachen.

Der ausführliche, modulweise Durchgang steht im Handbuch unter
**[Leitfall: ein Prozess durch alle Module](/handbuch.html#leitfall)**. Diese
Datei ist die Quelle der Artefakte und die Referenz für den Worker.

## Die Artefakte

| Datei | Artefakt | Rolle in der Applikation |
|------|----------|--------------------------|
| [`eintritt.bpmn`](eintritt.bpmn) | Prozess `lg-eintritt` | Hauptprozess: Meldung → Ausstattung → Konto → Karte/Paket → Arbeitsplatz → Bestätigung |
| [`ausstattung.dmn`](ausstattung.dmn) | Entscheidung `lg-ausstattung` | Ausstattung & Zugriffe je Rolle (Ausgabe ist ein Kontext) |
| [`lg-eintritt-start.form.json`](lg-eintritt-start.form.json) | Formular `lg-eintritt-start` | Start-Formular — zugleich der Datenvertrag |
| [`lg-arbeitsplatz.form.json`](lg-arbeitsplatz.form.json) | Formular `lg-arbeitsplatz` | Arbeitsplatz vorbereiten (IT) |
| [`lg-bestaetigung.form.json`](lg-bestaetigung.form.json) | Formular `lg-bestaetigung` | Ersten Tag bestätigen (HR) |

Alle Artefakte tragen das Präfix `lg-`. Prozess-, Formular- und
Entscheidungs-Ids leben pro Server in *einem* Namensraum; ein Präfix je
Applikation ist die billigste Kollisionsvermeidung, die es gibt.

## Der Prozess

```
Start "Eintritt gemeldet"              (Formular lg-eintritt-start)
 → [DMN]     Ausstattung & Zugriffe    → ausstattung {laptop, software, adGruppen, zutritt}
 → [Job]     Konto anlegen             ← EIGENER WORKER (Job-Typ konto-anlegen, retries=2)
 → [Mockup]  Zutrittskarte bestellen
 → [Mockup]  Willkommenspaket senden
 → 🧑 Arbeitsplatz vorbereiten          (Formular lg-arbeitsplatz, Gruppe it)
      └─ Boundary-Timer P2D, nicht unterbrechend → Erinnerung (Mockup) → Ende "Erinnert"
 → 🧑 Ersten Tag bestätigen             (Formular lg-bestaetigung, Gruppe hr)
 → Ende "Eingetreten"
```

Bewusst schlank: Genau **ein** Schritt ist kein Mockup, sondern ein echter Job
über die Job-API — «Konto anlegen». Das ist der Lehrpunkt von Modul 5, und
zugleich der Einstieg in Modul 6: scheitert der Worker wiederholt, parkt der Job
einen Incident.

## Die sechs Module am Leitfall

| Modul | Wo im Leitfall | Referenzkapitel |
|-------|----------------|-----------------|
| **1 Modellieren** | Der Prozess selbst: Start-Formular, DMN-Aufruf, Job-Task, zwei Human-Tasks, nicht unterbrechender Boundary-Timer | [Designen](/handbuch.html#designen) |
| **2 Testen** | Ein Playground-Szenario über `rolle` → `ausstattung`, mit dem Auffangfall als „einmal rot" | [Testen & Simulieren](/handbuch.html#szenarien) |
| **3 Ausliefern** | Applikation `lehrgang` publizieren, als Release an ein zweites Ziel promoten | [Ausliefern](/handbuch.html#ausliefern) |
| **4 Weiterentwickeln** | Eine Rolle ergänzen (eine Zeile in `ausstattung.dmn`), neue Version, laufende Instanz migrieren | [Weiterentwickeln](/handbuch.html#weiterentwickeln) |
| **5 Eigener Worker** | Der Job `konto-anlegen`: aktivieren, abschliessen, fehlschlagen — Idempotenz über den Job-Key | [Formulare & Worker](/handbuch.html#eigener-worker) |
| **6 Überwachen** | Zwei Fehlversuche des Workers → Incident; `/metrics` und `GET /api/v1/incidents` | [Betrieb & Incidents](/handbuch.html#ueberwachen) |

## Installieren und laufen lassen

Der bequemste Weg ist der Knopf **„Applikation installieren"** auf der
Beispiel-Karte im Handbuch unter [Beispiele](/handbuch.html#bsp-lehrgang): er legt
Applikation, Formulare, Entscheidung und Prozess an und publiziert sie in einem
Rutsch.

Von Hand über die REST-API — dieselbe Reihenfolge, die auch der Knopf geht:

```bash
BASE=http://localhost:8080/api/v1

# 1. Die Applikation
APP=$(curl -sX POST $BASE/applications -H 'Content-Type: application/json' \
        -d '{"name":"lehrgang"}' | jq -r .id)

# 2. Die DMN-Entscheidung: Modell hochladen, Referenz in die Applikation legen
curl -sX POST "$BASE/dmn-models?name=lg-ausstattung" \
     -H 'Content-Type: application/xml' --data-binary @ausstattung.dmn
curl -sX POST $BASE/dmnrefs -H 'Content-Type: application/json' \
     -d "{\"name\":\"lg-ausstattung\",\"modelRef\":\"lg-ausstattung\",\"projectId\":\"$APP\"}"

# 3. Die Formulare (schema = der Inhalt der .form.json, id daraus)
for f in lg-eintritt-start lg-arbeitsplatz lg-bestaetigung; do
  curl -sX POST $BASE/forms -H 'Content-Type: application/json' \
       -d "{\"id\":\"$f\",\"projectId\":\"$APP\",\"schema\":$(cat $f.form.json)}"
done

# 4. Der Prozess als Entwurf der Applikation
curl -sX POST "$BASE/drafts?projectId=$APP" \
     -H 'Content-Type: application/xml' --data-binary @eintritt.bpmn

# 5. Publizieren: alles zusammen deployen und als Release festhalten
curl -sX POST $BASE/applications/$APP/publish \
     -H 'Content-Type: application/json' -d '{"note":"Erste Version"}'
```

Eine Instanz starten (der Key kommt aus der Publish-Antwort, `definitions[].key`):

```bash
curl -sX POST $BASE/processes/<KEY>/instances -H 'Content-Type: application/json' -d '{
  "variables": {"name":"Ada Lovelace","personalnummer":"P-1001",
                "rolle":"entwicklung","standort":"zuerich","eintrittsdatum":"2026-11-01"}
}'
```

Die DMN entscheidet auf **Entwicklung** und legt `ausstattung` an: MacBook Pro 16,
`["IDE","Docker","VPN"]`, AD-Gruppen `["dev","git","vpn"]`, Zutritt bis Serverraum.
Dann **parkt** der Job `konto-anlegen` und wartet auf einen Worker. Eine andere
Rolle — oder eine unbekannte, die in den Auffangfall läuft — ergibt eine andere
Ausstattung: eine Zeile in `ausstattung.dmn`.

## Der eigene Worker: „Konto anlegen"

Ohne Worker bleibt der Job stehen — genau das ist der Ausgangspunkt von Modul 5.
Ein Worker least den Job, legt das Konto an und schliesst ihn ab. **Idempotenz
liegt beim Worker**: derselbe Job-Key (oder dieselbe `personalnummer`) darf kein
zweites Konto anlegen — die Zustellung ist mindestens einmal.

### Sprachneutral über die HTTP-Job-API

```bash
BASE=http://localhost:8080/api/v1

# 1. Einen Job leasen
JOB=$(curl -sX POST $BASE/jobs/activate -H 'Content-Type: application/json' \
        -d '{"type":"konto-anlegen","worker":"konto-worker","maxJobs":1}')
KEY=$(echo "$JOB"   | jq -r '.jobs[0].jobKey')
LEASE=$(echo "$JOB" | jq -r '.jobs[0].leaseToken')
PNR=$(echo "$JOB"   | jq -r '.jobs[0].variables.personalnummer')
GRUPPEN=$(echo "$JOB" | jq -c '.jobs[0].variables.adGruppen')

# 2. Die eigentliche Arbeit: das Konto anlegen (hier nur simuliert)
KONTO="AD-$PNR"

# 3a. Erfolg: abschliessen und ein Ergebnis zurückgeben
curl -sX POST $BASE/jobs/$KEY/complete -H 'Content-Type: application/json' \
     -d "{\"worker\":\"konto-worker\",\"leaseToken\":$LEASE,\"variables\":{\"kontoId\":\"$KONTO\"}}"

# 3b. Fehlschlag: mit verbleibenden Versuchen (>0 wird neu geparkt, 0 → Incident)
# curl -sX POST $BASE/jobs/$KEY/fail -H 'Content-Type: application/json' \
#      -d "{\"worker\":\"konto-worker\",\"leaseToken\":$LEASE,\"retries\":0,\"message\":\"AD nicht erreichbar\"}"
```

Der `leaseToken` ist der Fechtstempel (fencing token): Steigt er, hat ein anderer
Worker den Job übernommen, und ein `complete` mit dem alten Token wird abgelehnt.

### Als Go-Programm

```go
package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
)

const base = "http://localhost:8080/api/v1"

type job struct {
	JobKey     int64          `json:"jobKey"`
	LeaseToken int64          `json:"leaseToken"`
	Variables  map[string]any `json:"variables"`
}

func post(path string, body, out any) error {
	b, _ := json.Marshal(body)
	resp, err := http.Post(base+path, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func main() {
	var res struct {
		Jobs []job `json:"jobs"`
	}
	if err := post("/jobs/activate", map[string]any{
		"type": "konto-anlegen", "worker": "konto-worker", "maxJobs": 1,
	}, &res); err != nil {
		log.Fatal(err)
	}
	for _, j := range res.Jobs {
		pnr, _ := j.Variables["personalnummer"].(string)
		// Die eigentliche Arbeit: ein Konto für pnr mit j.Variables["adGruppen"] anlegen.
		// Idempotent: derselbe Job-Key darf kein zweites Konto anlegen.
		kontoID := "AD-" + pnr
		path := "/jobs/" + strconv.FormatInt(j.JobKey, 10) + "/complete"
		if err := post(path, map[string]any{
			"worker": "konto-worker", "leaseToken": j.LeaseToken,
			"variables": map[string]any{"kontoId": kontoID},
		}, nil); err != nil {
			// Bei einem Fehler stattdessen /fail mit retries: 0 → Incident.
			log.Printf("complete %d: %v", j.JobKey, err)
		}
	}
}
```

Ein echter Worker läuft in einer Schleife, aktiviert mehrere Jobs je Runde,
erneuert seinen Lease vor Ablauf und meldet Fehler mit `retries: 0` erst, wenn
sie dauerhaft sind. Die vollständige Mechanik — `activate`, `complete`, `fail`,
Lease, Idempotenz — steht im Handbuch unter
[Formulare & Worker](/handbuch.html#eigener-worker).

## Szenario je Modul (Kurzform)

- **Modul 2 – Testen:** Ein Playground-Szenario über den Datensatz `rolle`
  (entwicklung, vertrieb, produktion, verwaltung, ein Unbekannter). Stubs
  beantworten den Job `konto-anlegen` und die Human-Tasks, damit jeder Fall zu
  Ende läuft. Regel je Fall, z. B. `when rolle = "vertrieb" then
  ausstattung.laptop = "ThinkPad X1"`; Erwartung *no incidents* und *every case
  finishes*. Den Auffangfall einmal **rot** sehen: die Regel absichtlich falsch
  schreiben und prüfen, dass sie anschlägt.
- **Modul 5 – Eigener Worker:** Eine Instanz mit `rolle: "entwicklung"` starten,
  den Job leasen und abschliessen (oben). Dann zweimal `fail` mit `retries: 0` →
  Incident.
- **Modul 6 – Überwachen:** `GET /api/v1/incidents` listet den geparkten Fall;
  `atlas_open_incidents` in `/metrics` zählt ihn.

## Weiterbauen

- **Echte Anbindung** statt Mockup: `Zutrittskarte bestellen` und
  `Willkommenspaket senden` an einen Mail- oder REST-Worker hängen. Es ändert
  sich je ein Element — der Rest bleibt.
- **Eine neue Rolle**: eine Zeile in `ausstattung.dmn` — der Stoff von Modul 4.
- **Ein Genehmigungsschritt** vor „Konto anlegen" für privilegierte Rollen.

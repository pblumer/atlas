# Lasttest server01 — Ergebnisbericht

**Datum:** 2026-08-13
**Server:** Atlas `0.1.0-dev` (self-view `127.0.0.1:8080`, öffentlich hinter `atlas.blumer.cloud`)
**Zugang:** ausschließlich über die Atlas-MCP-Tools (`atlas_deploy`, `atlas_create_instance`, …)
**Verdikt:** bestanden — 0 Incidents durch den Test, server01 blieb durchgehend ruhig.

> Dies ist Revision 2 des Berichts. Eine in Revision 1 berichtete Durchsatzzahl
> auf Element-Ebene war falsch und ist zurückgezogen — siehe
> [§5 Transparenz & Korrektur](#5-transparenz--korrektur).

Ziel des Tests: zeigen, dass die Engine (a) alle wesentlichen BPMN-Element-Typen
interaktiv korrekt ausführt und (b) grössere Mengen an Prozess-Instanzen schnell
und stabil verarbeitet.

---

## 1. Kennzahlen

| Kennzahl | Wert |
|---|---|
| Abgeschlossene Kind-Prozess-Instanzen (`loadtest-child`) | **12.070** (10.000 in einem Durchgang) |
| Serverseitiger Durchsatz | **~500 Prozess-Instanzen/s** (2.000 in ~4 s, durable journalisiert) |
| Interaktive Abdeckung | **13 / 13** Instanzen durch alle Element-Typen, 0 Fehler |
| Auto-durchlaufende Instanzen (`loadtest-throughput`) | **687** |
| Neue Incidents durch den Test | **0** |

---

## 2. Volumen — 10.000 Prozess-Instanzen aus einem Aufruf

Statt tausende Instanzen einzeln über MCP zu starten (jeder Call kostet ~1,5 s
Transport, siehe §4), erzeugt **ein** Create-Aufruf über verschachtelte
Multi-Instance-**Call-Activities** zehntausend echte Kind-Prozess-Instanzen:

```
1 Top-Instanz  →  40 Mid-Instanzen  →  2.000 Kind-Instanzen     (× 5 Läufe = 10.000)
loadtest-fan-top   loadtest-fan-mid     loadtest-child
(MI sequenziell)   (MI parallel, je 50)  (trivialer Prozess)
```

- Jede `loadtest-child`-Instanz ist ein vollwertiger, durable persistierter
  Prozess (Start → Script-Task → Ende).
- Die Staffelung (Top **sequenziell** → Mid **parallel**) begrenzt die
  Nebenläufigkeit bewusst auf **≈ 50 gleichzeitig**, um den geteilten Server zu
  schonen ("nicht umbringen").
- **Belegt** über den Element-Visit-Count der Engine: `loadtest-child` →
  `cstart = 12.070`. Nach jedem Lauf fiel die Zahl aktiver Instanzen zurück auf
  die Hintergrund-Baseline; **0 Incidents**.

| Messung | Wert | Anmerkung |
|---|---|---|
| Kind-Instanzen erzeugt | 10.000 | 5 × 2.000 |
| Serverzeit je 2.000 | ~4 s | ≈ 500 Prozess-Instanzen/s |
| End-to-End je 2.000 | ~5,5 s | inkl. ~1,5 s MCP-Transport |
| 10.000 gesamt (Wanduhr) | ~42 s | 5 blockierende Aufrufe |
| Peak Nebenläufigkeit | ~50 | bewusst gedrosselt |

---

## 3. Interaktive Abdeckung — alle Element-Typen

13 `loadtest-allround`-Instanzen (1 Smoke + 12 nebenläufig) wurden komplett
durch den Prozess getrieben: Roboter schließen die User-Tasks
(`atlas_complete_task`), ein Worker die Service-Jobs (`atlas_complete_job`).
Bis zu **24 gleichzeitige** Completions in einer Welle.

Reale Element-Visit-Counts der Engine (13 = 12 Last + 1 Smoke):

| Element | Typ | Visits |
|---|---|---|
| `start` / `t_validate` / `t_reserve` / `t_credit` / `t_notify` / `end` | Start / Service / Service / User / Service / End | 13 |
| `gw_fork` | Parallel-Split | 13 |
| `gw_join` | Parallel-Join | **26** (2 Tokens/Instanz sauber zusammengeführt) |
| `t_approve` | User-Task (Betrag ≥ 1000) | 7 |
| `t_autoapprove` | Service-Task (Betrag < 1000) | 6 |

Der Exclusive-Split trennt exakt (7 + 6 = 13) nach Betrag — inklusive der
Grenzfälle `amount = 1000 → manuell` und `amount = 999 → auto`.

---

## 4. Wo die Zeit hingeht — Transport, nicht Engine

Jeder MCP-Aufruf über HTTP kostet konstant **~1,5 s**. Ein trivialer Prozess
(nur Gateways, `loadtest-throughput`) brauchte end-to-end **1,59 s** — praktisch
reine Netzwerk-Rundreise. Erst bei echter Arbeit (2.000 Kind-Instanzen)
überwiegt die Serverzeit (~4 s + ~1,5 s Transport).

**Konsequenz:** Über LLM-Agenten-über-MCP lässt sich der Server nicht schnell
genug *füttern*, um seine echte Grenze zu finden. Für einen echten Grenztest
(100k+, maximale Nebenläufigkeit) bräuchte es einen HTTP-nahen Lastgenerator
direkt auf server01.

---

## 5. Transparenz & Korrektur

**Zurückgezogen (aus Report-Revision 1):** eine Behauptung von „20.000 Elemente
in einem Prozess in ~28 ms ≈ 704.000 Element-Instanzen/s".

Diese Zahl beruhte auf einem Multi-Instance-Prozess, dessen Eingabe-Kollektion
per FEEL erzeugt wurde:

```feel
= for i in 1..count return i
```

Dieser Ausdruck liefert bei diesem Atlas-Build **`null`** statt einer Liste. Die
Multi-Instance lief damit **null-mal**; die gemessenen ~28 ms waren Leerlauf.
Die Zahl ist ungültig.

**Lektion / Verifikationsmethode:** Multi-Instance-Iteration nie aus einem
Zeitmesswert ableiten, sondern gegen einen beobachtbaren Effekt prüfen —
`outputCollection` (Ergebnisliste hat N Einträge) und Element-Visit-Counts. Der
Fix in diesem Test: die Kollektion als echte Daten übergeben (literale Liste im
Modell bzw. `items`-Start-Variable) statt sie per FEEL-Range zu generieren.
Erst nach bestätigter Iteration (`cstart = 12.070`) wurden Zahlen berichtet.

**Unverändert gültig** (echt ausgeführt, per Visit-Count/Timeline belegt, vom
MI-Fehler nicht betroffen): die interaktive Abdeckung (§3), die 687
auto-durchlaufenden Instanzen und der ~1,5-s-Transport-Boden (§4).

---

## 6. Aufbau & Reproduktion

Modelle in [`bpmn/`](bpmn/):

| Datei | Prozess-ID | Rolle |
|---|---|---|
| `loadtest-allround.bpmn` | `loadtest-allround` | Service + User-Tasks, Parallel-Fork/Join, Exclusive-Gateway |
| `loadtest-throughput.bpmn` | `loadtest-throughput` | läuft beim Start vollautomatisch durch |
| `loadtest-child.bpmn` | `loadtest-child` | triviale Kind-Prozess-Instanz (die gezählten) |
| `loadtest-fan-mid.bpmn` | `loadtest-fan-mid` | MI-Call-Activity → 50 Kinder |
| `loadtest-fan-top.bpmn` | `loadtest-fan-top` | sequenzielle MI-Call-Activity → 40 × 50 = 2.000 Kinder |

**Reproduktion** (über die Atlas-MCP-Tools bzw. die HTTP-API):

1. Deployen: `loadtest-child` zuerst, dann `loadtest-fan-mid`, dann
   `loadtest-fan-top` (Call-Activities binden die jeweils neueste Version des
   gerufenen Prozesses).
2. Volumen: `loadtest-fan-top` starten → 2.000 Kind-Instanzen. Für 10.000
   fünfmal starten.
3. Interaktiv: `loadtest-allround` mit Start-Variablen `{"amount": <n>}` starten;
   User-Tasks per `atlas_complete_task`, Service-Jobs per `atlas_complete_job`
   abschließen. `amount >= 1000` → manueller Genehmigungszweig, sonst
   Auto-Freigabe.
4. Verifizieren: `atlas_process_runtime <key>` (Visit-Counts),
   `atlas_instance_variables <key>` (z. B. `results`),
   `atlas_list_incidents`, `atlas_stats`.

**Hinweis:** `= for i in 1..count return i` **nicht** verwenden — liefert `null`.
Kollektionen als echte Daten übergeben.

---

## 7. Aufräumen

Alle Test-Prozess-Definitionen wurden nach dem Test wieder vom Server entfernt
(`atlas_delete_process`), inklusive ihrer completed-History. Fremd-Prozesse
(clio, marktleistung, gmail-Connector u. a.) und deren Incidents auf dem
geteilten Server blieben unangetastet.

Der visuelle Report liegt als [`server01-lasttest.html`](server01-lasttest.html)
bei (theme-fähig, hell/dunkel).

# Reise buchen — und sauber rückabwickeln 🧳

Das Beispiel zu **Kompensation**, dem einzigen BPMN-Mittel, das bereits getane
Arbeit rückgängig macht. Der Unterschied zum Fehlerpfad ist der ganze Punkt:

> Ein **Fehler** springt aus einer Aktivität heraus, die gerade schiefging.
> Eine **Kompensation** macht Aktivitäten rückgängig, die längst
> **erfolgreich** abgeschlossen sind — und genau die sind das Problem, wenn
> hinten etwas scheitert.

Fachlich ist es der Fall, den jede Privatperson kennt: Der Flug ist gebucht. Das
Hotel ist gebucht. Die Karte wird abgelehnt. Niemandem ist geholfen, wenn der
Prozess jetzt nur „Fehler" sagt — die zwei Buchungen sind trotzdem in der Welt
und kosten Stornofristen.

## Der Ablauf

```
Start "Reise buchen"
  → [Mockup] Flug buchen      ⟲ Kompensation: Flug stornieren
  → [Mockup] Hotel buchen     ⟲ Kompensation: Hotel stornieren
  → [Mockup] Zahlung ausführen
  → (X) Zahlung ok?
       ja   → Ende "Reise gebucht"
       nein → ⟲ Hotel rückabwickeln → ⟲ Flug rückabwickeln → Ende "Alles storniert"
```

## Drei Dinge, die man beim Nachbauen falsch macht

**Die Reihenfolge ist rückwärts.** Zuerst das Hotel, dann der Flug — die zuletzt
getane Arbeit wird zuerst zurückgenommen. Zwei Wurf-Ereignisse hintereinander
machen das sichtbar; ein einziger Wurf über alles würde die Reihenfolge
verstecken, die fachlich zählt (Stornofristen unterscheiden sich).

**Der Kompensations-Task hängt an keiner Sequenz.** Er ist über eine
`<association>` mit der Boundary verbunden und trägt `isForCompensation="true"`.
Wer ihn mit einem Sequenzfluss anschliesst, bekommt einen Task, der *immer*
läuft — auch wenn nichts zu stornieren ist.

**Kompensiert wird nur, was gelaufen ist.** Bricht die Buchung schon beim Flug
ab, hat das Hotel nie stattgefunden, und sein Handler wird nicht aufgerufen. Das
ist keine Sonderbehandlung im Modell, sondern die Semantik.

## Selbst ausprobieren

```
atlas_save_draft      xml=<reisestorno.bpmn>
atlas_deploy_project  …                                  → definitionKey
atlas_create_instance key=<definitionKey>  {reiseziel:"Lissabon", reisende:2, zahlungOk:false}
```

Gegen einen laufenden Server verifiziert — beide Wege, an denselben Variablen
ablesbar:

| Lauf | Variablen danach |
|---|---|
| `zahlungOk: false` | `flugbuchung`, `hotelbuchung`, `zahlungsergebnis {erfolgreich: false}` **und** `hotelstorno {storniert: true}`, `flugstorno {storniert: true, gebuehr: 40}` |
| `zahlungOk: true` | `flugbuchung`, `hotelbuchung`, `zahlungsergebnis {erfolgreich: true}` — **kein** `…storno` |

Dass die Storno-Variablen im Erfolgsfall fehlen, ist der Beweis: die
Kompensations-Tasks liegen ausserhalb des normalen Ablaufs und laufen nur, wenn
kompensiert wird.

## Dateien

| Datei | Rolle |
|---|---|
| [`reisestorno.bpmn`](reisestorno.bpmn) | Der Prozess: zwei Buchungen mit Kompensations-Boundary, Zahlung, Rückabwicklung in umgekehrter Reihenfolge. |

## Scharf schalten

Alle Umsysteme sind **Mockups** (ADR-0120), das Modell läuft also ohne eine
einzige Anbindung. Scharf werden aus den fünf Mockup-Tasks REST-Tasks gegen die
jeweilige Buchungs-API — Gateway, Kompensations-Boundaries und die Reihenfolge
der Rückabwicklung bleiben unberührt. Wichtig dabei: ein Storno-Aufruf muss
**idempotent** sein oder vorher prüfen, ob schon storniert wurde. Zustellung ist
„mindestens einmal", und eine zweite Stornierung derselben Buchung ist bei
manchen Anbietern ein Fehler, bei anderen eine zweite Gebühr.

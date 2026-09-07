# Preisänderung erreicht jede offene Offerte 📣

Das Beispiel zu **Signalen** — und zu dem Satz, an dem sich Nachricht und Signal
unterscheiden:

> Eine **Nachricht** trifft genau eine Instanz — die, deren Korrelationsschlüssel
> passt. Ein **Signal** trifft **alle**, die gerade darauf warten, und weiss
> nicht, wie viele das sind.

Fachlich: Ein Handwerksbetrieb hat ein Dutzend Offerten draussen, die auf einen
Kundenentscheid warten. Der Lieferant erhöht die Preise. Ohne Prozess bedeutet
das, sich an jede offene Offerte zu *erinnern*. Mit Prozess wirft eine Instanz
ein Signal, und jede wartende Offerte rechnet sich selbst nach.

## Zwei Modelle, eine Kopplung

```
proc_preisliste                              proc_offerte  (× beliebig viele)
──────────────────                           ─────────────────────────────────
Start "Neue Preise gültig"                   Start "Anfrage eingegangen"
  → [Script] Änderung festhalten               → [Script] Preis berechnen
  → ⚑ Signal werfen  ────────────────┐         → 🔑 Auf Entscheid warten
       "preisliste.geaendert"        └──────────►  └─ Boundary SIGNAL (nicht unterbrechend)
  → Ende                                              → [Script] Preis nachrechnen
                                                      → Ende "Nachgerechnet"
                                             → (X) Angenommen? → Auftrag / Abgelehnt
```

Die ganze Kopplung ist der **Signalname**. Weicht er zwischen Werfer und Fänger
um ein Zeichen ab, laufen beide Modelle fehlerfrei und treffen sich nie — genau
wie beim Nachrichtennamen einer Ereignisüberwachung.

## Warum die Boundary nicht unterbricht

Das ist der teuerste Modellierfehler, den dieses Diagramm vermeidet: Eine
**unterbrechende** Signal-Boundary würde bei jeder Preisänderung jede offene
Offerte vernichten. Nicht unterbrechend heisst: die Offerte bleibt offen, der
Kunde kann weiterhin zusagen, und parallel dazu rechnet ein zweiter Token nach.

Das Ergebnis landet in `angebotNeu` und **überschreibt `angebot` nicht**. Was dem
Kunden genannt wurde, ist ein Fakt und keine Variable — wer den alten Preis
überschreibt, kann hinterher nicht mehr belegen, was zugesagt war.

## Selbst ausprobieren

```
atlas_create_project  name="Preisaenderung"                    → projectId
atlas_save_form       id="offerte-entscheid" schema=<form>  projectId=…
atlas_save_draft      xml=<offerte.bpmn>                    projectId=…
atlas_save_draft      xml=<preisliste.bpmn>                 projectId=…
atlas_deploy_project  id=<projectId>
atlas_create_instance key=<offerte>     {kunde:"Kunde A", menge:40, einzelpreis:89.5}   (× 3)
atlas_create_instance key=<preisliste>  {artikel:"Zaunfeld", neuerPreis:94.9}
```

Gegen einen laufenden Server verifiziert: drei Offerten parken am
Kundenentscheid; **eine** Instanz von `proc_preisliste` genügt, und danach trägt
**jede** der drei `angebotNeu = {einzelpreis: 94.9, summe: 3796, differenz: 216}`.
Der Werfer hat nie erfahren, dass es drei waren.

## Dateien

| Datei | Rolle |
|---|---|
| [`offerte.bpmn`](offerte.bpmn) | Der Fänger: Offerte mit nicht unterbrechender Signal-Boundary. |
| [`preisliste.bpmn`](preisliste.bpmn) | Der Werfer: drei Elemente, die das Signal veröffentlichen. |
| [`form-offerte-entscheid.json`](form-offerte-entscheid.json) | Das Formular am Kundenentscheid; zeigt alte und nachgerechnete Summe. |

## Wann ein Signal — und wann nicht

* **Signal**, wenn der Sender seine Empfänger nicht kennt und nicht kennen soll:
  eine Preisliste, eine Betriebsschliessung, ein Rückruf, ein Ausfall.
* **Nachricht**, sobald es einen bestimmten Fall trifft: eine Zahlung zu *dieser*
  Rechnung, eine Antwort auf *diesen* Antrag. Siehe
  [`../mahnwesen/`](../mahnwesen/), das dieselbe Frage von der anderen Seite
  zeigt.

Wer versucht, ein Signal „an eine bestimmte Offerte" zu schicken, hat das falsche
Ereignis gewählt: ein Signal hat bewusst keinen Korrelationsschlüssel.

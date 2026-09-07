# Offene Rechnung mahnen 💸

Das Beispiel zu **Eskalation** — der dritten Ereignisart neben Fehler und
Nachricht, und der einzigen, die einen Fall aus einem Teilprozess heraus nach
oben meldet, *ohne ihn abzubrechen*.

Fachlich ist es der Zahlungseingang, den ein kleiner Betrieb heute im Kopf oder
in einer Excel-Spalte führt: Rechnung raus, nach zehn Tagen mahnen, nach zwanzig
entscheiden, ob Inkasso oder abschreiben. Der Prozess vergisst keine Rechnung und
niemand muss eine Liste durchgehen.

## Der Ablauf

```
Start "Rechnung gestellt"
  → Teilprozess "Zahlung überwachen"
       Timer (= zahlungsfrist) → [Mockup] Mahnung senden
         → Timer (= nachfrist) → Eskalation werfen → Ende
     ├─ Boundary NACHRICHT "zahlung.eingegangen" (unterbrechend)
     │     → Ende "Bezahlt"
     └─ Boundary ESKALATION (nicht unterbrechend)
           → 🔑 User-Task "Wie weiter?" → (X) → Ende "An Inkasso"
                                             → Ende "Abgeschrieben"
  → Ende "Frist ohne Zahlung"
```

## Warum es so gebaut ist

**Die Nachricht unterbricht, die Eskalation nicht.** Geht das Geld ein, ist der
ganze Mahnlauf gegenstandslos — der Teilprozess wird abgebrochen
(`cancelActivity` steht auf dem Default `true`). Läuft dagegen die Nachfrist ab,
soll der Mahnlauf zu Ende laufen **und** der Inhaber gefragt werden, also
`cancelActivity="false"`. Wer hier unterbricht, verliert den Rest eines Laufs,
der noch offen ist.

**Der Teilprozess ist keine Kosmetik.** Eine Eskalation wird an der
umschliessenden Aktivität gefangen. Ohne den Teilprozess gäbe es keine Aktivität,
an der die Boundary hängen könnte — deshalb besteht dieses Muster immer aus zwei
Ebenen.

**Nachricht ≠ Signal.** `zahlung.eingegangen` korreliert über die
`rechnungsnummer` und trifft damit genau **eine** Instanz. Ein Signal träfe alle
— siehe [`../preisaenderung/`](../preisaenderung/), das die Gegenprobe ist.

**Die Fristen sind Startvariablen.** `<timeDuration>` nimmt einen FEEL-Ausdruck
(ADR-0055), also stehen `P10D` und `P20D` als Vorgabe im Startformular und nicht
im Diagramm. Zum Ausprobieren startet man mit `PT2S` — derselbe Prozess, in
Sekunden statt Wochen.

## Selbst ausprobieren

```
atlas_create_project  name="Mahnwesen"                            → projectId
atlas_save_form       id="mahn-entscheid"  schema=<form>  projectId=…
atlas_save_draft      xml=<mahnwesen.bpmn>                projectId=…
atlas_deploy_project  id=<projectId>                              → definitionKey
atlas_create_instance key=<definitionKey>
                      {rechnungsnummer:"RE-1", kunde:"Muster GmbH", betrag:1250,
                       zahlungsfrist:"PT2S", nachfrist:"PT2S"}
```

Gegen einen laufenden Server verifiziert: Nach vier Sekunden steht die Aufgabe
*„Wie weiter?"* im Posteingang, während der Teilprozess bereits in seinem Ende
angekommen ist — beide Token existierten gleichzeitig, was genau der Unterschied
zwischen einer unterbrechenden und einer nicht unterbrechenden Boundary ist.
Nach `atlas_complete_task` mit `entscheid: "inkasso"` ist die Instanz
`completed`.

Den anderen Weg zeigt eine Nachricht: `atlas_publish_message` mit dem Namen
`zahlung.eingegangen` und dem Korrelationsschlüssel `RE-1` bricht den Mahnlauf ab
und endet in *„Bezahlt"*.

## Dateien

| Datei | Rolle |
|---|---|
| [`mahnwesen.bpmn`](mahnwesen.bpmn) | Der Prozess: Teilprozess mit Timer-Kette, Nachrichten- und Eskalations-Boundary. |
| [`form-mahn-entscheid.json`](form-mahn-entscheid.json) | Das Entscheidungsformular (Inkasso / abschreiben, Begründung Pflicht). |

## Scharf schalten

Der Mahn-Task ist ein **Mockup** (ADR-0120), das Beispiel läuft also ohne
Mailserver. Zu ersetzen ist genau dieser eine Task — durch einen Mail-Task. Zum
Üben genügt der Mail-Worker im **Vorschau**-Modus: die Mahnung wird identisch
gebaut und landet im Postausgang unter *Operations ▸ Outbox* statt beim Kunden.

# Umzug organisieren 📦

Atlas für eine **Privatperson** — das Beispiel gegen den Reflex, eine
Workflow-Engine sei etwas für Konzerne. Ein Umzug ist ein Prozess mit allem, was
dazugehört: mehrere Stränge, die gleichzeitig laufen, eine Frist, die man nicht
verpassen darf, und ein Abschluss, der auf alle Stränge wartet. Genau das, was
eine Liste auf Papier nicht kann.

## Der Ablauf

```
Start "Umzug geplant"     (umzugstermin, kuendigungsfrist, alte/neue Adresse)
  → [Script] Fristen rechnen        kuendigenBis = Umzugstermin − Kündigungsfrist
  → (+) parallel
       A  🔑 Wohnung kündigen   ⏰ Boundary-Timer auf kuendigenBis (nicht unterbrechend)
                                   → [Mockup] Erinnerung → Ende "Erinnert"
       B  🔑 Umzugsfirma buchen
       C  🔑 Adressen ändern        (Formular: die Checkliste, die man vergisst)
     (+) join — wartet auf alle drei
  → 🔑 Wohnung übergeben
  → Ende "Angekommen"
```

## Was dieses Modell zeigt und kein anderes im Repo

**Ein Termin statt einer Dauer.** Die übrigen Timer im Repository warten eine
Dauer ab („in zehn Tagen", [`../mahnwesen/`](../mahnwesen/)) oder laufen auf
einen Zeitplan ([`../hypothekarzinsen-migrosbank.bpmn`](../hypothekarzinsen-migrosbank.bpmn)).
Dieser feuert an einem **berechneten Datum**: `<timeDate>` nimmt einen
FEEL-Ausdruck (ADR-0055), und der rechnet mit echter Datumsarithmetik. Liegt das
Datum bereits in der Vergangenheit, feuert der Timer sofort — für einen Umzug in
zwei Wochen bei drei Monaten Frist ist genau das die richtige Antwort.

**Der Timer erinnert, er bricht nicht ab.** `cancelActivity="false"`: Die
Aufgabe *Wohnung kündigen* bleibt offen, es kommt nur eine Erinnerung dazu.
Unterbrechend wäre er das Gegenteil von hilfreich — er würde die Aufgabe
schliessen, deren Erledigung er anmahnt.

## Zwei Stolpersteine in einem Ausdruck

Beide erzeugen denselben Incident (`timer schedule: result is not a valid date`),
und beide sind beim Bauen dieses Beispiels tatsächlich passiert:

```
= date and time(date(fristen.kuendigenBis), time("09:00:00Z"))
                ^^^^                             ^^^^^^^^^^^^
```

1. **Das `date(...)` um die Variable.** Der Script-Task hat ein FEEL-Datum
   geschrieben — aber eine Prozessvariable wird als **JSON** persistiert, und
   zurück kommt ein String. Ohne die Rückwandlung bekommt `date and time` einen
   Text, wo es ein Datum erwartet.
2. **Ein Offset, keine Zonen-ID.** `"09:00:00Z"` funktioniert;
   `"09:00:00@Europe/Zurich"` ergibt hier keinen Zeitpunkt, den der Timer
   verwenden kann. Der Incident nennt den Unterschied nicht — er sagt in beiden
   Fällen dasselbe.

## Selbst ausprobieren

```
atlas_create_project  name="Umzug"                             → projectId
atlas_save_form       id="umzug-adressen" schema=<form>  projectId=…
atlas_save_draft      xml=<umzug.bpmn>                   projectId=…
atlas_deploy_project  id=<projectId>
atlas_create_instance key=<definitionKey>  {umzugstermin:"2026-10-01", kuendigungsfrist:"P3M",
                                            alteAdresse:"…", neueAdresse:"…"}
```

Gegen einen laufenden Server verifiziert: Nach dem Start stehen **drei** Aufgaben
gleichzeitig im Posteingang (`kuendigen`, `firma`, `adressen`), und der Timer ist
ohne Incident armiert. Die Übergabe erscheint erst, wenn alle drei erledigt sind
— ein exklusives Gateway an der Join-Stelle wäre der häufigste Modellierfehler
überhaupt: die Übergabe fände statt, sobald *irgendein* Strang fertig ist.

## Dateien

| Datei | Rolle |
|---|---|
| [`umzug.bpmn`](umzug.bpmn) | Der Prozess: parallele Stränge, Termin-Timer als Erinnerung, Join auf alle drei. |
| [`form-umzug-adressen.json`](form-umzug-adressen.json) | Die Adress-Checkliste (Gemeinde, Post, Kasse, Bank, Arbeitgeber, Versicherungen, Versorger, Abos). |

## Weiterbauen

* **Mehr Stränge**: Renovation, Kinderbetreuung, Schlüsselübergabe — je ein
  weiterer Zweig am selben Fork.
* **Echte Erinnerung**: aus dem Mockup wird ein Mail-Task an die eigene Adresse;
  zum Üben genügt der **Vorschau**-Modus des Mail-Workers.
* **Als Vorlage für andere Vorhaben**: Hochzeit, Hausbau, Firmengründung,
  Vereinsanlass — dieselbe Bauform, andere Zweige. Was ein Umzug von einer
  To-do-Liste unterscheidet, ist genau das, was hier im Modell steht: die Frist,
  die von einem Datum abhängt, und der Abschluss, der wartet.

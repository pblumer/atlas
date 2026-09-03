# Jira-Demo: Zugangsantrag 🎫

Zwei Prozesse, die den Jira-Worker-Typ ([ADR-0201](../../docs/adr/0201-jira-connector.md))
an einer echten Jira zeigen — der ausgehende Weg. Die Gegenrichtung, in der ein neues
Ticket einen Prozess *startet*, steht daneben in
[`jira-ticket-eingang/`](../jira-ticket-eingang/).

| Datei | Zweck |
|---|---|
| `jira-verbindungstest.bpmn` | Drei Elemente: legt einen Vorgang an, schreibt die Antwort in `ticket`. Zuerst starten. |
| `jira-zugangsantrag.bpmn` | Antrag → Vorgang anlegen → Freigabe → Transition bzw. Ablehnungskommentar. |

## Voraussetzung: eine Instanz, die den Worker-Typ kennt

Der Jira-Worker-Typ ist am 2026-08-27 auf `main` gelandet und in **keinem Release**
enthalten — v0.4.0 hat ihn nicht. Eine Instanz auf v0.4.0 oder einem älteren
`main`-Build zeigt ihn weder im Worker-Katalog unter *Console → Workers* noch als
Service-Task-Typ im Modeler. Zuerst also ein Build von `main`.

## Einrichtung (Jira Cloud)

1. **API-Token erzeugen:** Atlassian-Konto → *Sicherheit* → *API-Token* → Token
   erstellen.
2. **Secret anlegen:** *Console → Secrets* → neues Secret, z. B. `jira_acme`,
   Wert als JSON:

   ```json
   { "email": "du@example.com", "apiToken": "ATATT…" }
   ```

   > Das Token gehört in den Vault, nicht in einen Chat und nicht in ein Modell.
   > Die Secrets-Maske prüft die Form beim Speichern und nennt ein fehlendes Feld
   > beim Namen.

3. **Worker anlegen:** *Console → Workers* → *New worker* →
   Worker-Typ `jira`, **Name `jira`** (so heißt er in den Modellen),
   Endpoint `https://<deine-site>.atlassian.net`,
   Credential-Referenz `jira_acme`.

   Für Jira Data Center stattdessen `{ "token": "…" }` — ein Personal Access
   Token. Der Worker erkennt an den Feldern, welche der beiden Formen es ist;
   ein `method`-Feld, das ihnen widersprechen könnte, gibt es bewusst nicht.

## Verbindungstest starten

Modell deployen, Instanz starten, Formular ausfüllen:

- `jiraProjekt` — Projektschlüssel, z. B. `OPS`
- `vorgangstyp` — z. B. `Task` (bzw. `Aufgabe` in deutschsprachigen Projekten)

Läuft die Instanz durch, steht unter *Operations → Instanz → Variablen* das
Ergebnis in `ticket` — `{id, key, self}`. Der Vorgang ist in Jira da.

Parkt ein Token stattdessen mit einem Incident, sagt der Incident, woran es liegt:
ein nicht konfigurierter Worker-Name und ein konfigurierter, aber kaputter sind
zwei verschiedene Meldungen.

## Der Zugangsantrag

Der Prozess koordiniert, Jira dokumentiert, ein Mensch entscheidet:

```
Antrag (Formular)
  → Jira-Vorgang anlegen        create-issue      → ticket
  → Zugang freigeben            User-Task in Atlas
  → Freigegeben?                (X) Default: ablehnen
       ja   → Vorgang abschliessen   transition-issue + Kommentar
       nein → Ablehnung vermerken    add-comment
  → Antrag bearbeitet
```

Drei Dinge daran sind Absicht und keine Willkür:

**Der Default des Gateways ist die Ablehnung.** Bei einem Zugangsantrag ist die
konservative Antwort „nicht freigeben"; ein Gateway, dessen Bedingung nicht greift,
darf keinen Zugang gewähren.

**Ablehnen schaltet nichts weiter.** Es hält fest, warum nicht freigegeben wurde,
und lässt den Vorgang für einen Menschen offen. Welcher Übergang eine Ablehnung in
einem fremden Jira-Workflow wäre, weiß das Modell ohnehin nicht.

**Der Übergangsname steht im Startformular, nicht im Modell.** Der Worker löst
einen Übergang über den Namen auf, den Jira auf dem Knopf zeigt — und der ist pro
Workflow verschieden. So läuft dasselbe Modell gegen jedes Projekt.

### Die Falle, die zwei Pflichtfelder erklärt

FEEL-Verkettung propagiert `null`: Ist *eine* der verketteten Variablen nicht
gesetzt, ist das **ganze** Ergebnis null — nicht nur der fehlende Teil — und landet
als leerer String in Jira. Deshalb sind `begruendung` und `kommentar` in den
Formularen Pflichtfelder. Wer sie optional machen will, muss die Verkettung
absichern, nicht das Formular lockern.

### Ausprobieren

1. Beide Formulare und beide Modelle in ein Projekt deployen.
2. `jira-verbindungstest` starten — läuft der durch, stimmt die Einrichtung.
3. `jira-zugangsantrag` starten, Formular ausfüllen.
4. Der Vorgang steht in Jira. In Atlas wartet unter **Tasks** die Freigabe.
5. Freigeben → der Vorgang wird weitergeschaltet und trägt die Notiz.
   Ablehnen → er bleibt offen und bekommt den Ablehnungsgrund als Kommentar.

# Jira-Demo: Zugangsantrag 🎫

Zwei Prozesse, die den Jira-Worker-Typ ([ADR-0201](../../docs/adr/0201-jira-connector.md))
an einer echten Jira zeigen. **Work in progress** — der Verbindungstest steht, der
Zugangsantrag mit Freigabe fehlt noch.

| Datei | Zweck |
|---|---|
| `jira-verbindungstest.bpmn` | Drei Elemente: legt einen Vorgang an, schreibt die Antwort in `ticket`. Zuerst starten. |
| `jira-zugangsantrag.bpmn` | *(noch nicht geschrieben)* Antrag → Vorgang anlegen → Freigabe → Transition bzw. Ablehnungskommentar. |

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

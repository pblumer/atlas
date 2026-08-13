# Benutzerverwaltung als Atlas-Prozesse 👤

Atlas bildet seine **eigenen Verwaltungsabläufe** als BPMN + Formulare ab
(Dogfooding, wie [`examples/onboarding`](../onboarding/)): Der **Lebenszyklus**
eines Benutzers — Aufnahme, Zugriffs-Review, Offboarding — läuft als Atlas-Prozess
mit Atlas-Formularen. Die Prozesse sind die **Koordinations- und Audit-Schicht**;
die eigentlichen privilegierten Mutationen (Konto anlegen/sperren) bleiben bewusst
Admin-Handlungen.

Diese Beispiele sind der erste geplante Insasse des **geschützten System-Projekts**
aus [ADR-0119](../../docs/adr/0119-protected-system-project-and-bootstrap-deployment.md):
eigene Plattform-Prozesse, die mit der Installation kommen und nicht wie normale
Nutzer-Inhalte editier-/löschbar sind.

## Direkt-CRUD vs. regierter Prozess

Die native **Users-Konsole** (Organization → Users) bleibt die *direkte,
synchrone* Admin-/Break-glass-Oberfläche. Diese Prozesse sind die *Vordertür* für
**regierte** Änderungen (Antrag, Freigabe, Audit, Mail). Beide schreiben in
**denselben** User-Store.

> **Faustregel.** Zustandsübergang mit Dauer / Beteiligten / Genehmigung /
> Nebenwirkung → Prozess. Sofortige Direktmanipulation an einem Datensatz →
> native Maske.

Ein Buttton wie „Disable" wird also **nicht** zum Prozess; der *Ablauf* rundherum
(Offboarding mit Nachweis) schon.

## Warum die privilegierte Aktion ein User-Task ist

Die Atlas-Benutzerverwaltung (`api/users.go`) ist **admin-gated**, und die
Automatisierungs-/MCP-Identität darf Benutzer **absichtlich nicht** selbst
anlegen/sperren oder Passwörter self-service versenden (ADR-0044, ADR-0049). Ein
Prozess, der `POST /api/users` selbst aufruft, würde diese dokumentierte
Entscheidung aufweichen — gemäß `AGENTS.md` wird so etwas **geflaggt, nicht
umgangen**. Deshalb: der Prozess koordiniert, ein Admin führt die privilegierte
Aktion aus, nur die **Mail** ist automatisiert. Ein sanktionierter,
vollautomatischer Schreibpfad (`restConnector` auf `/api/users` mit
Vault-Credential) ist als eigene Folge-Entscheidung in ADR-0119 vermerkt und
gehört dann in das geschützte System-Projekt.

## Die drei Prozesse

### 1. Benutzer aufnehmen — `proc_benutzer_aufnahme`
```
Start (ba-antrag: Vorname, Nachname, E-Mail, Rolle, Abteilung, Begründung)
  → [Script] Zugangsdaten vorschlagen   – FEEL: benutzername = vorname.nachname
  → 🔑 User-Task "Konto anlegen" (ba-konto) – Admin legt an, setzt Initialpasswort, entscheidet
  → (X) Angelegt?  anlegen (Default) → Zugangs-Mail | ablehnen → Ablehnungs-Mail
  → Ende
```

### 2. Zugriffs-Review — `proc_benutzer_review`
```
Start (rev-start: Quartal, Umfang, Hinweis)
  → ✅ User-Task "Konten prüfen" (rev-pruefen) – ergebnis: ok | handlungsbedarf
  → (X) Handlungsbedarf?  handlungsbedarf → Meldung-Mail an Security/Admin | ok (Default) → weiter
  → [Script] Review protokollieren
  → Ende
```
Hinweis: In Produktion wäre der Start ein **Timer-Start-Event mit Zyklus**
(z. B. quartalsweise); hier bewusst ein Start-Formular, damit der Prozess
explizit startbar/testbar ist.

### 3. Benutzer offboarden — `proc_benutzer_offboarding`
```
Start (off-antrag: Benutzername, Letzter Arbeitstag, Grund, Benachrichtigung an)
  → 🔒 User-Task "Konto sperren" (off-sperren) – Admin sperrt in der Konsole, bestätigt
  → Bestätigungs-Mail
  → Ende
```

Alle drei sind der Gruppe `benutzerverwaltung` zugewiesen; die Mails laufen über
den Mail-Connector (`connector="Patrick Blumer"`, wie in `proc_cis_onboarding`).

## Artefakte

| Datei | Zweck |
|---|---|
| `benutzer-aufnahme.bpmn` | Aufnahme-Prozess (`proc_benutzer_aufnahme`) |
| `form-ba-antrag.json` / `form-ba-konto.json` | Formulare: Antrag / Konto anlegen |
| `benutzer-review.bpmn` | Review-Prozess (`proc_benutzer_review`) |
| `form-rev-start.json` / `form-rev-pruefen.json` | Formulare: Start / Konten prüfen |
| `benutzer-offboarding.bpmn` | Offboarding-Prozess (`proc_benutzer_offboarding`) |
| `form-off-antrag.json` / `form-off-sperren.json` | Formulare: Antrag / Konto sperren |

Alle BPMN-Diagramme tragen **hand-gesetztes BPMN-DI** (gerade Hauptachse, Zweige
auf eigener Spur).

## Deployen & starten (über die Atlas-MCP-Tools)

```
atlas_create_project  name="Benutzerverwaltung"                → projektId
atlas_save_form       id=<jede Form-id> … projectId=…
atlas_save_draft      xml=<jede .bpmn>   projectId=…
atlas_deploy_project  id=<projektId>                            → Definition-Keys
atlas_create_instance key=<Definition-Key>                     startet einen Ablauf
```

Später übernimmt der Bootstrap-Deploy aus ADR-0119 diesen Schritt automatisch beim
Serverstart (idempotent, ins geschützte System-Projekt).

## Erfasste Prozessvariablen (Auswahl)

- **Aufnahme:** `vorname`, `nachname`, `email`, `rolle`, `abteilung`, `begruendung`,
  `benutzername` (FEEL), `entscheidung`, `initialpasswort`, `ablehnungsgrund`
- **Review:** `quartal`, `umfang`, `hinweis`, `ergebnis`, `betroffene_konten`,
  `bemerkung`, `review_status` (FEEL)
- **Offboarding:** `benutzername`, `letzter_tag`, `grund`, `email`, `gesperrt`,
  `bemerkung`

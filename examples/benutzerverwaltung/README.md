# Benutzerverwaltung: Benutzer aufnehmen 👤

Atlas bildet einen **eigenen Verwaltungsprozess** als BPMN + Formulare ab
(Dogfooding, wie [`examples/onboarding`](../onboarding/)): Die Aufnahme eines
neuen Benutzers – Antrag, Konto anlegen, Zugangsdaten versenden – läuft als
Atlas-Prozess mit Atlas-Formularen. Der Prozess ist die Koordinations- und
Audit-Schicht; die eigentliche Konto-Anlage bleibt bewusst eine Admin-Handlung.

## Ablauf

```
Start (Formular ba-antrag: Vorname, Nachname, E-Mail, Rolle, Abteilung, Begründung)
  → [Script-Task] Zugangsdaten vorschlagen   – FEEL: benutzername = vorname.nachname
  → 🔑 User-Task "Konto anlegen"             – Admin legt das Konto an, setzt das
                                               Initialpasswort, entscheidet (Formular ba-konto)
  → (X) Angelegt?
        entscheidung = "anlegen"  (Default) → Service-Task "Zugangsdaten senden" (Mail)
        entscheidung = "ablehnen"           → Service-Task "Ablehnung senden"    (Mail, unten)
  → (X) Join
  → Ende "Aufnahme abgeschlossen"
```

Der **Antrag** kann von jeder berechtigten Person gestellt werden; die
**Konto-Anlage** ist der Gruppe `benutzerverwaltung` zugewiesen. Die Zugangs-
bzw. Ablehnungs-Mail läuft automatisch über den Mail-Connector.

## Warum ist das Anlegen ein User-Task und keine Automatik?

Die Atlas-Benutzerverwaltung (`api/users.go`) ist **admin-gated**, und die
Automatisierungs-/MCP-Identität darf Benutzer **absichtlich nicht** selbst
anlegen oder Passwörter self-service versenden (ADR-0044, ADR-0049). Ein
Prozess, der `POST /api/users` selbst aufruft und das Passwort verschickt,
würde diese dokumentierte Entscheidung aufweichen. Gemäß `AGENTS.md` wird so
etwas **geflaggt, nicht umgangen**. Deshalb:

- Der Prozess **koordiniert und dokumentiert** die Aufnahme.
- Ein Administrator legt das Konto im Admin-Bereich (**Benutzer → Neu**) an und
  bestätigt im Task – die privilegierte Mutation bleibt beim Menschen.
- Nur die **Mail** ist automatisiert (bestehender, sanktionierter Mail-Connector,
  wie in `proc_cis_onboarding`).

### Vollautomatische Variante (bewusst nicht enthalten)

Wer das Konto wirklich vom Prozess anlegen lassen will, braucht:

1. einen **`restConnector`**-Task auf `POST /api/users` (Body aus den
   Prozessvariablen) plus eine server-seitig hinterlegte **Admin-Credential**
   für diesen Connector, und
2. eine **neue ADR**, die die ADR-0044/0049-Grenze für diesen Weg bewusst
   öffnet (Audit, Rotation der Credential, Least-Privilege).

Das ist eine Architektur-Entscheidung, keine reine Modelländerung – daher hier
als Folge-Schritt vermerkt statt still eingebaut.

## Artefakte

| Datei | Zweck |
|---|---|
| `benutzer-aufnahme.bpmn` | Der BPMN-2.0-Prozess (`proc_benutzer_aufnahme`), BPMN-DI von Hand gesetzt |
| `form-ba-antrag.json` | Start-Formular: Antragsdaten |
| `form-ba-konto.json` | User-Task-Formular: Konto anlegen / entscheiden |

## Deployen & starten (über die Atlas-MCP-Tools)

```
atlas_create_project  name="Benutzerverwaltung"           → projektId
atlas_save_form       id=ba-antrag  … projectId=…
atlas_save_form       id=ba-konto   … projectId=…
atlas_save_draft      xml=<benutzer-aufnahme.bpmn>  projectId=…
atlas_deploy_project  id=<projektId>                       → Definition-Key
atlas_create_instance key=<Definition-Key>                 startet eine Aufnahme
```

Für einen neuen Benutzer genügt danach ein `atlas_create_instance` auf den
Definition-Key (bzw. das Start-Formular in der Tasks-App).

## Erfasste Prozessvariablen

- `vorname`, `nachname`, `email`, `rolle`, `abteilung`, `begruendung` — aus dem Antrag
- `benutzername` — Vorschlag vom Script-Task (FEEL: `lower case(vorname) + "." + lower case(nachname)`)
- `entscheidung` — `anlegen` | `ablehnen` (User-Task)
- `initialpasswort` — vom Admin gesetzt, per Mail versendet (nur bei `anlegen`)
- `ablehnungsgrund` — vom Admin gesetzt, per Mail versendet (nur bei `ablehnen`)

## Mögliche weitere Prozesse dieser Reihe

- **Passwort zurücksetzen** — Formular → Set-Password (Admin-Task) → Mail.
- **Benutzer deaktivieren / Offboarding** — Formular → Konto sperren (`disabled=true`).

Beide folgen demselben Muster (Prozess koordiniert, Admin führt die
privilegierte Aktion aus, Mail automatisch) und lassen sich analog ergänzen.

# Onboarding: Willkommen bei Atlas 🚀

Ein **selbst-referenzielles Onboarding** für neue Kolleg:innen: Sie lernen Atlas,
indem sie eine echte Atlas-Instanz durchlaufen. Atlas onboardet mit Atlas
(Dogfooding). Nach dem Anlegen eines Accounts findet die Person eine
Willkommens-Aufgabe in der Tasks-App und wird Schritt für Schritt durch die
Grundlagen geführt.

## Ablauf

```
Start (Start-Formular: Name + Track)
  → 👋 Willkommen an Bord          – Was ist Atlas? Die drei Säulen
  → 🧩 Grundbegriffe verstehen     – Prozess / Instanz / Token / Task / Gateway (+ Mini-Quiz)
  → [Script-Task] Begrüßung        – FEEL baut einen personalisierten Gruß
  → (X) Auch mitentwickeln? ───────┐
        track = "nur_benutzen"  → überspringt den Technik-Teil
        sonst (Default)         → 🏗️ Architektur & Invarianten
                                  → 🔧 Setup & Definition of Done
  → 🎓 Dein erster Schritt         – Abschluss + „Was baust du zuerst?"
  → [Script-Task] Abschluss
  → 🎉 Geschafft!
```

Der **fachliche Teil** (benutzen) läuft für alle; der **technische Teil**
(mitentwickeln) hängt am Gateway. Default ist der volle Weg inklusive Technik —
wer im Start-Formular „nur benutzen" wählt, überspringt ihn. So ist
„beides kombiniert" der Normalfall.

## Artefakte

| Datei | Zweck |
|---|---|
| `onboarding.bpmn` | Der BPMN-2.0-Prozess (`onboarding-atlas`) |
| `onboarding-form-start.json` | Start-Formular: Name + Track |
| `onboarding-form-welcome.json` | 👋 Willkommen |
| `onboarding-form-concepts.json` | 🧩 Grundbegriffe (+ Quiz) |
| `onboarding-form-architecture.json` | 🏗️ Architektur & die 6 Invarianten |
| `onboarding-form-devsetup.json` | 🔧 Setup & Definition of Done |
| `onboarding-form-finish.json` | 🎓 Abschluss |

## Deployen & starten (über die Atlas-MCP-Tools)

```
atlas_create_project  name="Onboarding"                 → projektId
atlas_save_form       id=onb-start …  projectId=…       (× 6 Formulare)
atlas_save_draft      xml=<onboarding.bpmn>  projectId=…
atlas_deploy_project  id=<projektId>                    → Definition-Key
atlas_create_instance key=<Definition-Key>             startet ein Onboarding
```

Für eine neue Kollegin/einen neuen Kollegen genügt danach ein
`atlas_create_instance` auf den Definition-Key — jede Instanz ist ein
eigenständiger, wiederholbarer Durchlauf. Die BPMN-DI (Diagramm-Layout) wird
beim Deploy automatisch erzeugt.

## Erfasste Prozessvariablen

- `displayName`, `track`, `losGehts` — aus dem Start-Formular
- `welcomeAck`, `quizInstanz` — fachlicher Teil
- `willkommensgruss` — vom Script-Task (FEEL), personalisiert
- `archAck`, `devAck` — technischer Teil (Checklisten)
- `erstesZiel` — „Was möchtest du zuerst bauen?"
- `onboardingStatus` — vom Abschluss-Script-Task

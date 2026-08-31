# Benutzerverwaltung als Atlas-Prozesse 👤

Atlas bildet seine **eigenen Verwaltungsabläufe** als BPMN + Formulare ab
(Dogfooding, wie [`examples/onboarding`](../onboarding/)): Der **Lebenszyklus**
eines Benutzers — Aufnahme, Zugriffs-Review, Offboarding — läuft als Atlas-Prozess
mit Atlas-Formularen. Die Prozesse sind die **Koordinations- und Audit-Schicht**;
die eigentlichen privilegierten Mutationen (Konto anlegen/sperren) bleiben bewusst
Admin-Handlungen.

Diese Beispiele sind der erste Insasse des **geschützten System-Projekts** aus
[ADR-0122](../../docs/adr/0119-protected-system-project-and-bootstrap-deployment.md):
eigene Plattform-Prozesse, die mit der Installation kommen und nicht wie normale
Nutzer-Inhalte editier-/löschbar sind.

> **Deploybare Quelle:** Die Kopien unter [`api/systemprocesses/`](../../api/systemprocesses/)
> sind ins Binary eingebettet und werden beim Serverstart automatisch ins
> geschützte System-Projekt deployed (idempotent per Checksumme). Die Dateien
> hier dienen der menschlichen Lektüre; bei Änderungen beide Stellen angleichen.

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

## Freigabe bleibt menschlich, Anlage ist automatisiert

Die Atlas-Benutzerverwaltung (`api/users.go`) ist **admin-gated**. Bis ADR-0123
blieb auch die Konto-Anlage eine reine Admin-Handhabung. Mit ADR-0123 gibt es
einen **sanktionierten, engen** Schreibpfad: den `userConnector` (create /
set-password / disable), der **nur** für Prozesse des geschützten System-Projekts
(ADR-0122) läuft, kein Credential im Modell trägt und dieselben Rails wie die
Admin-API nutzt (Passwortlänge, Uniqueness, Last-Admin-Lockout). Seit dem
Amendment vom 2026-08-14 ist die Provisionierung **standardmäßig aktiv (opt-out)** —
abschaltbar mit `--user-provisioning=false`.

Damit gilt: die **Freigabe bleibt eine Admin-Handlung** (User-Task „Antrag
freigeben"), die **Konto-Anlage selbst läuft automatisch** über den userConnector,
und die **Mail** ebenfalls. Die eigentlichen Sicherheitsgrenzen sind das
System-Projekt-Gating und die menschliche Freigabe — ist die Provisionierung
abgeschaltet, **parkt** der `Konto anlegen`-Task, bis ein Operator sie wieder
aktiviert.

## Die drei Prozesse

### 1. Benutzer aufnehmen — `proc_benutzer_aufnahme`
```
Start (ba-antrag: Vorname, Nachname, E-Mail, Abteilung, Begründung)
  → [Script] Zugangsdaten vorschlagen   – FEEL: benutzername = vorname.nachname
  → 🔑 User-Task "Antrag freigeben" (ba-konto) – Admin vergibt Rolle, setzt Initialpasswort
  → (X) Angelegt?
        anlegen (Default) → [userConnector create] "Konto anlegen" → Zugangs-Mail
        ablehnen          → Ablehnungs-Mail
  → Ende
```
Der Antragsteller wählt seine **Rolle bewusst nicht selbst** — das Start-Formular
kennt kein Rollen-Feld. So ist derselbe Prozess auch als **öffentliches
Registrierungs-Formular** tragfähig: die Login-Seite zeigt einen
„Registrieren"-Link auf die öffentliche Start-URL dieses Prozesses (ADR-0029 /
ADR-0126). Der Admin vergibt die Rolle erst bei der Freigabe — zur Auswahl stehen
die vier Rollen, die Atlas durchsetzt: `user` (Aufgaben), `modeler` (modellieren
und deployen), `operator` (Instanzen betreiben) und `admin`. Das Feld trägt eine
kommagetrennte Liste, weil ein Konto mehrere Rollen hält; `modeler,user` ist
deshalb ein einziger Wert und keine Ausnahme.

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
den Mail-Worker `Patrick Blumer` — das Attribut heisst weiterhin `connector="…"`
(ADR-0203 benennt die Begriffe um, nicht die Modelle), wie in `proc_cis_onboarding`.

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

Später übernimmt der Bootstrap-Deploy aus ADR-0122 diesen Schritt automatisch beim
Serverstart (idempotent, ins geschützte System-Projekt).

## Erfasste Prozessvariablen (Auswahl)

- **Aufnahme:** `vorname`, `nachname`, `email`, `rolle`, `abteilung`, `begruendung`,
  `benutzername` (FEEL), `entscheidung`, `initialpasswort`, `ablehnungsgrund`
- **Review:** `quartal`, `umfang`, `hinweis`, `ergebnis`, `betroffene_konten`,
  `bemerkung`, `review_status` (FEEL)
- **Offboarding:** `benutzername`, `letzter_tag`, `grund`, `email`, `gesperrt`,
  `bemerkung`

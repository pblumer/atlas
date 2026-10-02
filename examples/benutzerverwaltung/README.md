# Benutzerverwaltung als atlas-Prozesse 👤

Atlas bildet seine **eigenen Verwaltungsabläufe** als BPMN + Formulare ab
(Dogfooding, wie [`examples/onboarding`](../onboarding/)): Der **Lebenszyklus**
eines Benutzers — Aufnahme, Zugriffs-Review, Offboarding — läuft als atlas-Prozess
mit atlas-Formularen. Die Prozesse sind die **Koordinations- und Audit-Schicht**;
die eigentlichen privilegierten Mutationen (Konto anlegen/sperren) bleiben bewusst
Admin-Handlungen.

Diese Beispiele sind der erste Insasse des **geschützten System-Projekts** aus
[ADR-0122](../../docs/adr/0122-protected-system-project-and-bootstrap-deployment.md):
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

Die atlas-Benutzerverwaltung (`api/users.go`) ist **admin-gated**. Bis ADR-0123
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
  → [Script] Instanz festhalten         – FEEL: atlasInstance = processInstanceKey
  → 📣 Signal "Aufnahme beantragt"      – atlas.user.requested (siehe unten)
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
die vier Rollen, die atlas durchsetzt: `user` (Aufgaben), `modeler` (modellieren
und deployen), `operator` (Instanzen betreiben) und `admin`. Das Feld trägt eine
kommagetrennte Liste, weil ein Konto mehrere Rollen hält; `modeler,user` ist
deshalb ein einziger Wert und keine Ausnahme.

#### Von neuen Anträgen erfahren: das Signal `atlas.user.requested`

Ein eingegangener Antrag wartet bei „Antrag freigeben", und niemand wird darauf
hingewiesen. Den geschützten Prozess kann eine Installation nicht um eine
Benachrichtigung ergänzen. Deshalb wirft er selbst ein Signal,
**`atlas.user.requested`**, unmittelbar bevor die Freigabe wartet
([ADR-0431](../../docs/adr/0431-system-processes-announce-their-facts-as-signals.md)).
Der Prozess kennt seine Empfänger nicht. Wer informiert werden will, deployt einen
**eigenen** Prozess mit einem Signal-Start auf diesen Namen. Ohne einen solchen Prozess
bleibt der Wurf folgenlos.

**Was der Empfänger bekommt.** Das Signal überträgt alle Variablen, die der Antrag an
dieser Stelle hat. Sie stehen im Empfänger als gewöhnliche FEEL-Variablen bereit:

| Variable | Herkunft | immer gesetzt |
|---|---|---|
| `vorname`, `nachname`, `email` | Formular `ba-antrag` | ja (Pflichtfelder) |
| `abteilung`, `begruendung` | Formular `ba-antrag` | nein |
| `benutzername` | Script „Zugangsdaten vorschlagen" | ja |
| `atlasInstance` | Script „Instanz festhalten": der Schlüssel dieser Antragsinstanz, als Text | ja |

`initialpasswort`, `rolle` und `entscheidung` entstehen erst bei der Freigabe und sind
deshalb **nie** dabei. Ein Test hält diese Stelle fest. Mit `atlasInstance` kann der
Empfänger auf genau diesen Antrag zeigen.

Die Tabelle ist ein Auszug aus dem Ereigniskatalog, dem Paket `eventcatalog`
([ADR-0435](../../docs/adr/0435-one-catalogue-of-the-events-atlas-emits.md)). Ein Test hält
fest, dass der Empfänger genau die dort deklarierten Variablen erhält.

**Wer einen Empfänger deployen darf.** Das Signal trägt Personendaten. Deshalb braucht das
Deployment eines Prozesses, der darauf wartet, die Rolle `admin` (ADR-0435 §6). Ein
`modeler` erhält beim Deployment die Antwort 403, die Element, Ereignis, Personendaten-Felder
und die nötige Rolle nennt. Das Problems-Panel des Modelers meldet denselben Befund schon
beim Modellieren. Ein bereits deployter Empfänger läuft weiter; erst seine nächste Version
braucht eine Administratorin oder einen Administrator.

**Rezept: ein Antrag, eine Discord-Nachricht.** Das Rezept setzt einen eingerichteten
Discord-Worker voraus
([ADR-0258](../../docs/adr/0258-discord-worker.md)). `discord` und die Kanal-Id sind
durch die eigenen Werte zu ersetzen:

```xml
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:atlas="http://atlas/schema/1.0"
             id="defs_aufnahme_discord" targetNamespace="http://atlas/examples">
  <signal id="sig_user_requested" name="atlas.user.requested"/>
  <process id="proc_aufnahme_discord" name="Aufnahme-Antrag nach Discord melden" isExecutable="true">
    <startEvent id="start" name="Aufnahme beantragt">
      <outgoing>f_start_melden</outgoing>
      <signalEventDefinition signalRef="sig_user_requested"/>
    </startEvent>
    <serviceTask id="melden" name="In Discord melden">
      <extensionElements>
        <atlas:discordConnector connector="discord" operation="send-message"
          channel="123456789012345678"
          content="=&quot;Neuer Aufnahme-Antrag von &quot; + vorname + &quot; &quot; + nachname + &quot; (&quot; + benutzername + &quot;). Die Freigabe wartet in den Aufgaben.&quot;"
          resultVariable="gesendet">
          <atlas:discordField name="allowed_mentions" value="={parse: []}"/>
        </atlas:discordConnector>
      </extensionElements>
      <incoming>f_start_melden</incoming>
      <outgoing>f_melden_ende</outgoing>
    </serviceTask>
    <endEvent id="ende" name="Gemeldet">
      <incoming>f_melden_ende</incoming>
    </endEvent>
    <sequenceFlow id="f_start_melden" sourceRef="start" targetRef="melden"/>
    <sequenceFlow id="f_melden_ende" sourceRef="melden" targetRef="ende"/>
  </process>
</definitions>
```

Drei Punkte darin sind Absicht:

- **`allowed_mentions` mit leerer `parse`-Liste.** Der Text stammt aus einem öffentlichen
  Formular. Ohne diese Zeile könnte jemand mit dem Vornamen `@everyone` den ganzen Kanal
  anpingen.
- **Nur Pflichtfelder im Text.** In FEEL ergibt `"Text" + null` den Wert `null`. Ein leeres
  optionales Feld wie `begruendung` würde den ganzen Text auslöschen, und die Aufgabe
  scheitert. Wer optionale Felder zeigen will, schützt sie mit
  `if begruendung = null then "-" else begruendung`. Discord nimmt höchstens 2000 Zeichen
  an.
- **Wenig Personendaten.** Was in Discord steht, liegt bei Discord. Name und
  Benutzername genügen als Hinweis. Die Details stehen in der Aufgabe.

Zwei Eigenschaften eines Signals sind zu kennen. Es wird **nicht gepuffert**: Ist der
Empfänger beim Eingang eines Antrags nicht deployt oder deaktiviert, erfährt er nie davon.
Und es gilt **für die ganze Engine**: Jeder Prozess mit diesem Signalnamen empfängt
dieselben Daten.

### 2. Zugriffs-Review — `proc_benutzer_review`
```
Start (rev-start: Quartal, Umfang, Meldung an, Hinweis)
  → ✅ User-Task "Konten prüfen" (rev-pruefen) – ergebnis: ok | handlungsbedarf
  → (X) Handlungsbedarf?  handlungsbedarf → Meldung-Mail an `meldung_an` | ok (Default) → weiter
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
den Mail-Worker `mail` — das Attribut heisst weiterhin `connector="…"`
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

## Deployen & starten (über die atlas-MCP-Tools)

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

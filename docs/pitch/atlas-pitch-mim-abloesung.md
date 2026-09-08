# „Was wäre, wenn" — Atlas als Antwort auf die MIM-Ablösung

**Zielgruppe:** Manager oder Architekt, der die Ablösung von Microsoft Identity Manager
verantwortet oder mitentscheidet. Wenig technisches Interesse, hohes Interesse an
Handlungsspielraum, Risiko und Folgekosten.

**Format:** Live-Demonstration am laufenden System, 10 Minuten, ohne Folien, als
durchgehende Erzählung in drei Akten.

**Die These, die der Auftritt verkauft:** Die MIM-Ablösung ist ein Zwang. Ein Zwang ist
teuer, wenn man ihn als Ersatzbeschaffung behandelt, und wertvoll, wenn man ihn als
Gelegenheit behandelt, die Geschäftslogik aus einem Produkt herauszuholen und in eine
Form zu bringen, die das eigene Haus lesen kann.

---

## 1. Der Rahmen: drei Akte, neun Fragen

Jede Szene beginnt mit einer „Was wäre, wenn"-Frage. Die Live-Demonstration ist die
Antwort darauf. Das ist die ganze Dramaturgie — der Entscheider hört neun Fragen, die er
sich selbst schon gestellt hat, und sieht neunmal, dass es eine Antwort gibt.

| Akt | Zeit | Szene | Die Frage |
|-----|------|-------|-----------|
| I — Die Lage | 0:00–1:00 | 1 | Was wäre, wenn dieser Zwang Ihre Gelegenheit wäre? |
| II — Der Umzug | 1:00–2:20 | 2 | Was wäre, wenn Sie Ihre MIM-Workflows nicht neu schreiben müssten? |
| | 2:20–3:20 | 3 | Was wäre, wenn Ihre Identity-Logik lesbar wäre? |
| | 3:20–4:30 | 4 | Was wäre, wenn jede Identität eine Akte hätte? |
| | 4:30–5:40 | 5 | Was wäre, wenn die Revision fragt, warum dieses Konto Zugriff hat? |
| | 5:40–6:20 | 6 | Was wäre, wenn Ihre Plattform aus einer Datei bestünde? |
| III — Der Hebel | 6:20–7:20 | 7 | Was wäre, wenn Identity nur der erste Prozess wäre? |
| | 7:20–8:40 | 8 | Was wäre, wenn KI die Arbeit macht — und Sie trotzdem geradestehen können? |
| Schluss | 8:40–10:00 | 9 | Was Atlas nicht ist, und was der nächste Schritt kostet |

**Warum diese Reihenfolge:** Akt I macht den Handlungsdruck sichtbar, den der Entscheider
ohnehin spürt, ohne Angstverkauf. Akt II beantwortet die Frage „Wie komme ich da raus?"
mit dem Import als erstem, unerwartetem Beweis — wer zuerst zeigt, dass die Altlast
mitkommt, hat die Aufmerksamkeit für alles Weitere. Akt III dreht die Ablösung in eine
Plattformentscheidung und beantwortet die Frage, die jeder Entscheider 2026 im Hinterkopf
hat, aber selten ausspricht: *Was mache ich, wenn KI-Agenten in zwei Jahren die halbe
Sachbearbeitung übernehmen?*

**Streichkandidaten**, wenn die Zeit knapp wird: zuerst Szene 6, dann Szene 3.
Niemals streichen: Szene 2, 5, 8 und 9.

---

## 2. Vorbereitung

Zusätzlich zur allgemeinen Vorbereitung (siehe
[`atlas-pitch-entscheider.md`](atlas-pitch-entscheider.md)):

- **Eine XOML-Datei bereitlegen.** Szene 2 lebt davon, dass eine echte MIM-Workflow-Datei
  im Import landet. Im Repository liegt `mimimport/testdata/approval-workflow.xoml` als
  Rückfallebene; besser ist eine anonymisierte Datei aus dem Haus des Gegenübers, falls
  er eine liefern konnte. Alternativ eine `Export-FIMConfig`-XML, die den Workflow
  einbettet.
- **Den Import-Weg vorher einmal durchspielen:** Modeler → Kontextmenü eines Projekts →
  *Import MIM workflow (XOML)…*. Der Bericht danach zeigt, wie viele Knoten *native*,
  *preserved* und *to review* sind — diese drei Zahlen sind die Pointe der Szene.
- **Einen Identitäts-Prozess laufen haben**, idealerweise mit vielen Instanzen
  (Joiner/Mover/Leaver). Auf dem vorbereiteten System sind das rund 50 000 aktive
  Vorgänge.
- **Eine Berechtigungs-Entscheidungstabelle** offen haben, die als Gegenstück zu
  MIM-Sets und -MPRs dient.
- **Die Grenzen kennen** (Szene 9). Wer hier ins Schwimmen kommt, verliert den ganzen
  Auftritt. Die Liste steht in [`docs/comparisons/mim.md`](../comparisons/mim.md).

---

## 3. Das Drehbuch

### Akt I — Die Lage

#### Szene 1 · 0:00–1:00 · kein Klick

> „Ich fange mit etwas an, das Sie besser wissen als ich: Der Mainstream-Support für
> Microsoft Identity Manager ist im April dieses Jahres ausgelaufen. Der erweiterte
> Support läuft bis Januar 2029, und Microsoft sagt dazu ausdrücklich, man solle nicht
> mit einer weiteren Verlängerung rechnen, sondern das Produkt schrittweise ablösen.
>
> Das sind rund achtundzwanzig Monate. Kein Notfall — aber der Zeitpunkt, an dem die
> Entscheidung fällt, ist jetzt, nicht 2028.
>
> Die naheliegende Antwort ist, ein anderes Produkt zu kaufen. Ich möchte Ihnen in den
> nächsten Minuten eine andere Frage vorlegen: **Was wäre, wenn dieser Zwang nicht Ihr
> Problem wäre, sondern Ihre Gelegenheit?**
>
> Denn Sie müssen ohnehin an diese Logik heran. Die Frage ist nur, ob sie danach wieder
> in einem Produkt liegt, das in acht Jahren dasselbe Gespräch auslöst — oder in einer
> Form, die Ihr eigenes Haus lesen kann."

**Botschaft:** Der Handlungsdruck ist real und benannt, aber die Wahl ist grösser als
„welches Ersatzprodukt".
**Regie:** Nüchtern bleiben. Kein Alarmton. Die Zahlen tragen sich selbst.

---

### Akt II — Der Umzug

#### Szene 2 · 1:00–2:20 · Modeler → Import MIM workflow (XOML)

> „Der erste Einwand gegen jede Ablösung lautet: Wir haben Jahre in unsere Workflows
> gesteckt, und die müssten wir alle neu bauen. Schauen wir es uns an."

*[Kontextmenü, „Import MIM workflow (XOML)…", die vorbereitete Datei wählen.]*

> „Das ist ein MIM-Workflow, so wie er heute bei Ihnen liegt. Und das hier —"

*[Auf den Bericht zeigen: die Zahlen für „native", „preserved", „to review".]*

> „— ist die ehrliche Bilanz der Übersetzung. Grün ist das, was eins zu eins übersetzt
> wurde: Genehmigungen, Verzweigungen, Benachrichtigungen. Gelb ist übernommen, aber noch
> nicht verdrahtet — die Originalanweisung hängt am Kästchen, ein Entwickler schliesst sie
> an. Rot muss ein Mensch anschauen.
>
> Der Punkt ist nicht, dass das perfekt ist. Der Punkt ist, dass **Sie die Zahl kennen,
> bevor Sie das Projekt beauftragen.** Sie werfen Ihre Workflows hier hinein und wissen am
> selben Nachmittag, wie gross die Handarbeit wird. Das ist die Information, die in einer
> Migrationsofferte normalerweise fehlt — und die Ihnen als Erste ein Angebot verkauft."

*[Das entstandene Diagramm auf der Zeichenfläche zeigen.]*

> „Und aus einer XML-Datei, die niemand liest, ist ein Bild geworden."

**Botschaft:** Die Altlast kommt mit, und das Migrationsrisiko wird vor dem Projekt
messbar statt danach.
**Regie:** Die drei Zahlen laut vorlesen. Sie sind der stärkste Einzelmoment des Auftritts.
**Niemals streichen.**

---

#### Szene 3 · 2:20–3:20 · Modeler, Diagramm und Entscheidungstabelle

> „Halten wir kurz fest, was hier gerade passiert ist. In MIM steckt Ihre Fachlogik an
> vier Orten: in Workflows, in Sets, in Management Policy Rules und in Sync-Regeln.
> Zusammen ergeben sie ein Verhalten, das korrekt ist — das aber niemand mehr in einem
> Bild erklären kann. Wenn der Kollege, der das gebaut hat, das Haus verlässt, geht das
> Verständnis mit.
>
> Hier steht dasselbe Verhalten als Diagramm. Ein Fachbereichsleiter liest das."

*[Kurz den Hauptpfad entlangfahren, dann die Entscheidungstabelle öffnen.]*

> „Und was in MIM ein Set und eine Policy Rule ist — wer bekommt was, unter welcher
> Bedingung — ist hier eine Tabelle. Wenn sich die Berechtigungsmatrix ändert, ändert das
> ein Fachmitarbeiter in dieser Zeile. Kein Ticket, kein Entwickler, keine neue Version.
>
> **Was wäre, wenn Ihre Identity-Logik etwas wäre, das man liest statt rekonstruiert?**"

**Botschaft:** Aus verteiltem Produktwissen wird ein lesbares Modell — das ist der
eigentliche Gewinn der Ablösung.
**Zweiter Streichkandidat.**

---

#### Szene 4 · 3:20–4:30 · Operations Live-Ansicht → Tasks

> „Jetzt der Betrieb. In MIM laufen Ihre Run-Profile nach Zeitplan. Zwischen zwei Läufen
> ist der Zustand einer Identität eine Vermutung, und wenn etwas hängt, sucht jemand in
> Protokolldateien.
>
> Hier hat jede Identität ihren eigenen Vorgang."

*[Live-Ansicht: das Diagramm mit allen laufenden Instanzen.]*

> „Grün heisst: Hier steht gerade jemand. Die Zahl am Kästchen sagt, wie viele. Sie sehen
> in einem Bild, wo Ihre Eintritte hängen — und woran."

*[In die Tasks-Ansicht wechseln.]*

> „Und wo ein Mensch entscheiden muss — eine Freigabe durch den Vorgesetzten, eine
> Ausnahme —, liegt die Aufgabe hier, mit Formular, Frist und Zuständigkeit. Nicht in
> einem Mailfach.
>
> **Was wäre, wenn jede Identität eine Akte hätte statt eines Zustands in einer
> Datenbank?**"

**Botschaft:** Der Zustand jedes Eintritts ist jederzeit sichtbar, statt zwischen zwei
Läufen unbekannt zu sein.

---

#### Szene 5 · 4:30–5:40 · Replay

> „Die wichtigste Minute, und in Ihrem Umfeld die teuerste Frage überhaupt. Eine
> Rezertifizierung läuft, oder die Revision kommt, und jemand fragt: **Warum hat dieser
> Mitarbeiter diese Berechtigung?**
>
> Heute ist das ein Rekonstruktionsprojekt: Sync-Historie, Portal-Requests,
> Ereignisprotokolle, und am Ende eine Aussage mit einem Restzweifel."

*[Einen abgeschlossenen Vorgang öffnen, die Zeitleiste zurückziehen.]*

> „Hier spule ich den Vorgang dieser Identität Schritt für Schritt zurück. Ich sehe, wer
> was wann freigegeben hat, welche Regel gegriffen hat, und **welche Daten in genau diesem
> Moment galten** — nicht die von heute.
>
> Das ist keine Rekonstruktion. Das sind die aufgezeichneten Tatsachen. In einem
> Umfeld mit Rezertifizierungspflicht ist das der Unterschied zwischen einer Antwort und
> einer Ausrede."

**Botschaft:** Nachweisbarkeit ist bei Identity kein Komfort, sondern der Prüfgegenstand.
**Niemals streichen.**

---

#### Szene 6 · 5:40–6:20 · Console-Dashboard

> „Zwei Zahlen zum Betrieb. Erstens: Das hier sind rund fünfzigtausend Vorgänge, die
> gerade gleichzeitig laufen.
>
> Zweitens: Das läuft in einer einzigen Programmdatei. Keine Datenbank, kein
> Message-Broker, keine Middleware. Sie kopieren eine Datei auf einen Server und starten
> sie.
>
> Sie kennen die Gegenrechnung besser als ich: Was Sie heute für MIM an Serverrollen,
> Datenbank und Abhängigkeiten betreiben, und was ein Versionswechsel darin auslöst.
>
> **Was wäre, wenn Ihre Plattform aus einer Datei bestünde?**"

**Botschaft:** Der Betriebsaufwand fällt weg, nicht nur die Lizenzkosten.
**Erster Streichkandidat.**
**Regie:** Die MIM-Serverlandschaft nicht selbst aufzählen — der Gegenüber tut das im
Kopf, und das wirkt stärker.

---

### Akt III — Der Hebel

#### Szene 7 · 6:20–7:20 · Applikationsübersicht

> „Und jetzt kommt der Teil, wegen dem ich das Gespräch überhaupt führen wollte.
>
> Alles, was Sie gesehen haben, ist keine Identity-Software. Es ist eine Prozessplattform,
> auf der Identity zufällig der erste Prozess ist."

*[Die Liste der Applikationen zeigen — Bewerbermanagement, Antragswesen, Bestellfreigabe.]*

> „Dasselbe System trägt die Bewerbung, die Bestellfreigabe, den Antrag aus dem
> Fachbereich, den Verlustfall. Gleiche Oberfläche, gleiche Nachvollziehbarkeit, gleiche
> Betriebsart.
>
> Das ändert die Rechnung Ihrer Ablösung: Sie kaufen kein Ersatzprodukt für ein
> auslaufendes Nischenwerkzeug, sondern Sie richten die Stelle ein, an der Ihre
> Geschäftsprozesse künftig liegen. Der Identity-Prozess bezahlt die Einführung, und alles
> danach ist Grenzkosten.
>
> Und weil jede Anbindung zuerst als Attrappe laufen kann, beginnt ein neuer Prozess nicht
> mit einem Integrationsprojekt, sondern mit einem Diagramm.
>
> **Was wäre, wenn Identity nur der erste Prozess wäre?**"

**Botschaft:** Aus einer Zwangsablösung wird eine Plattformentscheidung mit fallenden
Grenzkosten.

---

#### Szene 8 · 7:20–8:40 · kein Klick, oder ein Agenten-Prozess

> „Ein letzter Gedanke, und er ist der eigentliche Grund, warum ich diese Bauweise für
> richtig halte.
>
> Sie werden in den nächsten zwei, drei Jahren KI-Agenten in Ihre Sachbearbeitung lassen.
> Nicht weil es modern ist, sondern weil der Kostendruck es erzwingt. Und dann stehen Sie
> vor einer Frage, die heute noch niemand gut beantwortet: **Wenn ein Agent entscheidet —
> wer haftet, und woran zeigen Sie, was er durfte?**
>
> Ein Agent, der frei auf Ihre Systeme zugreift, ist ein Haftungsrisiko. Ein Agent als
> Schritt in einem Prozess ist etwas anderes. In Atlas ist der Werkzeugkasten eines Agenten
> **das Diagramm**: Er darf genau die Schritte auslösen, die im Bild um ihn herum stehen —
> nicht mehr. Ein Mensch schaut auf eine Zeichnung und sieht die Gesamtheit dessen, was
> dieses Ding in Gang setzen kann. Und was er getan hat, steht anschliessend in derselben
> Aufzeichnung wie jeder menschliche Schritt, mit demselben Rückspulknopf.
>
> Das ist die Umkehrung des üblichen Gedankens: Je mehr KI in Ihren Prozessen arbeitet,
> desto wertvoller wird der modellierte Rahmen — nicht überflüssiger. Der Prozess ist
> nicht die Fessel des Agenten. Er ist der Grund, warum Sie ihn überhaupt einsetzen
> dürfen.
>
> **Was wäre, wenn die Automatisierung kommt und Sie trotzdem geradestehen können?**"

**Botschaft:** Nachvollziehbarkeit und ein modellierter Rahmen sind die Voraussetzung
dafür, KI überhaupt produktiv einsetzen zu dürfen.
**Niemals streichen.** Das ist der Satz, der nach dem Termin hängen bleibt.
**Regie:** Diese Fähigkeit ist jung und in Entwicklung. Als Richtung darstellen, nicht als
fertiges Produkt — sonst kippt die Glaubwürdigkeit, die Szene 9 gleich braucht.

---

### Schluss

#### Szene 9 · 8:40–10:00 · kein Klick, Blickkontakt

> „Und jetzt sage ich Ihnen, was Atlas nicht ist, denn sonst wäre alles davor nichts wert.
>
> **Atlas ersetzt den Synchronisationskern von MIM nicht.** MIM hat einen Metaverse,
> Connector Spaces, deklarative Attributflüsse mit Präzedenz und Join-Regeln. Das ist eine
> eigene Produktkategorie, und Atlas hat davon nichts. Wer heute darauf angewiesen ist,
> braucht dafür weiterhin eine Lösung — Entra ID Governance, ein IGA-Produkt, oder was
> auch immer Ihre Analyse ergibt.
>
> **Atlas ersetzt auch die Zusatzmodule nicht:** kein Privileged Access Management, kein
> Zertifikatsmanagement, kein Self-Service-Passwortzurücksetzen, keine
> Passwortsynchronisation.
>
> Was Atlas ersetzt, ist der Teil, der Ihr Haus ausmacht: die Workflows, die Regeln, die
> Freigaben, die Ausnahmen — alles, was kein Standardprodukt für Sie mitbringt, weil es bei
> Ihnen anders ist als bei allen anderen. Genau dieser Teil steckt heute in MIM fest, und
> genau er wird bei einer Produktablösung erfahrungsgemäss am teuersten.
>
> Und dazu die Offenheit, die ich Ihnen schulde: Atlas ist in aktiver Entwicklung, in einer
> Vorversion, und heute nicht für den unternehmenskritischen Produktivbetrieb freigegeben.
> Was Sie gesehen haben, läuft — aber es ist ein System im Aufbau.
>
> Deshalb schlage ich Ihnen keinen Beschaffungsentscheid vor, sondern eine Messung: Geben
> Sie mir Ihre MIM-Workflow-Exporte. Ich sage Ihnen in einer Woche, wie viel davon
> automatisch übersetzt wird und wo die Handarbeit liegt — und zeige Ihnen einen davon
> hier laufend, mit Attrappen statt echter Anbindungen.
>
> Das kostet Sie eine Datei und eine Woche. Und danach haben Sie eine Zahl, die Sie in
> jeder Ablösungsdiskussion brauchen werden — unabhängig davon, wofür Sie sich am Ende
> entscheiden."

**Botschaft:** Die eigenen Grenzen zu benennen macht den nächsten Schritt klein, billig
und schwer abzulehnen.

---

## 4. Einwandbehandlung

| Einwand | Antwort |
|---------|---------|
| „Wir gehen sowieso in die Cloud, Entra ID Governance macht das." | „Für die Cloud-Identitäten und die Standard-Rezertifizierung wahrscheinlich ja — das ist die richtige Wahl und kein Widerspruch zu dem, was ich zeige. Die Frage ist, wo Ihre hausspezifische Logik landet: die Sonderfälle, die On-Prem-Systeme, die Freigabeketten. Erfahrungsgemäss ist genau das der Teil, den man am Ende nachbaut — und dafür ist das hier der günstigere Ort." |
| „Ein IGA-Produkt bringt Konnektoren und Rollenmodell mit." | „Richtig, und es bringt einen Synchronisationskern mit, den Atlas nicht hat. Die beiden schliessen sich nicht aus: Das IGA-Produkt hält die Identitäten synchron, Atlas führt die Prozesse. Wenn Ihre Analyse zum reinen IGA-Weg führt, ist das eine saubere Entscheidung — nur sollten Sie dann wissen, was er in Workflow-Anpassungen kostet." |
| „Wir haben Hunderte von Workflows." | „Dann ist die Messung aus Szene 2 umso mehr wert. Sie erhalten pro Workflow drei Zahlen, bevor Sie irgendetwas beauftragen." |
| „Wer garantiert uns, dass es dieses Produkt in zehn Jahren noch gibt?" | „Niemand — und das ist bei MIM gerade die Erfahrung, die Sie machen. Der Unterschied ist die Form: Ihre Prozesse liegen als BPMN vor, einem herstellerunabhängigen Standard, den mehrere Engines ausführen. Ihre Regeln liegen als DMN vor. Was Sie hier hineinstecken, ist nicht in einem Dateiformat gefangen, das nur ein Hersteller lesen kann." |
| „Ist das produktionsreif?" | „Nein, heute nicht — Vorversion in aktiver Entwicklung. Deshalb schlage ich eine Messung an Ihren Workflow-Exporten vor und keine Ablösung." |
| „Und die Sicherheit? Das ist unser Identity-System." | „Zugangsdaten liegen verschlüsselt im System und nie im Prozessmodell; die Anbindungen an Verzeichnisdienste und Datenbanken laufen bewusst ausserhalb der Engine, damit ein Verzeichnis-Passwort dort gar nicht ankommt. Für den Schweizer Bundeskontext existiert eine Auseinandersetzung mit dem ISDS-Konzept samt der offenen Punkte." |
| „KI-Agenten in unserem Identity-Prozess? Auf keinen Fall." | „Verständlich, und Sie müssen das auch nicht. Der Punkt in Szene 8 ist der umgekehrte: Wenn Sie irgendwann irgendwo Agenten einsetzen — und das werden Sie —, brauchen Sie genau diese Art von Rahmen und Protokoll. Wer heute ohne beides anfängt, baut sich das Problem für die nächste Revision." |

---

## 5. Fakten, auf die sich der Auftritt stützt

- **MIM 2016 Lifecycle:** Mainstream-Support endete am 14. April 2026, erweiterter Support
  bis 9. Januar 2029 (verlängert vom ursprünglichen 13. Januar 2026); Microsoft empfiehlt,
  nicht mit einer weiteren Verlängerung zu rechnen. *Vor dem Termin gegen die
  Microsoft-Lifecycle-Seite prüfen — Termine können sich erneut ändern.*
- **XOML-Import:** `atlas import-mim` auf der Kommandozeile und *Import MIM workflow
  (XOML)…* im Modeler; der Bericht klassifiziert jeden Knoten als *native*, *preserved*
  oder *manual-review*, nicht übersetzte Anweisungen bleiben in `atlas:mimSource` am
  Element erhalten.
- **Abdeckung der MIM-Konnektoren:** dokumentiert in
  [`docs/comparisons/mim.md`](../comparisons/mim.md) — Active Directory, Entra ID, LDAP,
  MS SQL / MariaDB / PostgreSQL, SOAP, SharePoint-Listen, CSV / Fixed-Width / AVP, LDIF /
  DSML, PowerShell / Python / JavaScript, SCIM. Fehlend und bewusst nicht geplant: Lotus
  Domino, native DB2- und Oracle-Konnektoren, SharePoint UPA.
- **Was fehlt:** Synchronisationskern (Metaverse, Connector Space, Join/Projektion,
  deklarative Attributflüsse mit Präzedenz), PCNS und Passwortsynchronisation, SSPR,
  Privileged Access Management, BHOLD, Certificate Management.
- **Agentenfähigkeit:** Ein Agent ist ein Prozessschritt auf dem Job-Pfad; sein
  Werkzeugkasten sind die im Diagramm enthaltenen Aktivitäten. Diese Fähigkeit ist jung —
  Teile davon sind noch als Entwurf dokumentiert. Im Pitch als Richtung darstellen.
- **Reifegrad:** Entwicklervorschau (`0.x`), ausdrücklich nicht für den Produktivbetrieb
  freigegeben.

---

*Ergänzt den allgemeinen Entscheider-Pitch in
[`atlas-pitch-entscheider.md`](atlas-pitch-entscheider.md). Wo dieser die Fähigkeiten
zeigt, verkauft dieser hier eine Entscheidung: die Zwangsablösung als Einstieg in eine
Prozessplattform.*

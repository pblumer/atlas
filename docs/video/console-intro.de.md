---
# Voice and pace. build.sh fetches the voice (Piper, offline).
# Available: thorsten-low (neutral, male), pavoque-low (darker, male).
voice: thorsten-low
# Speaking pace: 1.0 = normal, higher = slower, lower = faster.
tempo: 0.95
---

# Atlas Console — introduction (2 minutes)
#
# The narration below is German on purpose: it is content, in the language its
# audience speaks. Everything around it — README, script comments — is English like
# the rest of the repository. A second language is a second console-intro.<locale>.md.
#
# Every "## <id>" heading is a scene. The id drives what record.mjs does on screen,
# so it must not be renamed. The text below it is the narration and is yours to
# change: it also sets the length of the scene, because the recording is cut to the
# measured speech duration.
#
# "caption:" is the lower third shown on screen. <b>…</b> emphasizes. An empty
# caption line suppresses it.

## intro
caption: <b>Atlas Console</b> — die Oberfläche des Workflow-Servers

Atlas ist eine BPMN-Workflow-Engine, die aus einer einzigen Programmdatei läuft.
Die Konsole ist die Oberfläche, über die man diesen Server betreibt.

## login
caption: Anmeldung — Konten im Server oder über einen Identity Provider

Ist die Anmeldung aktiviert, beginnt jede Sitzung hier. Die Konten liegen im Server
selbst, alternativ meldet man sich über einen Identity Provider an.

## dashboard
caption: <b>Dashboard</b> — Einstieg und Neuerungen der laufenden Version

Nach der Anmeldung öffnet das Dashboard. Es nennt die drei Schritte: modellieren,
deployen, ausführen. Darunter stehen die Neuerungen der laufenden Version.

## dashboard-stats
caption: Deployments und laufende Instanzen auf einen Blick

Weiter unten steht der Stand dieser Instanz: wie viele Prozessdefinitionen deployed
sind und wie viele Instanzen gerade laufen.

## apps
caption: Der App-Umschalter — Modeler, Tasks, Operations, Panorama, Data

Die Konsole ist eine von mehreren Anwendungen auf demselben Server. Der App-Umschalter
führt zum Modeler, zu den Aufgaben, zum Betrieb und zur Datensicht.

## engine
caption: <b>Engine</b> — ein Knoten, eine Partition

Die Seite Engine zeigt den Kern. Der Zustand wird aus einem fortlaufenden
Write-Ahead-Log materialisiert: Jede Zustandsänderung liegt auf der Platte, bevor sie
sichtbar wird.

## engine-build
caption: Aus welchem Commit der laufende Server gebaut wurde

Darunter steht, aus welchem Commit der laufende Server gebaut wurde — die Auskunft,
mit der man eine Betriebsmeldung einer Codeversion zuordnet.

## workers
caption: <b>Workers</b> — der Katalog der Worker-Typen

Workers ist der Katalog der Worker-Typen: REST, Mail, Datenbanken, Verzeichnisdienste.
Hier wird angebunden, was ein Prozess ausserhalb von Atlas braucht — einmal für die
ganze Organisation.

## ai-access
caption: <b>AI access</b> — Zugang für KI-Assistenten über MCP

AI access öffnet den Server für KI-Assistenten über das Model Context Protocol.
Der Zugriff läuft über API-Token mit begrenzten Berechtigungen.

## org
caption: <b>Organization</b> — Benutzer, Gruppen und Rollen

Organization verwaltet die Benutzer, Gruppen und Rollen dieser Instanz. Teilt man ein
Projekt mit einer Gruppe, erhält jedes Mitglied dieselbe Rolle.

## org-appearance
caption: Die Akzentfarbe gilt für alle Anwendungen der Instanz

Weiter unten steht das Erscheinungsbild. Die Akzentfarbe gilt für jede Anwendung
dieser Instanz.

## logs
caption: <b>Logs</b> — das Serverprotokoll in der Oberfläche

Logs zeigt das Serverprotokoll direkt in der Oberfläche, ohne Zugang zu der Maschine,
auf der der Server läuft.

## audit
caption: <b>Audit log</b> — jede Änderung an Zugriffsrechten

Das Audit-Protokoll hält jede Änderung an Zugriffsrechten fest: wer wann was
freigegeben oder entzogen hat.

## backup
caption: <b>Backup</b> — Entwurfsdaten oder die ganze Instanz

Backup sichert entweder die Entwurfsdaten oder die ganze Instanz samt Write-Ahead-Log.
Das zweite stellt einen vollständigen Server an anderer Stelle wieder her.

## outro
caption: <b>Atlas Console</b> — Dashboard · Engine · Workers · Organization · Logs · Audit · Backup

Das ist die Konsole. Modelliert wird im Modeler, und der Betrieb hat mit Operations
eine eigene Anwendung.

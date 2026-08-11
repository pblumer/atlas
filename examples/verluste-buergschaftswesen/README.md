# Verluste Bürgschaftswesen (SOLL)

BPMN-Umsetzung des SOLL-Prozesses **2.2.1 Verluste Bürgschaftswesen** (DSKU /
SECO) aus der Prozessdokumentation V1.0. Der Prozess bildet die digitale
End-to-End-Bearbeitung einer Vorschussabrechnung für einen Verlustfall im
Bürgschaftswesen ab – von der Einreichung durch eine Bürgschaftsorganisation
über die automatisierte Prüfung und die fachliche Validierung durch DSKU bis
zum dokumentierten Fallabschluss in Acta Nova.

Diese Modelle sind als **privates Atlas-Projekt** „Verluste Bürgschaftswesen
(SOLL)" auf dem Server deployt (Definitionen 113–120) und hier als
versionierbare Quelle abgelegt.

## Modelle

| Datei | Process-ID | Inhalt |
|---|---|---|
| `2.2.1_verluste-buergschaftswesen-soll.bpmn` | `verluste-buergschaftswesen-soll` | Hauptprozess: orchestriert die 7 Subprozesse als Call Activities, mit Validierungs-Gateway, EFV-DLZ/SAP-Message-Events und Zahlungsauftrag-Genehmigung |
| `2.2.1.1_verluste-app-login.bpmn` | `verluste-app-login` | Login vorhanden? / Loginantrag → DSKU-Prüfung → bewilligt \| abgelehnt |
| `2.2.1.2_verlustfall-eroeffnen-hochladen.bpmn` | `verlustfall-eroeffnen-hochladen` | Fall eröffnen, Metadaten, Upload (Excel + PDF), technische Prüfung mit Korrekturschleife |
| `2.2.1.3_vorschussdaten-validieren.bpmn` | `vorschussdaten-validieren` | Auto-Extraktion/Kontrolle + DSKU-Entscheid: validieren \| ablehnen \| Korrektur-/Abklärungsschleife |
| `2.2.1.4_verlustfalldaten-kontrollregister.bpmn` | `verlustfalldaten-kontrollregister` | Übernahme in das Zentrale Kontrollregister Verluste, Abrechnungsdatum, Gesamttotal, systemische Freigabe |
| `2.2.1.5_eigenbeleg-erstellen-versenden.bpmn` | `eigenbeleg-erstellen-versenden` | Eigenbeleg-Entwurf, DSKU-Prüfung mit interner Korrekturschleife, PDF-Zusammenführung, Versand an EFV-DLZ |
| `2.2.1.6_zahlungsstatus-pruefen-bestaetigen.bpmn` | `zahlungsstatus-pruefen-bestaetigen` | Täglicher SAP-Prüflauf (Timer P1D), Statusänderung? / final genehmigt?, DSKU bestätigt Abschluss |
| `2.2.1.7_dokumentation-ablegen.bpmn` | `dokumentation-ablegen` | Register/Status nachführen, Acta-Nova-Subdossier (SECO-332.82), Dokumente ablegen, Referenz speichern, Archivierung melden |

## Rollen (candidateGroups)

- `buergschaftsorganisation` – einreichende/korrigierende Stelle (BG)
- `dsku` – Dossierverantwortliche DSKU (fachliche Prüfung, Validierung,
  Genehmigung, Abschlussbestätigung)

## Systeme / Schnittstellen (als Service-Tasks bzw. Message-Events modelliert)

Verluste App (führende Prozessapplikation), DSKU Web Portal, Zentrales
Kontrollregister Verluste (Datastore), uid.admin.ch, EFV-DLZ, SAP SuPro 100,
Acta Nova.

## Konventionen

- Automatisierte App-/System-Schritte → **Service Tasks** (`zeebe:taskDefinition`).
  Ohne angebundenen Worker parkt der Token dort (im SOLL-Modell erwartet).
- Menschliche Schritte → **User Tasks** mit `candidateGroups`.
- Entscheidungen → **Exclusive Gateways** mit benannten, gelabelten Ausgängen;
  Rückläufe über ein Join-Gateway auf einer separaten Lane (hand-gesetztes
  BPMN-DI, gerade Hauptachse).

## Bewusste Vereinfachungen / offene Punkte (nächste Iteration)

- **2.2.1.3**: Die Unterscheidung *interner Fehler* vs. *Abklärung mit der BG*
  ist derzeit im Schritt „Intern korrigieren / mit BG abklären" zusammengefasst
  und kann in zwei getrennte Pfade aufgetrennt werden. Ebenso ist die
  Warnungs-/Fehler-Logik (akzeptierbare Warnung vs. Ablehnung) auf einen
  Entscheid-Gateway verdichtet.
- **Message-/Service-Anbindung**: EFV-DLZ- und SAP-Interaktionen sind als
  Message-Events bzw. Service-Tasks modelliert, aber noch nicht an reale Worker/
  Correlation-Keys angebunden (Correlation-Key-Platzhalter: `abrechnungsfallId`).
- **Formulare & Datenobjekte**: Start-/User-Task-Formulare (form-js) und die
  BPMN-Datenobjekte (Verlustfall XY, Vorschusstabelle, Prüfereignis, Eigenbeleg …)
  sind noch nicht hinterlegt.
- **DMN**: Die automatische Vorschussdatenkontrolle könnte als
  DMN-Entscheidungstabelle ausgelagert werden.

## Deploy

```
# über MCP (atlas-mcp): Projekt-Drafts speichern und deployen
atlas_deploy_project  { id: "<projektId>" }
```

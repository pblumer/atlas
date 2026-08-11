# Prüfung mit Zeitlimit und DMN-Bewertung

Ein Assessment (Prüfung) als BPMN-Prozess — das Beispiel zu der Frage „kann man
Prüfungen mit Atlas abbilden?". Es zeigt drei Dinge, die eine Workflow-Engine
einer reinen Prüfungssoftware voraushat:

1. **Ein hartes Zeitlimit** als unterbrechendes **Boundary-Timer-Event** am
   User-Task (`PT45M`). Läuft die Frist ab, bevor abgegeben wird, wird die
   Prüfung abgebrochen und der Prozess endet in *„Zeit abgelaufen"* — ohne
   Bewertung.
2. **Die Bewertung als DMN-Entscheidung** ([`notenschluessel.dmn`](notenschluessel.dmn)),
   nicht im Code. Bestehensgrenze und Notenstufen leben in einer
   Entscheidungstabelle, die Fachleute ändern können, ohne die Engine oder das
   BPMN neu zu deployen.
3. **Ein ergebnisabhängiger Folgeschritt**: ein Exklusiv-Gateway verzweigt auf
   `bewertung.bestanden`.

## Ablauf

```
Start "Prüfung gestartet"
 → User-Task "Prüfung ablegen"   (Formular pruefung-fragen → antwort1..4)
    └─ Boundary-Timer PT45M (interrupting) ──→ Ende "Zeit abgelaufen"
 → Script "Punkte zählen"        (antwort1..4 → punkte, je 25 P., FEEL)
 → Business-Rule "Bewerten"      (DMN Notenschluessel: punkte →
                                   bewertung {bestanden, note})
 → Exklusiv-Gateway "bestanden?"
      bewertung.bestanden = true → Ende "Prüfung bestanden"
      sonst (default)           → Ende "Prüfung nicht bestanden"
```

Die vier Fragen sind Single-Choice (form-js `radio`). Der Skript-Task vergibt je
richtiger Antwort 25 Punkte (max. 100). Der Notenschlüssel (`hitPolicy="UNIQUE"`):

| punkte   | bestanden | note           |
|----------|-----------|----------------|
| `< 50`   | false     | nicht bestanden |
| `[50..65)` | true    | befriedigend   |
| `[65..85)` | true    | gut            |
| `>= 85`  | true      | sehr gut       |

## Dateien

| Datei | Rolle |
|-------|-------|
| [`pruefung.bpmn`](pruefung.bpmn) | Der Prozess: User-Task + Boundary-Timer, Skript, Business-Rule-Task, Gateway. |
| [`notenschluessel.dmn`](notenschluessel.dmn) | Die Bewertungsregeln (`decisionId="Notenschluessel"`, Eingabe `punkte`, Ausgaben `bestanden` + `note`). |
| [`pruefung-fragen.form.json`](pruefung-fragen.form.json) | Das Prüfungsformular (vier `radio`-Fragen), gebunden am User-Task via `formId="pruefung-fragen"`. |

## Über MCP deployen und durchspielen

```
atlas_create_project        name="Pruefung_Zeitlimit_DMN"          → projectId
atlas_upload_decision_model handle="notenschluessel" xml=<dmn>
atlas_register_decision     name="Notenschluessel" modelRef="notenschluessel" projectId=…
atlas_save_form             id="pruefung-fragen" schema=<form>      projectId=…
atlas_save_draft            xml=<bpmn>                              projectId=…
atlas_deploy_project        id=<projectId>                          → definitionKey
atlas_create_instance       key=<definitionKey>
atlas_list_tasks            limit=1                                 → taskKey
atlas_complete_task         key=<taskKey> variables={antwort1..4}
atlas_instance_decisions    key=<instanceKey>   # DMN-Trace: welche Regel traf
atlas_instance_variables    key=<instanceKey>   # punkte + bewertung
```

Gegen einen laufenden Atlas-Server (`0.1.0-dev`) verifiziert:

- **Alle vier richtig** → `punkte=100`, DMN-Regel `r_sehrgut` trifft →
  `bewertung={bestanden:true, note:"sehr gut"}` → Ende *„Prüfung bestanden"*.
- **Nur eine richtig** → `punkte=25`, Regel `r_durchgefallen` trifft →
  `bewertung={bestanden:false, note:"nicht bestanden"}` → Ende
  *„Prüfung nicht bestanden"*.

Das Boundary-Timer-Event `frist` bleibt in der Timeline als paralleler Token
aktiv, solange der User-Task offen ist — bei Abgabe wird es verworfen, bei
Fristablauf unterbricht es den Task und routet nach *„Zeit abgelaufen"*.

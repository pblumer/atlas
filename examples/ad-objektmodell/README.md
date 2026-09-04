# AD-Objektmodell: ArchiMate-Export nach Atlas

Ein Enterprise-Architect-Export des Active-Directory-Objektmodells lag als
**ArchiMate 3.1 Model Exchange File** vor und liess sich nicht als
Informationsmodell importieren.

## Warum ArchiMate nicht importierbar ist

Atlas liest als Informationsmodell genau zwei Dokumente (ADR-0232,
[`api/infomodel/import.go`](../../api/infomodel/import.go)):

* Atlas' eigenes JSON, und
* **UML XMI 2.5.1**, wie es ein UML-Werkzeug exportiert.

ArchiMate ist eine andere Notation. `DetectImportFormat` sieht ein `<` und waehlt
den XMI-Pfad; dieser sucht nach `packagedElement`, `ownedMember`, `ownedElement`
oder `nestedClassifier`. Eine ArchiMate-Datei fuehrt ihre Elemente unter
`<elements><element>`, also findet der Reader nichts und `ParseImport` bricht ab:

```
this document contains no classes Atlas could read
```

Der Unterschied ist nicht nur syntaktisch. ArchiMate kennt fuer ein
`BusinessObject` keine typisierten Attribute -- der Export legte jedes
Klassenattribut als `<property key="attr_..." value="name | Visibility: ... |
Multiplicity: [0..*] | ... | Desc: ..."/>` ab, also als Zeichenkette. Auch ein
ArchiMate-faehiger Reader haette daraus keine Attribute gewonnen.

## Der Konverter

[`archimate2xmi.py`](archimate2xmi.py) bildet ab:

| ArchiMate | UML XMI 2.5.1 | Atlas |
| --- | --- | --- |
| `element[@xsi:type='BusinessObject']` | `uml:Class` | Business Object |
| `property[@key='attr_*']` | `uml:Property` (`ownedAttribute`) | Attribut, Typ `string` |
| `Multiplicity: [0..*]` | `lowerValue`/`upperValue` | `0..*` |
| `SpecializationRelationship` | `uml:Generalization` | Generalisierung |
| `documentation` | `ownedComment/body` | Dokumentation |

Werkzeug-Metadaten (`owner`, `status`, `accessRights`, `createdBy`, ...) sind
keine fachlichen Attribute und wandern als Herkunftszeile in die Dokumentation
der Klasse.

```bash
python3 archimate2xmi.py EFD-BIT-AD-Objekte.archimate.xml > ad-objekte.xmi
```

## Bekannte Luecken des Quellexports

Der Export war unvollstaendig. Er kuendigte rund 271 Attribute an, enthielt aber
nur 40; die uebrigen standen als Platzhalter darin
(`<!-- ... weitere 167 Attribute von Top ... -->`,
`<property key="attr_..." value="... 14 Attribute von Security-Principal ..."/>`).

| Klasse | angekuendigt | im Export enthalten |
| --- | --- | --- |
| Top | 185 | 19 |
| Person | 7 | 7 |
| organizationalPerson | 64 | 14 |
| Security-Principal | 14 | 0 |
| User | 15 | 0 |

Der Konverter erfindet nichts: er uebernimmt, was dasteht, meldet jeden
Platzhalter auf stderr und vermerkt ihn in der Dokumentation der betroffenen
Klasse. Fuer ein vollstaendiges Modell braucht es einen vollstaendigen
Quellexport.

Weiter offen:

* **Typen.** Der Export nennt keine Datentypen, alle Attribute werden `string`.
  Die LDAP-Syntaxen (Integer8, Boolean, GeneralizedTime) muessten nachgepflegt
  werden.
* **Multiplizitaet.** Der Export setzt durchgaengig `[0..*]`, obwohl die meisten
  AD-Attribute einwertig sind.
* **Business Key.** Keine Klasse deklariert eine Identitaet. Ohne sie laesst sich
  kein Data Store an die Klasse haengen und keine instanzuebergreifende
  Zuordnung ueber `atlas_data_objects` fahren. Kandidat waere
  `distinguishedName` oder `objectGUID` mit Multiplizitaet `1`.

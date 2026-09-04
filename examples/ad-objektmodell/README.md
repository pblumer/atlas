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

---

# Zweite Quelle: Microsofts Schemadokumentation

Weil der EA-Export unvollstaendig war, wurde das Modell aus der oeffentlichen
Schemadokumentation von Microsoft neu erzeugt. Sie liegt als Markdown im
Repository `MicrosoftDocs/win32` unter `desktop-src/ADSchema` -- derselbe
Bestand, der learn.microsoft.com/windows/win32/adschema speist: 258 Klassen und
1420 Attribute, je Klasse eine Attributtabelle pro Windows-Version mit den
Spalten Attribut, Mandatory und "Derived from", je Attribut LDAP-Display-Name,
Syntax und Is-Single-Valued.

```bash
git clone --filter=blob:none --sparse https://github.com/MicrosoftDocs/win32.git
cd win32 && git sparse-checkout set desktop-src/ADSchema
python3 adschema2atlas.py win32/desktop-src/ADSchema --format json > ad-identity.json
python3 adschema2atlas.py win32/desktop-src/ADSchema --format xmi  > ad-identity.xmi
```

## Wie sich die Quellen unterscheiden

Gegenuebergestellt wurden die vom EA-Export angekuendigten Attributzahlen und
die Zahl der Attribute, die Microsoft der jeweiligen Klasse selbst zuschreibt
(Stand Windows Server 2012, der neuesten dort dokumentierten Version):

| Klasse | EA behauptet | Microsoft eigene | Deckung |
| --- | --- | --- | --- |
| Top | 185 | 118 | 18 der 19 im Export enthaltenen bestaetigt |
| Person | 7 | 7 | 5 von 7 bestaetigt |
| Organizational-Person | 64 | 62 | alle 14 im Export enthaltenen bestaetigt |
| Security-Principal | 14 | 14 | Export enthielt keine |
| User | 15 | 150 | Export enthielt keine |

Person, Organizational-Person und Security-Principal decken sich also, Top und
User nicht. Drei Attribute des Exports finden sich bei Microsoft nicht an der
Stelle, an der EA sie fuehrt:

* `msExchOWAForceSaveFileTypesBL` ist eine Exchange-Schemaerweiterung. Microsoft
  dokumentiert unter adschema nur das Basisschema; die 185 fuer Top duerften die
  Exchange-Erweiterungen des konkreten Verzeichnisses mitzaehlen.
* `distinguishedName` und `wWWHomePage` fuehrt EA an Person, Microsoft an Top.
  Fachlich derselbe Sachverhalt, nur eine Stufe hoeher in der Hierarchie.

Die 15 fuer User bleiben unerklaert -- Microsoft zaehlt dort 150 eigene
Attribute. Vermutlich war der Export an dieser Stelle bereits gekuerzt.

## Was der Generator abbildet

| Microsoft | Atlas |
| --- | --- |
| `c-*.md`, Klassenseite | Business Object |
| Attributtabelle, "Derived from" = die Klasse | eigenes Attribut (geerbte stehen an der Oberklasse) |
| `Ldap-Display-Name` | Attributname -- das, was ein Skript oder LDAP-Filter schreibt |
| `Syntax` | Primitiv: String/Object/Sid -> string, Enumeration/Interval -> number, Boolean -> boolean, Generalized-Time -> dateTime |
| `Is-Single-Valued` + `Mandatory` | Multiplizitaet 1, 0..1, 1..* oder 0..* |
| `Subclass of` | Generalisierung |
| `Auxiliary Classes` (nur Security-Principal, Mail-Recipient) | Generalisierung |
| Beschreibung aus dem Front Matter | Dokumentation der Klasse bzw. des Attributs |

Der Ausschnitt heisst Identity-Kern: alles, was Account-, Berechtigungs- und
Verzeichnisprozesse anfassen, samt der automatisch ergaenzten Oberklassen. Das
ergibt 18 Klassen, 527 Attribute und 22 Generalisierungen.

## Bewusste Abweichungen

* **Hilfsklassen als Generalisierung.** In AD sind `Security-Principal` und
  `Mail-Recipient` auxiliary classes, in UML gibt es das nicht. Sie tragen mit
  `objectSid`, `sAMAccountName` und den Mailadressen echte fachliche Attribute,
  also werden sie wie im bisherigen EA-Modell als Oberklasse gefuehrt. POSIX-
  und Exchange-Hilfsklassen bleiben draussen, damit der Ausschnitt nicht
  ausufert.
* **`objectGUID` als Geschaeftsschluessel.** Microsoft fuehrt das Attribut als
  optional, weil das Verzeichnis es selbst setzt. Atlas verlangt von einem
  Business Key, dass er vorhanden und einwertig ist, also steht er hier auf
  Multiplizitaet 1. Er haengt an `Top`; Atlas vererbt den Schluessel nicht ueber
  die Generalisierung, wer also einen `User` als Datenobjekt fuehrt, deklariert
  ihn dort erneut.
* **Grobe Typen.** `Interval` ist in AD eine 64-Bit-Zahl in
  100-Nanosekunden-Schritten seit 1601 (so etwa `accountExpires`), hier schlicht
  `number`. Ein Prozessinformationsmodell sagt, dass ein Konto ablaeuft, nicht
  wie das Verzeichnis den Zeitpunkt kodiert.
* **Layout.** Die JSON-Variante bringt Koordinaten mit: Spalte nach
  Vererbungstiefe, in der Spalte gestapelt nach Kastenhoehe. Atlas uebernimmt
  sie. Die XMI-Variante kann das nicht -- XMI haelt die Geometrie in einer
  eigenen Datei -- dort legt Atlas ein Raster an.
* **Textbereinigung.** Microsofts Markdown-Quellen enthalten Escapes und
  Reste kaputter HTML-Entities (`\ 8211;` statt eines Gedankenstrichs, ein
  doppelt escapter UNC-Pfad). `unescape()` raeumt beides auf, laesst die
  Backslashes eines echten UNC-Pfads aber stehen.

## Grenze beim Hochladen ueber MCP

`atlas_import_information_model` und `atlas_save_information_model` tragen das
Dokument inline im Aufruf. Die vollstaendige Datei mit allen 527
Attributbeschreibungen ist 161 KB und damit zu gross dafuer. In Atlas steht
deshalb je Attribut der erste Satz der Microsoft-Beschreibung, auf 130 Zeichen
begrenzt; die Dateien hier tragen den vollen Text samt LDAP-Syntax. Wer den
will, importiert `ad-identity.json` ueber die Oberflaeche.

Zweite Stolperstelle: `atlas_save_information_model` ersetzt die Klassen und
vergibt fuer jede eine neue Id. Werden nur `classes` gesendet, zeigen die
gespeicherten Generalisierungen anschliessend ins Leere und der Server weist
das Modell als ungueltig zurueck. `classes` und `associations` gehoeren in
denselben Aufruf, dann werden die Enden mitgezogen.

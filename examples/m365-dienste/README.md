# Microsoft-365-Dienste – ein Shop mit Governance

Ein Katalog aus der Sicht einer IT-Abteilung, die ihre Microsoft-365-Dienste über Atlas
anbietet: Was eine Person, ein Team oder eine Funktion bestellen kann, wird von den
Worker-Typen **Entra**, **Exchange** und **Teams Phone** bereitgestellt, geändert und
wieder entzogen. Das Paket enthält die Kataloge, die Produkte, alle Prozesse, die
Entscheidungstabellen, die Formulare (darunter zwei öffentliche) und eine wöchentliche
Governance-Prüfung, zweisprachig (Deutsch und Englisch).

Die Worker heissen `ucc_entra`, `ucc_exchange` und `ucc_teams`. Wer andere Namen
verwendet, ersetzt sie in den `.bpmn`-Dateien (Attribut `connector`).

## Inhalt

| Datei | Inhalt |
|---|---|
| `atlas.json` | Manifest der Applikation «Beispiel: Microsoft-365-Dienste» (Schlüssel `beispiel-m365-dienste`): 15 Prozesse, 21 Formulare, 5 Entscheidungsreferenzen |
| `katalog.json` | Das Katalogdokument: zwei Kataloge, neun Produkte, eine Beziehung |
| `fragen.json` | Was jeder Platzhalter im Katalogdokument fragt (Zielgruppen, Genehmigende) |
| `*.bpmn` | Die Prozesse, je mit Dokumentation an Prozess und Elementen |
| `*.dmn` | Die fünf Entscheidungsmodelle |
| `*.form.json` | Bestell-, Aktions-, Aufgaben- und öffentliche Formulare |
| `installieren.sh`, `installieren.ps1` | Installation in eine Atlas-Instanz: Entscheidungsmodelle, Applikation, Veröffentlichung, Katalog |
| `antworten.beispiel.json` | Vorlage für die Antworten auf `fragen.json` |

## Die Kataloge

| Katalog | Rang | Zielgruppe | Produkte |
|---|---|---|---|
| Microsoft 365 für Mitarbeitende (`cat-m365-mitarbeitende`) | 500 | `{{zielgruppe}}` | Lizenz, Telefonie, Team, Funktionspostfach, Verteiler, Postfachzugriff |
| Microsoft 365: Kommunikation und Telefonie (`cat-m365-ucc`) | 510 | `{{zielgruppe-ucc}}` | alle neun |

Eine Person sieht im Shop genau einen Katalog: den mit dem höchsten Rang unter denen, die
ihre Gruppen erreichen. Der UCC-Katalog bietet deshalb auch die allgemeinen Produkte an.
Die Ränge müssen auf der Zielinstanz frei sein; sonst sind sie in `katalog.json` anzupassen.

## Die Produkte

| Produkt | Worker | Genehmigung | Prozess | Aktionen nach der Bereitstellung |
|---|---|---|---|---|
| Microsoft 365: Lizenz mit Postfach (`m365-lizenz`), Varianten E3, E5, F3 | Entra, Exchange | vorgesetzte Person der betroffenen Person | `lizenz.bpmn` (Lebenszyklus) | Lizenz zurückgeben, Abwesenheitsnotiz setzen, Abwesenheitsnotiz beenden |
| Teams-Telefonie mit eigener Rufnummer (`m365-telefonie`), setzt die Lizenz voraus | Entra, Teams Phone | `{{telefonie-team}}` | `telefonie.bpmn` (Lebenszyklus) | Telefonie aufheben |
| Microsoft Teams: Team (`m365-team`) | Entra | vorgesetzte Person der verantwortlichen Person | `team.bpmn` (Lebenszyklus) | Team archivieren, Person aufnehmen, Kanal anlegen |
| Funktionspostfach (`m365-funktionspostfach`) | Exchange | vorgesetzte Person der verantwortlichen Person | `funktionspostfach.bpmn` (Lebenszyklus) | entfernen, Zugriff erteilen, Zugriff entziehen |
| Verteilerliste (`m365-verteiler`) | Exchange | keine | `verteiler.bpmn` (Lebenszyklus) | entfernen, Mitglied aufnehmen, Mitglied entfernen |
| Zugriff auf ein Postfach (`m365-postfachzugriff`), 365 Tage befristet | Exchange | vorgesetzte Person der Person, die den Zugriff erhält, je nach Regel zusätzlich Informationssicherheit | `postfachzugriff-erteilen.bpmn`, `postfachzugriff-entziehen.bpmn` (getrennte Prozesse) | Rückgabe |
| Sitzungszimmer (`m365-raum`) | Exchange | `{{facility-management}}` | `raum-einrichten.bpmn`, `raum-entfernen.bpmn` (getrennte Prozesse) | Rückgabe |
| Anrufwarteschlange (`m365-warteschlange`) | Teams Phone, Entra | `{{telefonie-team}}` | `warteschlange.bpmn` (Lebenszyklus) | entfernen, Agenten ändern |
| Telefonzentrale (`m365-telefonzentrale`) | Teams Phone, Entra | `{{telefonie-team}}` | `telefonzentrale.bpmn` (Lebenszyklus) | entfernen |

Die Rufnummer wählt die Gruppe `M365-Telefonie` aus ihrem Nummernplan (Aufgabe «Rufnummer
wählen»), weil Atlas keinen Nummernbestand führt. Zugewiesen wird sie danach vom
Teams-Phone-Worker (`ensure-phone-number`, als Direct-Routing-Nummer); gehört sie schon
jemandem, kommt die Aufgabe mit einem Hinweis zurück (Grenzereignis auf
`TEAMSPHONE_NUMBER_IN_USE`). Bei der Rückgabe gibt `remove-phone-number` die persönliche
Nummer frei, bevor die Lizenz geht, und `remove-resource-account` gibt die Nummer einer
Warteschlange oder Telefonzentrale frei, bevor es das Konto löscht. Ein Ressourcenkonto
nimmt eine Rufnummer nur mit Nutzungsort und der Lizenz *Microsoft Teams Phone Resource
Account* an; beides gibt ihm der Entra-Worker gleich nach dem Anlegen.

## Die Genehmigung durch die vorgesetzte Person

Lizenz, Team, Funktionspostfach und Postfachzugriff nennen als Genehmigungsart den Prozess
`proc_m365_genehmigung_vorgesetzter` (`genehmigung-vorgesetzter.bpmn`) und nicht die
eingebaute Art «superior». Der Grund: Die eingebaute Art fragt die vorgesetzte Person der
**bestellenden** Person und kennt dafür nur deren Atlas-Konto. Bestellt wird hier aber für
jemanden, den das Bestellformular nennt: `upn` (Lizenz), `besitzer` (Team,
Funktionspostfach) oder `person` (Postfachzugriff). Diese Antworten erhält nur ein
Genehmigungsprozess der Installation selbst (ADR-0441).

Ablauf:

1. **Vorgesetzten lesen.** Eine Entra-Aufgabe mit der Operation `get-user-manager` liest
   über den Worker `ucc_entra` den Vorgesetzten der betroffenen Person; welche Person das
   ist, wählt ihr `userId`-Ausdruck aus den Bestellangaben. «Kein Vorgesetzter» ist eine
   Antwort (das Ergebnis ist leer). Kennt Entra ID die Person nicht, endet die Aufgabe mit
   dem BPMN-Fehler `ENTRA_USER_NOT_FOUND`, den ein Fehler-Grenzereignis fängt. Beide Fälle
   führen zur Ersatzgruppe, die den Grund sieht. Ein technischer Fehler (Anmeldung,
   Berechtigung, Netz) wird zum Incident, damit nicht still jede Genehmigung bei der
   Ersatzgruppe landet. Nennt die Bestellung niemanden, schlägt die Aufgabe ebenfalls
   fehl; die Bestellformulare verlangen die Angabe.
2. **Atlas-Konto suchen** (`GET /api/v1/users/assignable`). Treffer ist ein Konto, dessen
   Benutzername gleich der UPN oder der Mailadresse des Vorgesetzten ist; sonst genau ein
   Konto mit dem Teil vor dem «@» als Benutzernamen (kleingeschrieben, nur `a-z 0-9 . - _`,
   so benennt Atlas Konten aus Entra ID) und gleichem Anzeigenamen.
3. **Aufgabe** an dieses Konto, sonst an die **Ersatzgruppe**: `approval.ref` des Produkts,
   wenn gesetzt, sonst die Gruppe `M365-Administration`. Beide Aufgaben tragen das Formular
   `genehmigung` der Plattform; der Posteingang zeigt dafür «Genehmigen» und «Ablehnen» und
   die Angaben der Bestellung.
4. **Ergebnis an den Auftrag:** `…/start` mit `approvedBy` (wer die Aufgabe abgeschlossen
   hat) oder `…/decision` mit der Begründung der Ablehnung.

Daneben benachrichtigt der Prozess die entscheidende Person oder Gruppe per Mail, erinnert
die vorgesetzte Person nach drei Tagen und reicht die Genehmigung nach sieben Tagen an den
nächsthöheren Vorgesetzten weiter (`…/escalate`). Die Aufgabe bleibt dabei offen.

**Personendaten.** `upn`, `besitzer`, `person`, `postfach` und `anlass` sind im
Genehmigungsprozess als Personendaten der empfangenden Person deklariert. Kein Ausdruck, den
die Engine auswertet, liest sie (der Compiler weist das zurück): die betroffene Person wählt
der `userId`-Ausdruck der Entra-Aufgabe, der erst bei der Übergabe des Jobs an den Worker
ausgewertet wird, und den Text der Mail bildet der Mail-Worker; beide erhalten den
Klartext. Der
Vorgesetzte ist nicht deklariert, weil er nicht die empfangende Person ist und Gateway und
Kontosuche ihn lesen.

**`anlass` statt `begruendung`.** Das Bestellformular des Postfachzugriffs fragt den Anlass
unter dem Schlüssel `anlass`. `begruendung` ist die Variable, in die der Posteingang die
Begründung einer Ablehnung schreibt; unter demselben Namen hätte die Antwort der
bestellenden Person den Grund der Ablehnung überdeckt bzw. ihn als Personendatum gesperrt.

## Die Entscheidungen

| Modell (Handle) | Entscheidung | Wofür |
|---|---|---|
| `m365-namensregel` | `M365Namensregel` | Namenskonvention: Präfix je Objekttyp (TM, FP, VL, RM, CQ, AA), Sperrliste reservierter Bezeichnungen, Längengrenze, Begründung jeder Ablehnung |
| `m365-lizenzprofil` | `M365Lizenzprofil` | Variante, Zusatzlizenz der Telefonie oder Lizenz eines Ressourcenkontos → SKU-ID |
| `m365-postfachzugriff` | `M365PostfachzugriffFreigabe` | Postfachtyp und Zugriffsart → Zusatzfreigabe durch die Informationssicherheit ja/nein |
| `m365-telefonie` | `M365TelefonieSprachregion` | Sprachregion → Sprache und Zeitzone der Teams-Telefonie |
| `m365-verteiler-zuordnung` | `M365VerteilerZuordnung` | öffentliche Auswahl → interne Verteilerliste und Prüfpflicht |

Jede Tabelle hat eine Auffangregel, die im Zweifel ablehnt bzw. die Prüfung verlangt
(fail-closed).

## Die öffentlichen Formulare

| Prozess | Formular | Ablauf |
|---|---|---|
| `partnerzugang.bpmn` | `m365-partnerzugang-antrag` | Externe Person beantragt einen Zugang → `M365-Administration` prüft, wählt die Gültigkeit und setzt das Initialpasswort → Entra legt das Konto an (`ext.`-Präfix, `employeeType` Partner) → nach Ablauf sperrt der Prozess das Konto selbst |
| `verteiler-anmeldung.bpmn` | `m365-verteiler-anmeldung` | Externe Adresse meldet sich bei einer Liste an oder ab → je nach Entscheidung prüft `M365-Administration` → Exchange legt den Mail-Kontakt an (falls nötig) und nimmt ihn auf bzw. entfernt die Mitgliedschaft |

Ein öffentlicher Antrag legt nie direkt etwas an: Konten entstehen nur nach einer
menschlichen Prüfung. Der öffentliche Link wird nach der Installation bewusst erzeugt
(siehe unten), nicht durch den Import.

## Governance

- **Namensregel als Entscheidung.** Die bestellende Person gibt nur Bereichskürzel und
  Bezeichnung an; Anzeigename und Alias bildet `M365Namensregel`
  (`FP-FIN Kreditoren`, `fp-fin-kreditoren@<MAIL_DOMAIN>`). Reservierte Bezeichnungen
  (admin, postmaster, security, support …) und zu lange Namen werden mit Begründung
  abgelehnt; die Position steht danach auf `rejected`, nicht als Störung.
- **Herkunftskennzeichnung.** Jedes Exchange-Objekt erhält `CustomAttribute1` =
  `atlas-katalog`, `CustomAttribute2` = Produkt, `CustomAttribute3` = Auftrag/Position,
  `CustomAttribute4` = verantwortliche Person (beim Raum: Standort). Rückgabe und
  Änderungen finden das Objekt über diese Kennzeichnung, nicht über den Namen.
- **Governance-Prüfung.** `governance-pruefung.bpmn` sucht wöchentlich Funktionspostfächer
  und Verteiler ohne Kennzeichnung sowie Microsoft-365-Gruppen ausserhalb der Namensregel
  und legt den Befund `M365-Administration` vor. Der Befund entzieht nichts; gehandelt wird
  durch Menschen.
- **Zusatzfreigabe und Befristung.** Vollzugriff und «Senden als» auf ein persönliches
  Postfach verlangen die Zustimmung der Informationssicherheit; der Zugriff ist auf ein
  Jahr befristet.
- **Besitz.** Teams, Funktionspostfächer und Verteiler entstehen mit einer verantwortlichen
  Person; das Recht steht im Bestand der empfangenden Person und ist rezertifizierbar.
- **Personendaten.** UPNs, Namen, Begründungen und Rufnummern sind in den Prozessen als
  Personendaten deklariert (`atlas:personal`) und werden verschlüsselt gespeichert. Sie
  stehen nur in Worker-Ausdrücken, nie in Bedingungen.

## Voraussetzungen auf der Zielinstanz

0. **Ein Atlas-Server, der Positionen mit Variante startet.** Die Lizenz ist das einzige
   Produkt mit Varianten (Standard, Erweitert, Frontline). Ältere Server setzten die
   Positions-ID `m365-lizenz#standard` unkodiert in die Adresse ihres Systemprozesses
   «Shop: Auftrag erfüllen»; die Bestellung blieb dann mit dem Incident «a line cannot be
   set to here» stehen. Behoben im Eintrag «A position ordered in a variant is started
   again» des CHANGELOG. Alle anderen Produkte sind davon nicht betroffen.
1. **Worker** (Console → Workers), jeweils mit eigener App-Registrierung; die Einrichtung
   beschreibt das Handbuch unter «Runbooks»:
   - `ucc_entra` (Microsoft Entra ID): `User.ReadWrite.All`, `Group.ReadWrite.All`. Für
     Teams-Mitglieder, Kanäle und das Archivieren verlangt Microsoft Graph laut seiner
     Dokumentation zusätzlich `TeamMember.ReadWrite.All`, `Channel.Create` und
     `TeamSettings.ReadWrite.All` (an einem Tenant nicht geprüft).
   - `ucc_exchange` (Microsoft Exchange): `Exchange.ManageAsApp` und eine Exchange-Rolle
     (z.B. *Exchange Recipient Administrator*).
   - `ucc_teams` (Microsoft Teams Phone): Rolle *Teams Administrator* und
     `User.ReadWrite.All`; PowerShell 7 mit dem Modul MicrosoftTeams auf dem Worker-Host.
2. **Secret `atlas`** im Vault: ein API-Token mit der Rolle `operator`. Damit melden die
   Prozesse ihre Positionen an die Bestellung. Am einfachsten unter **Console → Administration → API tokens**:
   Scope `full`, «Direkt im Vault ablegen» angekreuzt, Name `atlas`. Das Geheimnis verlässt
   den Server dann nie.
3. **Für die Genehmigung durch die vorgesetzte Person** (Lizenz, Team, Funktionspostfach,
   Postfachzugriff):
   - der Worker `ucc_entra` aus Punkt 1; er liest den Vorgesetzten mit `User.Read.All`,
     das in `User.ReadWrite.All` enthalten ist;
   - Atlas-Konten der vorgesetzten Personen, am einfachsten aus der Kontenspiegelung von
     Entra ID; wer kein Konto hat, wird durch die Ersatzgruppe vertreten;
   - der Vault des Servers (Vorgabe), weil der Prozess Personendaten deklariert;
   - für die Benachrichtigung ein Mail-Worker `mail`. Fehlt er, bleibt nur die Nachricht
     liegen; die Genehmigung läuft.
4. **Gruppen** in Atlas: die beiden Zielgruppen, `M365-Telefonie`, `M365-Administration`,
   `Informationssicherheit` und die Gruppe des Facility Managements. Die Aufgaben der
   Prozesse adressieren `M365-Telefonie`, `M365-Administration` und
   `Informationssicherheit` über ihren Namen.
5. **Applikationskonstanten** (Console → Applikation → Constants), nach dem Import:

   | Konstante | Fallback | Bedeutung |
   |---|---|---|
   | `MAIL_DOMAIN` | `contoso.ch` | Domain der Adressen von Funktionspostfächern, Verteilern, Räumen und Ressourcenkonten |
   | `USAGE_LOCATION` | `CH` | Nutzungsort, ohne den Graph keine Lizenz zuweist |
   | `PARTNER_UPN_DOMAIN` | `contoso.onmicrosoft.com` | Domain der Partnerkonten |
   | `ATLAS_API_BASE` | `http://127.0.0.1:8080` | Adresse, unter der die Prozesse Atlas selbst erreichen, wenn eine Rückgabe oder Aktion sie nicht mitbringt |

## Installieren

```sh
ATLAS_TOKEN=atlasat_… ./installieren.sh https://atlas.intern.example.ch antworten.json
```

bzw. unter Windows

```powershell
$env:ATLAS_TOKEN = 'atlasat_…'
.\installieren.ps1 -Server https://atlas.intern.example.ch -Antworten antworten.json
```

Das Skript

1. lädt die fünf Entscheidungsmodelle unter ihren Handles hoch
   (`POST /api/v1/dmn-models?handle=…`). Die Applikation trägt nur Referenzen auf sie, und
   das Veröffentlichen prüft, dass es sie gibt;
2. importiert die Applikation als Quellbaum (`POST /api/v1/applications/source`);
3. veröffentlicht sie (`POST /api/v1/applications/{id}/publish`);
4. füllt die Platzhalter des Katalogdokuments mit den Gruppen-IDs der Instanz und
   importiert es (`POST /api/v1/catalogs/import`).

Das Token braucht die Rolle `modeler`. **Schritt 4 verlangt die Rolle `productmanager`,
und ein API-Token trägt sie nie** (ein von einem Admin erzeugtes Token trägt `modeler`,
`operator` und `user`). Auf einer Instanz mit Anmeldung lehnt der Server Schritt 4 deshalb
ab; das Skript legt dann das ausgefüllte Dokument als `katalog-zum-import.json` ab, und eine
angemeldete Person mit `productmanager` lädt es in der Console unter **Catalogue → Import**
hoch, mit «veröffentlichen». Aus demselben Grund gelingt `atlas import` mit Token auf einer
solchen Instanz nur bis zum Katalog.

`antworten.json` beantwortet `fragen.json`; Gruppen dürfen mit Namen oder ID angegeben
werden (Vorlage: `antworten.beispiel.json`).

**Wem die Applikation gehört.** Die importierte Applikation gehört dem Token, das sie
angelegt hat (`system:token:…`), und ist privat. Ein zweiter Lauf mit demselben Token
aktualisiert sie, statt sie zu verdoppeln; ein anderes Token erhält «no application with
that key». Nach der Installation die Applikation deshalb in der Console einer Person oder
einem Team übergeben bzw. teilen; ein Admin sieht und verwaltet sie auch so.

Ganz ohne Skript geht es auch in der Console: die fünf `.dmn`-Dateien im Modeler unter den
Handles `m365-lizenzprofil`, `m365-namensregel`, `m365-postfachzugriff`, `m365-telefonie`
und `m365-verteiler-zuordnung` hochladen, die Applikation über **Import source** (ein
`.tar.gz` mit `atlas.json` und den genannten Dateien) importieren und veröffentlichen,
dann `katalog.json` mit den vier Gruppen-IDs anstelle der Platzhalter unter
**Catalogue → Import** hochladen.

Bezieht die Zielinstanz ihre Entscheidungen aus einem temis-Dienst
(`ATLAS_DMN_RESOLVER_URL`), werden die fünf Modelle dort unter denselben Namen angelegt,
statt hochgeladen.

### Öffentliche Links erzeugen

```sh
curl -X POST "$SERVER/api/v1/public-links" -H "Authorization: Bearer $ATLAS_TOKEN" \
  -H 'Content-Type: application/json' -d '{"processId":"proc_m365_partnerzugang"}'
curl -X POST "$SERVER/api/v1/public-links" -H "Authorization: Bearer $ATLAS_TOKEN" \
  -H 'Content-Type: application/json' -d '{"processId":"proc_m365_verteiler_anmeldung"}'
```

Die Antwort nennt `/public/forms/<token>`. Ein Link lässt sich jederzeit widerrufen.

## Was geprüft ist und was nicht

Geprüft gegen einen lokalen Atlas-Server aus diesem Repository (Teams Phone im Mock-Modus):
Installation mit `atlas import` (zweimal), Veröffentlichung beider Kataloge, alle fünf
Entscheidungen mit ihren Regeln, eine Bestellung der Anrufwarteschlange von der
Genehmigung über Namensregel, Sprache, Ressourcenkonto, Warteschlange und
Rufnummernaufgabe bis `done` (damals noch mit der manuellen Zuweisung der Rufnummer),
die Aktion «Agenten ändern» (`completed`), die Rückgabe (`returned`), die Ablehnung eines
Verteilers mit reservierter Bezeichnung (`rejected` mit Begründung), beide öffentlichen
Formulare ohne Anmeldung bis zum jeweiligen Entra- bzw. Exchange-Schritt, die
Verschlüsselung der Personendaten und der Postfachzugriff mit Zusatzfreigabe.

Nach der Umstellung auf `ensure-phone-number` geprüft gegen einen lokalen Atlas-Server,
Teams Phone im Mock-Modus. Die Entra-Jobs wurden von Hand über `POST /api/v1/jobs/activate`
und `…/complete` abgeschlossen, und die Prozesse liefen ohne `atlas:personal`, weil sie über
eine Nachricht statt aus einer Bestellung starteten. Geprüft wurden:

- zwei Warteschlangen und eine Telefonzentrale bis `done`, je mit Nutzungsort und Lizenz
  des Ressourcenkontos (der Body von `assign-license` trägt die SKU `440eaaa8-…`);
- eine schon vergebene Nummer, die über das Grenzereignis an «Rufnummer wählen»
  zurückging (`nummerVergeben = true`, die alte Nummer im Feld); die zweite Wahl,
  mit Leerzeichen geschrieben, wurde normalisiert zugewiesen;
- die Rückgabe einer Warteschlange und der Telefonzentrale (`returned`); die Nummer liess
  sich danach erneut zuweisen;
- die persönliche Telefonie mit Zuweisung und Freigabe der Nummer.

Dass das Formular bei `nummerVergeben = true` den Hinweis zeigt, ist nicht in der Oberfläche
geprüft.

Ebenso geprüft: `installieren.sh` gegen eine frische Instanz mit Anmeldung (Schritte 1–3
mit einem Admin-Token, Schritt 4 abgelehnt und danach über eine angemeldete Sitzung
importiert, beide Kataloge veröffentlicht).

Auf atlas.blumer.cloud über die Shop-Oberfläche bestellt; dabei fielen zwei Fehler auf,
die die Bestellungen über die API nicht zeigten: Das Bestellformular des Sitzungszimmers
fragte die Plätze mit einem Zahlenfeld ab, das der Shop als Zahl sendet und die Bestellung
nur als Text annimmt (jetzt ein Textfeld mit Muster), und der Systemprozess startete die
Lizenz-Position mit Variante nicht (siehe Voraussetzung 0).

Die Genehmigung durch die vorgesetzte Person prüft `go test ./api -run TestTheM365Approval`
gegen einen echten Server: Bestellung eines Teams und eines Postfachzugriffs mit den
Bestellformularen dieses Pakets, der Job «Vorgesetzten lesen» erhält die Antworten im
Klartext, die Aufgabe geht an das Konto der vorgesetzten Person bzw. an
`M365-Administration`, die Zustimmung startet die Bereitstellung mit `approvedBy`, die
Ablehnung erreicht den Auftrag mit der Begründung der genehmigenden Person; eine Person,
die Entra ID nicht kennt (`ENTRA_USER_NOT_FOUND`), geht mit diesem Grund an die
Ersatzgruppe und nicht in einen Incident. Den Entra-Worker spielt der Test selbst: Er
übernimmt den Job und antwortet mit dem Vorgesetzten, ohne Vorgesetzten oder mit dem
Fehler.

Nicht geprüft: die Entra- und Exchange-Schritte gegen einen echten Tenant (für diese
Worker gibt es keinen Mock), `get-user-manager` gegen Microsoft Graph,
die Mail an die genehmigende Person, die Eskalation nach sieben Tagen, die
Governance-Prüfung gegen echte Daten und `installieren.ps1` (in der Prüfumgebung stand keine
PowerShell zur Verfügung).

## Annahmen, die vor dem Einsatz zu prüfen sind

- **SKU-IDs** in `m365-lizenzprofil.dmn` stammen aus Microsofts Liste der Lizenz-IDs. Ein
  Tenant mit den Varianten ohne Teams hat andere; massgebend ist
  `GET https://graph.microsoft.com/v1.0/subscribedSkus`.
- **Sprachen und Zeitzone** in `m365-telefonie.dmn` müssen in den Listen von
  `Get-CsAutoAttendantSupportedLanguage` und `Get-CsAutoAttendantSupportedTimeZone`
  stehen.
- **OPATH-Filter** der Governance-Prüfung (`-not(CustomAttribute1 -eq 'atlas-katalog')`)
  vor dem ersten Lauf mit `Get-Mailbox -Filter` im eigenen Tenant prüfen.
- **Regeln der Zusatzfreigabe** in `m365-postfachzugriff.dmn` sind ein Vorschlag;
  massgebend ist die Weisung der eigenen Organisation.
- **Verteilerlisten** in `m365-verteiler-zuordnung.dmn` sind Beispiele.
- **Nummernart.** Die drei Telefonie-Prozesse weisen Direct-Routing-Nummern zu
  (`numberType="direct-routing"` an der Service-Task «Rufnummer zuweisen»). Ein Tenant mit
  Calling Plan oder Operator Connect ändert das Attribut dort. Der Worker lehnt dann eine
  Nummer, die der Tenant nicht besitzt, mit `TEAMSPHONE_NUMBER_NOT_FOUND` ab; dafür haben
  die Modelle kein Grenzereignis, die Ablehnung wird also zum Incident.
- **Lizenz vor Nummer.** Teams nimmt eine Rufnummer nur für eine Person mit
  Teams-Phone-Lizenz und für ein Ressourcenkonto mit der Lizenz *Microsoft Teams Phone
  Resource Account* an, und Teams kennt eine eben zugewiesene Lizenz unter Umständen erst
  nach einer Weile. Die Aufgabe «Rufnummer wählen» liegt dazwischen; scheitert die
  Zuweisung trotzdem, wiederholt der Worker sie wie jede Störung. Der Tenant muss Lizenzen
  dieser Art frei haben (`subscribedSkus`). An einem Tenant ist das nicht geprüft.

## Bekannte Grenzen

- **Teams und Ressourcenkonten werden über ihren Namen wiedergefunden.** Ändert sich ein
  Präfix der Namensregel, findet die Rückgabe bestehender Objekte dieses Typs sie nicht
  mehr. Präfixe sind deshalb wie Identitäten zu behandeln. Exchange-Objekte sind davon
  nicht betroffen.
- **Eine Ablehnung der Informationssicherheit** wird über
  `POST /api/v1/orders/{id}/lines/{itemId}/decision` gemeldet; diese Route adressiert die
  Position über das Produkt. Bestellt jemand im selben Auftrag zwei Postfachzugriffe, trifft
  die Ablehnung die erste Position dieses Produkts.
- **Öffentliche Verteiler-Anmeldung ohne Double-Opt-in.** Ein öffentliches Formular kann
  jede Adresse eintragen. Eine Bestätigung durch die Adresse selbst braucht einen
  Mail-Worker und ist nicht modelliert; Listen mit Prüfpflicht sind davon ausgenommen.
- **Abfangbare Teams-Phone-Ablehnungen.** Die Grenzereignisse für `TEAMSPHONE_UPN_TAKEN`,
  `TEAMSPHONE_TOO_MANY_AGENTS` und `TEAMSPHONE_NUMBER_IN_USE` brauchen einen
  Teams-Phone-Worker, der diese Ablehnungen als BPMN-Fehler meldet (CHANGELOG: «A Teams
  Phone refusal can take an error boundary's path»); ein älterer Worker führt dieselbe
  Ablehnung als Incident.
- **Eine Rückgabe oder Aktion erhält `atlasApiBase` nicht**, nur die Bereitstellung. Die
  Prozesse fallen deshalb auf die Applikationskonstante `ATLAS_API_BASE` zurück, die auf
  jeder Instanz zu setzen ist, deren Server nicht unter `http://127.0.0.1:8080` erreichbar
  ist.
- **Output-Mappings auf Business-Rule-Tasks** wertet Atlas nicht aus. Die Modelle lesen das
  Ergebnis einer Entscheidung deshalb direkt (`regel.alias`) und bilden Einzelwerte, die ein
  Formular anzeigen soll, mit einer Script-Task.

## Was die Tests prüfen

`go test ./examples` prüft ohne Server: alle Prozesse kompilieren, das Katalogdokument
veröffentlicht sich, Lebenszyklus-Bindungen und Shop-Aufgaben passen zu den Prozessen,
jedes Formular ist vorhanden, jedes Bestellformular liefert nur Text (keine Zahlenfelder,
Checkboxen oder Mehrfachauswahlen), jede Antwort eines Bestellformulars ist als Personendatum
deklariert oder als nicht personenbezogen markiert, jeder Platzhalter wird gefragt, und
`atlas.json` nennt genau die Prozesse und Formulare dieses Verzeichnisses.
`go test ./api/layout` hält alle Diagramme an die Layout-Regeln (keine Überlappungen,
keine Kreuzungen, Beschriftungen frei).

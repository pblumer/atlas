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
| `atlas.json` | Manifest der Applikation «Beispiel: Microsoft-365-Dienste» (Schlüssel `beispiel-m365-dienste`): 14 Prozesse, 23 Formulare, 5 Entscheidungsreferenzen |
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
| Microsoft 365: Lizenz mit Postfach (`m365-lizenz`), Varianten E3, E5, F3 | Entra, Exchange | vorgesetzte Person | `lizenz.bpmn` (Lebenszyklus) | Lizenz zurückgeben, Abwesenheitsnotiz setzen, Abwesenheitsnotiz beenden |
| Teams-Telefonie mit eigener Rufnummer (`m365-telefonie`), setzt die Lizenz voraus | Entra, manuell | `{{telefonie-team}}` | `telefonie.bpmn` (Lebenszyklus) | Telefonie aufheben |
| Microsoft Teams: Team (`m365-team`) | Entra | vorgesetzte Person | `team.bpmn` (Lebenszyklus) | Team archivieren, Person aufnehmen, Kanal anlegen |
| Funktionspostfach (`m365-funktionspostfach`) | Exchange | vorgesetzte Person | `funktionspostfach.bpmn` (Lebenszyklus) | entfernen, Zugriff erteilen, Zugriff entziehen |
| Verteilerliste (`m365-verteiler`) | Exchange | keine | `verteiler.bpmn` (Lebenszyklus) | entfernen, Mitglied aufnehmen, Mitglied entfernen |
| Zugriff auf ein Postfach (`m365-postfachzugriff`), 365 Tage befristet | Exchange | vorgesetzte Person, je nach Regel zusätzlich Informationssicherheit | `postfachzugriff-erteilen.bpmn`, `postfachzugriff-entziehen.bpmn` (getrennte Prozesse) | Rückgabe |
| Sitzungszimmer (`m365-raum`) | Exchange | `{{facility-management}}` | `raum-einrichten.bpmn`, `raum-entfernen.bpmn` (getrennte Prozesse) | Rückgabe |
| Anrufwarteschlange (`m365-warteschlange`) | Teams Phone, manuell | `{{telefonie-team}}` | `warteschlange.bpmn` (Lebenszyklus) | entfernen, Agenten ändern |
| Telefonzentrale (`m365-telefonzentrale`) | Teams Phone, Entra, manuell | `{{telefonie-team}}` | `telefonzentrale.bpmn` (Lebenszyklus) | entfernen |

«Manuell» bezeichnet die Rufnummer: Der Teams-Phone-Worker verwaltet Warteschlangen,
Telefonzentralen und ihre Ressourcenkonten, aber keine Rufnummern. Die Zuweisung ist
deshalb eine Aufgabe für die Gruppe `M365-Telefonie`, die das Cmdlet nennt. Erhält der
Worker eine solche Operation, ersetzt eine Service-Task genau diese Aufgabe.

## Die Entscheidungen

| Modell (Handle) | Entscheidung | Wofür |
|---|---|---|
| `m365-namensregel` | `M365Namensregel` | Namenskonvention: Präfix je Objekttyp (TM, FP, VL, RM, CQ, AA), Sperrliste reservierter Bezeichnungen, Längengrenze, Begründung jeder Ablehnung |
| `m365-lizenzprofil` | `M365Lizenzprofil` | Variante → SKU-ID der Lizenz |
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
   Prozesse ihre Positionen an die Bestellung.
3. **Verzeichnis-Worker `verzeichnis`** für die eingebaute Genehmigung durch die
   vorgesetzte Person (Lizenz, Team, Funktionspostfach, Postfachzugriff).
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
Rufnummernaufgabe bis `done`, die Aktion «Agenten ändern» (`completed`), die Rückgabe
(`returned`), die Ablehnung eines Verteilers mit reservierter Bezeichnung (`rejected` mit
Begründung), beide öffentlichen Formulare ohne Anmeldung bis zum jeweiligen Entra- bzw.
Exchange-Schritt, die Verschlüsselung der Personendaten und der Postfachzugriff mit
Zusatzfreigabe.

Ebenso geprüft: `installieren.sh` gegen eine frische Instanz mit Anmeldung (Schritte 1–3
mit einem Admin-Token, Schritt 4 abgelehnt und danach über eine angemeldete Sitzung
importiert, beide Kataloge veröffentlicht).

Auf atlas.blumer.cloud über die Shop-Oberfläche bestellt; dabei fielen zwei Fehler auf,
die die Bestellungen über die API nicht zeigten: Das Bestellformular des Sitzungszimmers
fragte die Plätze mit einem Zahlenfeld ab, das der Shop als Zahl sendet und die Bestellung
nur als Text annimmt (jetzt ein Textfeld mit Muster), und der Systemprozess startete die
Lizenz-Position mit Variante nicht (siehe Voraussetzung 0).

Nicht geprüft: die Entra- und Exchange-Schritte gegen einen echten Tenant (für diese
Worker gibt es keinen Mock), die Governance-Prüfung gegen echte Daten und
`installieren.ps1` (in der Prüfumgebung stand keine PowerShell zur Verfügung).

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
- **Abfangbare Teams-Phone-Ablehnungen.** Die Grenzereignisse für `TEAMSPHONE_UPN_TAKEN`
  und `TEAMSPHONE_TOO_MANY_AGENTS` greifen erst, wenn der Teams-Phone-Worker diese
  Ablehnungen als BPMN-Fehler meldet; bis dahin führt dieselbe Ablehnung zu einem
  Incident.
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

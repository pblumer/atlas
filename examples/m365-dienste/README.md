# Microsoft-365-Dienste – ein Shop mit Governance

Ein Katalog aus der Sicht einer IT-Abteilung, die ihre Microsoft-365-Dienste über Atlas
anbietet: Was eine Person, ein Team oder eine Funktion bestellen kann, wird von den
Worker-Typen **Entra**, **Exchange**, **Teams Phone** und **SharePoint** bereitgestellt,
geändert und wieder entzogen. Das Paket enthält die Kataloge, die Produkte, alle Prozesse, die
Entscheidungstabellen, die Formulare (darunter zwei öffentliche) und eine wöchentliche
Governance-Prüfung, zweisprachig (Deutsch und Englisch).

Die Worker heissen `ucc_entra`, `ucc_exchange`, `ucc_teams` und `ucc_sharepoint`. Wer andere
Namen verwendet, ersetzt sie in den `.bpmn`-Dateien (Attribut `connector`) und im
Bestellformular der SharePoint-Teamwebsite (Block `atlasAvailability`).

## Inhalt

| Datei | Inhalt |
|---|---|
| `atlas.json` | Manifest der Applikation «Beispiel: Microsoft-365-Dienste» (Schlüssel `beispiel-m365-dienste`): 16 Prozesse, 28 Formulare, 6 Entscheidungen samt ihren Modellen (Format 2) |
| `katalog.json` | Das Katalogdokument: zwei Kataloge, zehn Produkte, eine Beziehung |
| `fragen.json` | Was jeder Platzhalter im Katalogdokument fragt (Zielgruppen, Genehmigende) |
| `*.bpmn` | Die Prozesse, je mit Dokumentation an Prozess und Elementen |
| `*.dmn` | Die sechs Entscheidungsmodelle |
| `*.form.json` | Bestell-, Aktions-, Aufgaben- und öffentliche Formulare |
| `installieren.sh`, `installieren.ps1` | Installation in eine Atlas-Instanz: Entscheidungsmodelle, Applikation, Veröffentlichung, Katalog |
| `antworten.beispiel.json` | Vorlage für die Antworten auf `fragen.json` |

## Die Kataloge

| Katalog | Rang | Zielgruppe | Produkte |
|---|---|---|---|
| Microsoft 365 für Mitarbeitende (`cat-m365-mitarbeitende`) | 500 | `{{zielgruppe}}` | Lizenz, Telefonie, Team, SharePoint-Teamwebsite, Funktionspostfach, Verteiler, Postfachzugriff |
| Microsoft 365: Kommunikation und Telefonie (`cat-m365-ucc`) | 510 | `{{zielgruppe-ucc}}` | alle zehn |

Eine Person sieht im Shop genau einen Katalog: den mit dem höchsten Rang unter denen, die
ihre Gruppen erreichen. Der UCC-Katalog bietet deshalb auch die allgemeinen Produkte an.
Die Ränge müssen auf der Zielinstanz frei sein; sonst sind sie in `katalog.json` anzupassen.

## Die Produkte

| Produkt | Worker | Genehmigung | Prozess | Aktionen nach der Bereitstellung |
|---|---|---|---|---|
| Microsoft 365: Lizenz mit Postfach (`m365-lizenz`), Varianten E3, E5, F3 | Entra, Exchange | vorgesetzte Person der betroffenen Person | `lizenz.bpmn` (Lebenszyklus) | Lizenz zurückgeben, Abwesenheitsnotiz setzen, Abwesenheitsnotiz beenden |
| Teams-Telefonie mit eigener Rufnummer (`m365-telefonie`), setzt die Lizenz voraus | Entra, Teams Phone | `{{telefonie-team}}` | `telefonie.bpmn` (Lebenszyklus) | Telefonie aufheben |
| Microsoft Teams: Team (`m365-team`) | Entra | vorgesetzte Person der verantwortlichen Person | `team.bpmn` (Lebenszyklus) | Team archivieren, Person aufnehmen, Kanal anlegen |
| SharePoint: Teamwebsite (`m365-sharepoint-site`) | Entra, SharePoint | vorgesetzte Person der Besitzerin oder des Besitzers | `sharepoint-site.bpmn` (Lebenszyklus) | Site endgültig zurückgeben, Externe Person einladen, Site-Besitzer ändern, Speicherkontingent erhöhen, Site löschen, Gelöschte Site wiederherstellen |
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

## Die SharePoint-Teamwebsite

Das Produkt `m365-sharepoint-site` bietet, was ein Self-Service-Portal für SharePoint mit den
Kacheln *Invite*, *Site Quota*, *Site Owners*, *Delete* und *Undelete* und einem Formular
*Create a new Team Site* anbietet. Eine moderne, gruppenverbundene Teamwebsite ist eine
Microsoft-365-Gruppe (`groupTypes` Unified, ohne Team), deren Alias (`mailNickname`) die URL
der Site ist: `https://<SHAREPOINT_HOST>/sites/<alias>`. Microsoft legt die Site zur Gruppe
selbst an; der Prozess legt deshalb die Gruppe an und wartet auf die Site.

**Bestellformular** (`m365-sharepoint-site-bestellung`): Titel, URL, Sprache, anfängliches
Kontingent (1, 5, 10 oder 25 GB), Zweck, Sichtbarkeit, Besitzer und Stellvertretung (UPN). Das
Feld URL prüft beim Tippen, ob die URL noch frei ist (ADR-draft-name-availability-check): bei
den Gruppen in Entra ID, bei den gelöschten Gruppen, die ihren Alias 30 Tage lang behalten,
und bei SharePoint selbst unter `https://contoso.sharepoint.com/sites/<url>`. Der Wert wird
kleingeschrieben geprüft; erlaubt sind 3 bis 64 Zeichen aus Kleinbuchstaben, Ziffern, `-` und
`_` (`^[a-z0-9][a-z0-9_-]{2,63}$`, dasselbe Muster im Formular und in der Prüfung; einen Punkt
erlaubt SharePoint in Site-Namen nicht). **Diese Prüfung ist eine Gefälligkeit**: Zwei Personen
können im selben Augenblick «frei» lesen. Verbindlich prüft die Bereitstellung.

**Bereitstellung** (`m365.site.erstellen`, `sharepoint-site.bpmn`):

1. Namensregel `M365Namensregel` mit dem Objekttyp `site`: Form der URL, reservierte Namen
   (admin, postmaster, security …), höchstens 64 Zeichen. Alias ist die URL, Anzeigename der
   Titel. Eine Ablehnung geht mit Begründung über die Entscheidungsroute an den Auftrag.
2. `get-user` für Besitzer und Stellvertretung, bevor etwas angelegt wird.
3. Verbindliche Prüfung: `list-groups` und `list-deleted-groups` mit
   `mailNickname eq '<alias>'` und `get-site` auf `<SHAREPOINT_HOST>:/sites/<alias>`. Ein
   Treffer irgendwo lehnt die Position mit «Die URL ist bereits vergeben» ab.
4. `create-group` (Unified, `mailEnabled` true, `securityEnabled` false, Sichtbarkeit aus dem
   Formular, Beschreibung mit Zweck und Katalog-Position, `preferredLanguage` aus der Sprache),
   dann `add-group-owner` für beide Personen.
5. Warten auf die Site: höchstens zehnmal im Abstand von zwei Minuten `get-site`. Erscheint sie
   nicht, erhält `M365-SharePoint` die Aufgabe «Site-Erstellung prüfen».
6. Ein Eintrag in der Liste «Site-Inventar» (siehe Voraussetzungen), dann `done`.
7. Aufgabe «Speicherkontingent setzen» für `M365-SharePoint`: **Microsoft Graph hat keine
   Schnittstelle für das Speicherkontingent einer Site.** Gesetzt wird es im SharePoint Admin
   Center (oder mit `Set-SPOSite -StorageQuota`); die Aufgabe sagt es so.

**Aktionen:**

| Aktion | Formular | Ablauf | Ergebnis |
|---|---|---|---|
| Site endgültig zurückgeben (`deprovision`) | – | Gruppe suchen, `delete-group`, Inventar auf Returned | `returned` |
| Externe Person einladen (`gast`) | `m365-sharepoint-gast` | `get-site` für die Adresse, `invite-guest` (Einladung führt auf die Site, Microsoft verschickt sie), `add-group-member` mit dem eingeladenen Konto | `completed` |
| Site-Besitzer ändern (`besitzer`) | `m365-sharepoint-besitzer` | neuen Besitzer eintragen, auf Wunsch danach den bisherigen austragen | `completed` |
| Speicherkontingent erhöhen (`kontingent`) | `m365-sharepoint-kontingent` | `get-site` liest die Belegung, `M365SiteKontingent` entscheidet, `M365-SharePoint` setzt das Kontingent (und prüft über 25 GB den Antrag), Inventar nachführen | `completed`, bei Ablehnung `rejected` |
| Site löschen (`loeschen`) | – | `delete-group`, Inventar auf Removed; das Recht bleibt | `completed` |
| Gelöschte Site wiederherstellen (`wiederherstellen`) | – | `list-deleted-groups`, `restore-group`, Inventar auf Active | `completed`, wenn nichts mehr im Papierkorb liegt `rejected` |

«Site löschen» und «Site endgültig zurückgeben» löschen beide die Gruppe; Microsoft löscht mit
ihr die Site, und beides bleibt 30 Tage im Papierkorb wiederherstellbar. Nach «Site löschen»
bleibt das Recht im Bestand, damit «Gelöschte Site wiederherstellen» möglich bleibt; nach der
Rückgabe endet es, und eine Wiederherstellung ist nur noch Sache der Administration.

Eine Ablehnung einer Aktion (`rejected`, ADR-0429) trägt keinen Text; der Grund steht in der
Instanz (`stufe.grund`, `pruefkommentar`). Die Gateways lesen keine Personendaten: Ob der
bisherige Besitzer gehen soll, sagt das nicht personenbezogene Feld `bisherigenEntfernen`.

## Die Genehmigung durch die vorgesetzte Person

Lizenz, Team, SharePoint-Teamwebsite, Funktionspostfach und Postfachzugriff nennen als Genehmigungsart den Prozess
`proc_m365_genehmigung_vorgesetzter` (`genehmigung-vorgesetzter.bpmn`) und nicht die
eingebaute Art «superior». Der Grund: Die eingebaute Art fragt die vorgesetzte Person der
**bestellenden** Person und kennt dafür nur deren Atlas-Konto. Bestellt wird hier aber für
jemanden, den das Bestellformular nennt: `upn` (Lizenz), `besitzer` (Team,
SharePoint-Teamwebsite, Funktionspostfach) oder `person` (Postfachzugriff). Diese Antworten erhält nur ein
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

**Personendaten.** `upn`, `besitzer`, `stellvertretung`, `person`, `postfach` und `anlass` sind im
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
| `m365-namensregel` | `M365Namensregel` | Namenskonvention: Präfix je Objekttyp (TM, FP, VL, RM, CQ, AA), Sperrliste reservierter Bezeichnungen, Längengrenze, Begründung jeder Ablehnung; für SharePoint-Sites (Objekttyp `site`) die Form der URL statt eines Präfixes |
| `m365-site-kontingent` | `M365SiteKontingent` | gewünschtes Speicherkontingent einer Site und ihre Belegung → erlaubt ja/nein, Prüfung durch `M365-SharePoint` ja/nein (bis 25 GB ohne, bis 250 GB mit Prüfung, darüber und unter der Belegung abgelehnt) |
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
  (ausgenommen die gekennzeichneten Gruppen der SharePoint-Teamwebsites) und legt den Befund `M365-Administration` vor. Der Befund entzieht nichts; gehandelt wird
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
   - Für die SharePoint-Teamwebsite zusätzlich: am Worker `ucc_entra` `User.Invite.All`
     (Gäste einladen; `Group.ReadWrite.All` aus der ersten Zeile deckt Anlegen, Löschen und
     Wiederherstellen der Gruppe und das Lesen gelöschter Gruppen ab), und ein Worker
     `ucc_sharepoint` (Microsoft SharePoint) mit `Sites.ReadWrite.All` oder mit
     `Sites.Selected` und Schreibrecht auf der Inventar-Site, dazu `Sites.Read.All` für
     `get-site` auf beliebigen Sites (Existenz und Belegung).
   - **Live-Prüfung der URL** im Bestellformular: Bei beiden Workern (`ucc_entra` und
     `ucc_sharepoint`) in der Console den Schalter «formLookups» (Formular-Abfragen erlauben)
     einschalten. Der SharePoint-Worker muss dafür als Worker Instance laufen (beaufsichtigt
     oder extern), nicht im Server-Prozess. Fehlt eines davon, kann das Formular nur nicht
     vorwarnen; bestellt werden kann trotzdem, und die Bereitstellung prüft wie immer.
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
   `M365-SharePoint`, `Informationssicherheit` und die Gruppe des Facility Managements. Die
   Aufgaben der Prozesse adressieren `M365-Telefonie`, `M365-Administration`,
   `M365-SharePoint` und `Informationssicherheit` über ihren Namen.
5. **Applikationskonstanten** (Console → Applikation → Constants), nach dem Import:

   | Konstante | Fallback | Bedeutung |
   |---|---|---|
   | `MAIL_DOMAIN` | `contoso.ch` | Domain der Adressen von Funktionspostfächern, Verteilern, Räumen und Ressourcenkonten |
   | `USAGE_LOCATION` | `CH` | Nutzungsort, ohne den Graph keine Lizenz zuweist |
   | `PARTNER_UPN_DOMAIN` | `contoso.onmicrosoft.com` | Domain der Partnerkonten |
   | `ATLAS_API_BASE` | `http://127.0.0.1:8080` | Adresse, unter der die Prozesse Atlas selbst erreichen, wenn eine Rückgabe oder Aktion sie nicht mitbringt |
   | `SHAREPOINT_HOST` | `contoso.sharepoint.com` | SharePoint-Host des Tenants; die Sites liegen unter `https://<SHAREPOINT_HOST>/sites/<alias>` |
   | `SITE_INVENTAR` | `contoso.sharepoint.com:/sites/m365-governance` | Site, die die Liste «Site-Inventar» trägt (Host und Pfad, eine Site-ID oder die URL) |
6. **Die Liste «Site-Inventar»** auf der Site aus `SITE_INVENTAR`, von Hand angelegt, mit den
   Spalten `Title` (vorhanden), `Alias`, `SiteUrl`, `GroupId`, `Sprache` und `Position`
   (je eine Textzeile), `KontingentGB` (Zahl) und `Status` (Text oder Auswahl mit Active,
   Returned, Removed). Die Liste führt keine Personendaten; die Besitzer stehen in Entra ID.
   Ab 5000 Einträgen braucht die Spalte `Alias` einen Index.
7. **Der Host im Bestellformular.** Das Feld URL in `m365-sharepoint-site-bestellung.form.json`
   nennt in seinem Block `atlasAvailability` den Host `contoso.sharepoint.com`. Er ist bei der
   Installation auf den eigenen Tenant anzupassen (derselbe Wert wie `SHAREPOINT_HOST`); die
   Live-Prüfung liest ihn aus dem Formular und nicht aus der Konstante.

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

1. lädt die sechs Entscheidungsmodelle unter ihren Handles hoch
   (`POST /api/v1/dmn-models?handle=…`). Der Quellbaum trägt die Modelle ebenfalls
   (`atlas.json` im Format 2), sodass `atlas import` und atlas hub ohne diesen Schritt
   auskommen; das Skript behält ihn, und der Import findet die Modelle dann unverändert vor;
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

Ganz ohne Skript geht es auch in der Console: die sechs `.dmn`-Dateien im Modeler unter den
Handles `m365-lizenzprofil`, `m365-namensregel`, `m365-postfachzugriff`,
`m365-site-kontingent`, `m365-telefonie` und `m365-verteiler-zuordnung` hochladen, die Applikation über **Import source** (ein
`.tar.gz` mit `atlas.json` und den genannten Dateien) importieren und veröffentlichen,
dann `katalog.json` mit den vier Gruppen-IDs anstelle der Platzhalter unter
**Catalogue → Import** hochladen.

Bezieht die Zielinstanz ihre Entscheidungen aus einem temis-Dienst
(`ATLAS_DMN_RESOLVER_URL`), werden die sechs Modelle dort unter denselben Namen angelegt,
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

**SharePoint-Teamwebsite.** Geprüft ist nur ohne Server und ohne Tenant: Der Prozess
kompiliert, das Katalogdokument veröffentlicht sich mit dem Produkt und allen sieben
Aktionen, der Block `atlasAvailability` des Bestellformulars wird vom Parser des Servers
angenommen, das Diagramm hält die Layout-Regeln ein, und beide Entscheidungen
(`M365Namensregel` mit dem Objekttyp `site` und die bisherigen Objekttypen unverändert,
`M365SiteKontingent` mit allen Regeln) wurden lokal ausgewertet. **Nichts davon lief gegen
einen echten Tenant**, und keine Bestellung lief durch den Shop: weder `create-group`,
`invite-guest`, `restore-group` und `list-deleted-groups` gegen Microsoft Graph noch
`get-site` und die Inventarliste gegen SharePoint, noch die Live-Prüfung im Formular.

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
- **Kein Speicherkontingent über eine Schnittstelle.** Microsoft Graph kennt keinen Aufruf,
  der das Speicherkontingent einer Site setzt; das geht nur im SharePoint Admin Center oder mit
  PowerShell (`Set-SPOSite -StorageQuota`). Beide Kontingent-Schritte sind deshalb Aufgaben
  für `M365-SharePoint`, und ob ein gesetztes Kontingent stimmt, prüft niemand nach. Was
  `get-site` als Belegung liest, ist das Kontingent der Standardbibliothek; dass das dem
  Kontingent der Site im Admin Center entspricht, ist Microsofts Dokumentation und nicht an
  einem Tenant geprüft.
- **Kein «Subsite Quota».** Das Portal, an dem sich das Produkt orientiert, hat eine Kachel für
  das Kontingent von Unterwebsites. SharePoint Online kennt kein eigenes Kontingent für
  Unterwebsites (sie teilen das der Site), moderne Sites raten von Unterwebsites ab, und es
  gibt keine Schnittstelle dafür. Das Produkt bildet die Kachel deshalb nicht nach.
- **Eine gelöschte Site ohne Gruppe wird nicht erkannt.** Die verbindliche Prüfung sieht
  bestehende und gelöschte Gruppen und bestehende Sites. Eine gelöschte klassische Site oder
  Kommunikationssite im Papierkorb von SharePoint belegt ihre URL ebenfalls, ist für Graph aber
  nicht sichtbar; `create-group` legt dann die Gruppe an, und die Site entsteht nicht unter der
  bestellten Adresse (SharePoint wählt dann nach unserem Verständnis eine abgewandelte URL;
  nicht geprüft). Der Prozess wartet in diesem Fall vergeblich auf `/sites/<alias>`, und die
  Aufgabe «Site-Erstellung prüfen» geht an `M365-SharePoint`.
- **Die Live-Prüfung ist eine Gefälligkeit.** Sie warnt nur; die Bereitstellung prüft erneut
  und lehnt ab, wenn die URL inzwischen vergeben ist.
- **Der Filter auf gelöschte Gruppen** (`mailNickname eq '…'` auf
  `/directory/deletedItems/microsoft.graph.group`) ist an keinem Tenant geprüft.
- **Sprache der Site.** `preferredLanguage` der Gruppe kommt aus dem Formular (`de-CH`,
  `en-US`, `fr-CH`, `it-CH`); ob SharePoint daraus die Sprache der Site ableitet, ist nicht
  geprüft. Die Sprache einer Site lässt sich nach ihrer Erstellung nicht mehr ändern; wer sie
  verbindlich braucht, prüft das vor dem Einsatz an einem Test-Tenant.
- **Sites in der Governance-Prüfung.** Der Alias einer Site ist ihre frei gewählte URL und
  folgt keinem Präfix. Die Gruppe einer Site, die dieser Katalog angelegt hat, trägt deshalb in
  ihrer Beschreibung «| SharePoint-Site | Katalog-Position …», und die Governance-Prüfung zählt
  so gekennzeichnete Gruppen nicht als Abweichung. Die Kennzeichnung steht in einem Feld, das
  Besitzerinnen und Besitzer ändern können; sie ist ein Hinweis, kein Nachweis. Massgebend
  bleibt das Site-Inventar.
- **Gäste** lassen sich nur einladen, wenn die Einstellungen des Tenants für externe
  Zusammenarbeit (Entra ID) und für das Teilen (SharePoint) es erlauben; sonst endet die Aktion
  mit einem Incident.

## Was die Tests prüfen

`go test ./examples` prüft ohne Server: alle Prozesse kompilieren, das Katalogdokument
veröffentlicht sich, Lebenszyklus-Bindungen und Shop-Aufgaben passen zu den Prozessen,
jedes Formular ist vorhanden, jedes Bestellformular liefert nur Text (keine Zahlenfelder,
Checkboxen oder Mehrfachauswahlen), jede Antwort eines Bestellformulars ist als Personendatum
deklariert oder als nicht personenbezogen markiert, jeder Platzhalter wird gefragt, und
`atlas.json` nennt genau die Prozesse und Formulare dieses Verzeichnisses.
`go test ./api/layout` hält alle Diagramme an die Layout-Regeln (keine Überlappungen,
keine Kreuzungen, Beschriftungen frei).

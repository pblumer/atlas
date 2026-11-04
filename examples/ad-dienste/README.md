# Active-Directory-Dienste – Gruppen, Berechtigungen und Konten als Shop

Ein Katalog aus der Sicht einer Informatik, die ihr Active Directory über Atlas anbietet:
Sicherheitsgruppen mit einer verantwortlichen Person, Berechtigungen auf Fileshares und
Applikationen als Gruppenmitgliedschaft, persönliche Admin-Konten, Servicekonten, Testkonten,
Konten für externe Personen und die Versetzung eines Kontos in eine andere
Organisationseinheit. Alles läuft über den Worker-Typ **AD** (ADR-0166); das Paket enthält den
Katalog, die Produkte, die Prozesse, drei Entscheidungstabellen und die Formulare, zweisprachig
(Deutsch und Englisch).

Der Worker heisst `ad_verzeichnis`. Wer einen anderen Namen verwendet, ersetzt ihn in den
`.bpmn`-Dateien (Attribut `connector` der `atlas:adConnector`-Elemente).

## Inhalt

| Datei | Inhalt |
|---|---|
| `atlas.json` | Manifest der Applikation «Beispiel: Active-Directory-Dienste» (Schlüssel `beispiel-ad-dienste`): 8 Prozesse, 11 Formulare, 3 Entscheidungen samt ihren Modellen (Format 2) |
| `katalog.json` | Das Katalogdokument: ein Katalog, neun Produkte, eine Beziehung (`excludes`) |
| `fragen.json` | Was jeder Platzhalter im Katalogdokument fragt (Zielgruppe, Genehmigende) |
| `antworten.beispiel.json` | Vorlage für die Antworten auf `fragen.json` |
| `*.bpmn` | Die Prozesse, je mit Dokumentation an Prozess und Elementen |
| `*.dmn` | Die drei Entscheidungsmodelle |
| `*.form.json` | Bestell-, Aktions- und Aufgabenformulare |
| `installieren.sh`, `installieren.ps1` | Installation in eine Atlas-Instanz: Entscheidungsmodelle, Applikation, Veröffentlichung, Katalog |

## Der Katalog

| Katalog | Rang | Zielgruppe | Produkte |
|---|---|---|---|
| Active Directory (`cat-ad-dienste`) | 600 | `{{zielgruppe-ad}}` | alle neun |

Eine Person sieht im Shop genau einen Katalog: den mit dem höchsten Rang unter denen, die ihre
Gruppen erreichen. Wer neben diesem Paket die Microsoft-365-Dienste (Ränge 500 bis 520)
installiert, sieht deshalb mit einer Gruppe, die beide Zielgruppen erreicht, nur den Katalog
«Active Directory». Die Ränge müssen auf der Zielinstanz frei sein; sonst sind sie in
`katalog.json` anzupassen.

## Die Produkte

| Produkt | Genehmigung | Prozess | Befristung | Aktionen nach der Bereitstellung |
|---|---|---|---|---|
| Sicherheitsgruppe (`ad-sicherheitsgruppe`) | vorgesetzte Person der bestellenden Person (`superior`) | `ad-sicherheitsgruppe.bpmn` | – | Gruppe löschen, Mitglied aufnehmen, Mitglied entfernen, Verantwortung übergeben |
| Fileshare Finanzen: lesen (`ad-recht-fileshare-finanzen-lesen`) | verantwortliche Person der Gruppe (`proc_ad_genehmigung_gruppenverantwortung`) | `ad-berechtigung.bpmn` | 365 Tage | Berechtigung zurückgeben |
| Fileshare Finanzen: ändern (`ad-recht-fileshare-finanzen-aendern`), schliesst «lesen» aus | wie oben | `ad-berechtigung.bpmn` | 365 Tage | Berechtigung zurückgeben |
| Applikation CRM (`ad-recht-applikation-crm`) | wie oben | `ad-berechtigung.bpmn` | 365 Tage | Berechtigung zurückgeben |
| Admin-Konto (`ad-admin-konto`) | `{{informationssicherheit}}` | `ad-admin-konto.bpmn` | 365 Tage | Admin-Konto zurückgeben (auch durch den Austrittsprozess der Microsoft-365-Dienste), Konto entsperren, Passwort zurücksetzen |
| Servicekonto (`ad-servicekonto`) | vorgesetzte Person der bestellenden Person | `ad-servicekonto.bpmn` | 365 Tage | Servicekonto zurückgeben, Konto entsperren, Passwort zurücksetzen |
| Testkonto (`ad-testkonto`) | keine | `ad-testkonto.bpmn` | 90 Tage, gibt sich am Ablaufdatum selbst zurück | Testkonto zurückgeben, Konto entsperren, Passwort zurücksetzen |
| Konto für eine externe Person (`ad-externes-konto`) | vorgesetzte Person der bestellenden Person (des Sponsors) | `ad-externes-konto.bpmn` | 180 Tage | Konto zurückgeben, Konto entsperren, Passwort zurücksetzen |
| Versetzung (`ad-versetzung`) | `{{ad-administration}}` | `ad-versetzung.bpmn` | – (gibt sich nach der Ausführung selbst zurück) | Versetzung abschliessen (der Prozess selbst und der Betrieb) |

Jedes Produkt hat **einen Lebenszyklus-Prozess** (ADR-0425): eine Nachricht je Aktion startet
eine eigene Instanz. Bereitstellung und Rückgabe melden ihr Ergebnis über die Positionsroute
(`done`, `returned`, `returnFailed`) oder lehnen mit Begründung ab (`…/decision`); eine Änderung
oder ein Service antwortet mit einer Shop-Aufgabe im Modus `outcome` (`completed` oder
`rejected`, ADR-0429). Alle Aufrufe an Atlas tragen das Secret `atlas`.

Warum die Genehmigungen so verteilt sind: Eine **Gruppe** ist eine gemeinsame Ressource, für die
die bestellende Person eine verantwortliche Person benennt; ihre vorgesetzte Person bestätigt den
Bedarf. Über den **Zugang zu einer Gruppe** entscheidet, wer sie verantwortet, nicht die
vorgesetzte Person der empfangenden. Ein **Admin-Konto** ist ein privilegierter Zugang und geht
deshalb an die Informationssicherheit. Ein **Testkonto** ist kurzlebig, trägt keine Rechte und
endet von selbst; es braucht keine Genehmigung. Eine **Versetzung** ändert Gruppen und Ort eines
fremden Kontos und wird deshalb von der AD-Administration geprüft.

### Sicherheitsgruppe

**Bestellformular** (`ad-sicherheitsgruppe-bestellung`): Bereichskürzel, Bezeichnung,
Gruppenbereich (global, domänenlokal, universell), Zweck und die verantwortliche Person (ihr
Anmeldename).

**Bereitstellen:** Namensregel `AdNamensregel` (SG-<BEREICH>-<bezeichnung>), Prüfung der
Angaben, Konto der verantwortlichen Person suchen (es muss bestehen und aktiv sein), verbindliche
Prüfung, dass `sAMAccountName` und `cn` in der ganzen Domäne frei sind, `create-group` in
`AD_GRUPPEN_OU` mit diesen Attributen, inline in `attributes` (ausgewertet vom AD-Worker):

| Attribut | Wert |
|---|---|
| `cn`, `sAMAccountName` | aus der Namensregel |
| `groupType` | global `-2147483646`, domänenlokal `-2147483644`, universell `-2147483640` (Sicherheitsgruppe; Werte laut Microsofts Dokumentation von groupType: 0x2, 0x4, 0x8, jeweils mit dem Bit 0x80000000) |
| `description` | Zweck und Auftrag |
| `managedBy` | DN des Kontos der verantwortlichen Person (versiegelt; nur der Worker liest ihn) |
| `info` | Herkunftskennzeichnung `atlas-katalog <Auftrag>/<Position>` |

**Aktionen:**

| Aktion | Formular | Ablauf | Ergebnis |
|---|---|---|---|
| Mitglied aufnehmen (`mitglied`) | `ad-gruppe-mitglied` | Konto suchen (der Filter prüft den Anmeldenamen), Gruppe über die Kennzeichnung finden, mit einer Suche auf `member` prüfen, ob das Konto schon Mitglied ist, `add-group-member`; schon Mitglied ist erledigt | `completed`, sonst `rejected` |
| Mitglied entfernen (`mitglied-weg`) | `ad-gruppe-mitglied` | wie oben, `remove-group-member`; kein Mitglied ist erledigt | `completed`, sonst `rejected` |
| Verantwortung übergeben (`verantwortung`) | `ad-gruppe-verantwortung` | neues Konto suchen (aktiv), `update-attributes` ersetzt `managedBy` | `completed`, sonst `rejected` |
| Gruppe löschen (`deprovision`) | – | siehe unten | `returned` oder `returnFailed` |

**Rückgabe:** Eine leere Gruppe wird gelöscht. Hat sie noch Mitglieder, geht die Aufgabe «Gruppe
hat noch Mitglieder» an die empfangende Person: Entweder entfernt der Prozess alle Mitglieder und
löscht die Gruppe, oder die Rückgabe wird nicht ausgeführt. Ob die Gruppe Mitglieder hat, fragt eine
Suche mit `(member=*)`; entfernt werden sie mit einem `update-attributes`, das `member` auf `null`
setzt und das Attribut damit leert. Die DNs der Mitglieder kommen nie in die Instanz. Dann meldet er `returnFailed`: Die
Gruppe und das Recht bleiben, die Position zeigt, dass eine Rückgabe versucht wurde, und sie lässt
sich jederzeit wiederholen. **Solange die Position `returnFailed` ist, nimmt der Shop keine
Aktion an** (nur ein gehaltenes Recht nimmt Aktionen an, so an einem lokalen Server beobachtet).
Wer die Gruppe zuerst selbst leeren will, entfernt die Mitglieder vor der Rückgabe.

### Berechtigungen

Eine Berechtigung auf ein Fileshare oder eine Applikation ist eine Mitgliedschaft in einer
bestehenden AD-Gruppe. Alle Produkte `ad-recht-*` sind an **einen** Lebenszyklus gebunden
(`ad-berechtigung.bpmn`) und an **eine** Genehmigung; welche Gruppe ein Produkt meint, sagt die
Entscheidung `AdBerechtigungen` (Produkt-ID → Gruppe). Ein neues Recht ist eine Zeile in dieser
Tabelle und ein Produkt im Katalog, kein neuer Prozess.

**Bestellformular** (`ad-berechtigung-bestellung`): `konto`, der Anmeldename der empfangenden
Person.

**Bereitstellen:** Tabelle lesen, Angaben prüfen, Konto suchen (aktiv), Gruppe lesen (`scope`
base: gibt es sie?), mit einer zweiten Suche auf die Gruppe prüfen, ob das Konto schon in `member`
steht, `add-group-member`, `done`. Ein Konto, das schon Mitglied ist, hat das Recht bereits; die
Position wird erledigt gemeldet. **Rückgabe:** Tabelle, Konto (auch ein gesperrtes), Mitgliedschaft,
`remove-group-member`, `returned`; gibt es das Konto nicht mehr, ist nichts zu entziehen; kennt die
Tabelle das Produkt nicht mehr, meldet sie `returnFailed`.

**«ändern» schliesst «lesen» aus** (Kante `excludes`, ADR-0342): Die Bestellung lehnt beim
Aufgeben ab, wer beide zugleich hielte. Wer von «lesen» auf «ändern» wechselt, gibt «lesen» zuerst
zurück.

### Die Genehmigung durch die Gruppenverantwortung

`proc_ad_genehmigung_gruppenverantwortung` (`ad-genehmigung-gruppenverantwortung.bpmn`) ist die
Genehmigungsart der drei Berechtigungen, gebaut wie die Genehmigung der Microsoft-365-Dienste:

1. `AdBerechtigungen` nennt die Gruppe; `search` liest von ihr nur `managedBy` (`select`).
2. `search` mit `baseDN` = dem DN aus `managedBy`, `scope` base, liest vom Konto der
   verantwortlichen Person nur `userPrincipalName`, `displayName` und `userAccountControl`.
3. `GET /api/v1/users/assignable` sucht ihr Atlas-Konto: ein Benutzername gleich der UPN, sonst
   genau ein Benutzername gleich dem Teil vor dem «@» mit gleichem Anzeigenamen.
4. Aufgabe «Berechtigung genehmigen» (Formular `genehmigung` der Plattform) an dieses Konto. Hat
   die Gruppe kein `managedBy`, zeigt es auf eine Gruppe oder ein gesperrtes Konto, oder hat die
   Person kein Atlas-Konto, geht die Aufgabe an die Ersatzgruppe: `approval.ref` des Produkts
   (`{{ad-administration}}`), sonst die Gruppe `AD-Administration`. `entscheidungsweg` sagt warum.
5. Ergebnis an den Auftrag: `…/start` mit `approvedBy` oder `…/decision` mit der Begründung.

Erinnerung und Eskalation sind nicht modelliert; eine liegengebliebene Genehmigung zeigt Atlas
unter den festgefahrenen Genehmigungen.

### Admin-Konto

**Bestellformular** (`ad-admin-konto-bestellung`): Hauptkonto (Anmeldename, höchstens 16
Zeichen), Begründung (`anlass`), gültig bis (höchstens 365 Tage).

**Bereitstellen:** Ablaufdatum prüfen, Hauptkonto suchen (muss bestehen und aktiv sein, der
Anmeldename höchstens 16 Zeichen haben und kein eingebautes Konto wie `administrator` oder
`krbtgt` sein; das prüft der Filter der Suche), verbindliche Prüfung auf `sAMAccountName`, `cn`
und `userPrincipalName`, dann `create-user` in `AD_ADMIN_OU` **gesperrt** (`userAccountControl`
514) mit:

| Attribut | Wert |
|---|---|
| `sAMAccountName`, `cn` | `adm-<hauptkonto>`, kleingeschrieben |
| `userPrincipalName` | `adm-<hauptkonto>@<AD_UPN_SUFFIX>` |
| `displayName` | «<Anzeigename des Hauptkontos> (Admin)» |
| `seeAlso` | DN des Hauptkontos: die Verbindung zur Person |
| `accountExpires` | Ablaufdatum als FILETIME (siehe unten) |
| `description`, `info` | Zweck und Auftrag, Herkunftskennzeichnung |

Danach gibt die empfangende Person in einer Aufgabe das Initialpasswort ein, `set-password`
setzt es, eine Suche überschreibt die Variable (siehe «Passwortübergabe»), `enable` gibt das
Konto frei (512), `done`.

Den Namen bildet **nicht** die Entscheidung `AdNamensregel`: Er entsteht aus dem Anmeldenamen des
Hauptkontos, und der ist versiegelt. Jeder Ausdruck, den der AD-Worker auswertet (`dn`, `filter`,
`attributes`), bildet ihn gleich: `"adm-" + lower case(hauptkonto)`.

Die Verbindung zum Hauptkonto steht in **`seeAlso`**, einem Standardattribut jedes
Benutzerobjekts, das einen DN aufnimmt. `extensionAttribute1` bis `15` gibt es erst mit dem
Schema von Exchange, und ein Verzeichnis ohne Exchange hätte das Attribut nicht.

**Rückgabe:** `disable`, dann `delete`. Ein gelöschtes Konto lässt sich nur über den
AD-Papierkorb zurückholen, wo er aktiviert ist; eine Quarantäne-OU wäre die vorsichtigere Wahl,
hinterlässt aber Konten, die jemand später löschen muss. **Austritt:** Das Produkt nennt
`beispiel-m365-dienste` in `commandedBy`, und die Aktion `deprovision` hat den Auslöser
`operator`: Der Austrittsprozess der Microsoft-365-Dienste kann das Admin-Konto einer austretenden
Person mit einer Shop-Aufgabe im Modus `command` zurückgeben (Produkt `ad-admin-konto`, Aktion
`deprovision`).

### Servicekonto

**Bestellformular** (`ad-servicekonto-bestellung`): Bezeichnung, Zweck, gewünschter Service
Principal Name (freiwillig). Verantwortlich ist die empfangende Person; das Recht steht in ihrem
Bestand und wird rezertifiziert.

**Bereitstellen:** Namensregel (`svc-<bezeichnung>`), verbindliche Prüfung, Konto gesperrt in
`AD_SERVICE_OU` anlegen, Passwort durch die verantwortliche Person, aktivieren, `done`.

**Kein `servicePrincipalName`:** Der gewünschte SPN steht nur in `description`. Ein SPN, den
schon ein anderes Konto trägt, bricht dessen Kerberos-Anmeldung; welcher Name richtig ist und ob
er frei ist, prüft die AD-Administration (`setspn -S` sucht nach Duplikaten). Wer das
automatisieren will, braucht vorher eine Suche nach dem SPN in der ganzen Gesamtstruktur.

### Testkonto

**Bestellformular** (`ad-testkonto-bestellung`): Bezeichnung, gültig bis (höchstens 90 Tage).

**Bereitstellen** wie beim Servicekonto, mit `accountExpires`. Danach **wartet die Instanz** bis
00:00 UTC nach dem Ablaufdatum und gibt die Position mit einer Shop-Aufgabe im Modus `command`
zurück (Aktion `deprovision`, im Namen der eigenen Applikation `beispiel-ad-dienste`, die das
Produkt in `commandedBy` nennt). Die Rückgabe startet die Operation `deprovision`, die das Konto
sperrt, löscht und `returned` meldet. `returned` direkt zu melden geht nicht: Die Bestellung nimmt
es nur von der Rückgabe an, um die gebeten wurde (`api/order/advance.go`). Wurde das Konto vorher
zurückgegeben, lehnt die Bestellung den Befehl ab (`SHOP_COMMAND_REFUSED`), und ein
Fehler-Grenzereignis beendet die Instanz still.

### Konto für eine externe Person

**Bestellformular** (`ad-externes-konto-bestellung`): Vorname, Nachname, Firma, E-Mail-Adresse,
das eigene Konto (der Sponsor) und gültig bis (höchstens 180 Tage). Die empfangende Person ist
der Sponsor: Das Recht steht in ihrem Bestand, und ihr Konto steht in `manager`.

**Bereitstellen:** Ablaufdatum prüfen, Sponsor suchen (aktiv), verbindliche Prüfung auf den
Namen, Konto gesperrt in `AD_EXTERN_OU` anlegen (`accountExpires`, `manager`), Initialpasswort durch
den Sponsor, aktivieren, `done`. Die Aufgabe zeigt dem Sponsor Name, Firma und Adresse der externen
Person, damit er weiss, wem er das Passwort gibt.

**Das Konto heisst `ext-<vorname>.<nachname>`** und trägt `givenName`, `sn`, `displayName`,
`company` und `mail`. Der Name wird kleingeschrieben, Umlaute werden ausgeschrieben (ä → ae,
é → e), alles ausser `a-z` und `0-9` fällt weg, und nach `ext-` wird auf 16 Zeichen gekürzt, damit
der `sAMAccountName` höchstens 20 Zeichen hat: Zoë-Anna Müller-Lüdenscheidt wird
`ext-zoeanna.muellerl`. Gebildet wird der Name in den Ausdrücken, die der AD-Worker auswertet,
nicht in `AdNamensregel`, weil Vor- und Nachname versiegelt sind (siehe «Personendaten»). Gibt es
den Namen schon, lehnt der Prozess die Bestellung mit Begründung ab; einen zweiten Kandidaten
bildet er nicht (siehe «Bekannte Grenzen»). Eine leere Firma oder Adresse fehlt im Eintrag.

### Versetzung

**Bestellformular** (`ad-versetzung-bestellung`): das Konto, die bisherige und die neue
Organisationseinheit (FIN, HR, IT).

Eine Versetzung ist **kein Recht, das jemand hält**, und Atlas kennt kein Produkt ohne Bestand:
Der Katalog veröffentlicht kein Produkt ohne Bereitstellung und Rückgabe, und eine Aktion mit dem
Effekt `service` gehört zu einem gehaltenen Recht. Weil kein Produkt dieses Pakets das Konto einer
Person als Recht führt, an das sich die Versetzung hängen liesse, ist sie ein **Produkt, das sich
nach der Ausführung selbst zurückgibt**: `done`, dann eine Shop-Aufgabe im Modus `command`
(`deprovision`), deren Rückgabe im Verzeichnis nichts ändert und `returned` meldet. Im Bestand
bleibt eine zurückgegebene Position: der Nachweis, wer wann wohin versetzt wurde, kein entzogenes
Recht.

**Ablauf:** `AdOe` für beide Einheiten, Angaben prüfen, Konto suchen (`scope` one unter der OU
der bisherigen Einheit: es muss direkt dort liegen), mit **einer** Suche die Gruppen beider Einheiten lesen, in denen das
Konto Mitglied ist, aus den Gruppen der bisherigen austragen, die nicht auch zur neuen gehören, in
die fehlenden der neuen eintragen (je eine sequentielle Mehrfachinstanz), dann `move` in die OU der
neuen Einheit, `done`, Selbstrückgabe. Die Gruppen kommen vor dem Verschieben, weil sich dabei der
DN des Kontos ändert. Die Idee entspricht den «Mitgliedschaften der Organisationseinheit» der
Microsoft-365-Dienste; dort ist die Versetzung als Grenze vermerkt.

### Entsperren und Passwort zurücksetzen

An Admin-, Service-, Test- und externen Konten, je als Aktion mit dem Effekt `service`:

| Aktion | Ablauf | Ergebnis |
|---|---|---|
| Konto entsperren (`entsperren`) | Konto über die Kennzeichnung finden, `update-attributes` setzt `lockoutTime` auf `"0"` | `completed`, ohne Konto `rejected` |
| Passwort zurücksetzen (`passwort`) | Konto finden, Aufgabe an die empfangende Person, `set-password`, Variable überschreiben | `completed`, ohne Konto `rejected` |

`lockoutTime` 0 hebt eine Sperre nach zu vielen Fehlversuchen auf, nicht die Deaktivierung.

## Die Entscheidungen

| Modell (Handle) | Entscheidung | Wofür |
|---|---|---|
| `ad-namensregel` | `AdNamensregel` | Objekttyp (sicherheitsgruppe, service, test) und Eingaben → `cn`, `sAMAccountName`, `displayName`, `erlaubt`, `grund`. Präfixe SG-, svc-, tst-; Sperrliste (administrator, admin, krbtgt, guest, gast, root, system …); Kontonamen höchstens 20 Zeichen, Gruppennamen höchstens 64. Die Namen des Admin-Kontos (adm-) und des externen Kontos (ext-) entstehen aus versiegelten Werten und deshalb in den Prozessen, siehe dort |
| `ad-berechtigungen` | `AdBerechtigungen` | Produkt-ID → Gruppe (`gruppeCn`, `gruppeDn`), Bezeichnung, `maxTage` als Hinweis; drei Beispielzeilen |
| `ad-oe` | `AdOe` | Kürzel der Organisationseinheit → OU und Liste der Gruppen; drei Beispielzeilen |

Jede Tabelle hat eine Auffangregel, die ablehnt (fail-closed). Die DNs in `AdBerechtigungen` und
`AdOe` sind Beispiele und auf das eigene Verzeichnis anzupassen; die Gruppen müssen bestehen.

## Governance

- **Namensregel als Entscheidung.** Die bestellende Person gibt Bereich und Bezeichnung an;
  `AdNamensregel` bildet den Namen und lehnt reservierte und zu lange mit Begründung ab. Danach
  prüft der Prozess **verbindlich** im Verzeichnis, dass der Name frei ist. Ausgenommen sind die
  Namen, die aus Personendaten entstehen (`adm-<Anmeldename>`, `ext-<vorname>.<nachname>`): Eine
  Entscheidung wertet die Engine aus, und die liest keinen versiegelten Wert. Diese beiden Regeln
  stehen in den Prozessen, in den Ausdrücken, die der AD-Worker auswertet, mit derselben
  Längengrenze; die Sperrliste der Hauptkonten steht im Filter der Hauptkontosuche.
- **Herkunftskennzeichnung.** Jedes Objekt, das dieses Paket anlegt, trägt im Attribut `info`
  `atlas-katalog <Auftrag>/<Position>`. Rückgabe und Aktionen finden das Objekt darüber, nicht über
  den Namen, der sich nach einer Änderung der Namensregel anders bilden würde.
- **LDAP-Filter ohne Freitext.** Ein Suchfilter, der aus einer Eingabe gebaut wird, ist eine
  Einladung zur Filter-Injektion: `amaier)(|(objectClass=*` als Anmeldename gibt einer Suche einen
  anderen Sinn. In diesem Paket steht deshalb **nie ein ungeprüfter Wert in einem Filter**.
  Anmeldenamen hält das Formular auf `^[A-Za-z0-9._-]{1,20}$`, und weil sie versiegelt sind und
  kein Gateway sie lesen darf, prüft der Filterausdruck selbst sie noch einmal: `matches()` in dem
  Ausdruck, den erst der AD-Worker auswertet. Hat der Name die Form nicht, fragt die Suche einen
  Filter, der nichts findet (`(&(objectClass=user)(!(objectClass=user)))`), und der Prozess lehnt
  ab, als gäbe es das Konto nicht. Jeder Wert, der in einen Filter kommt, wird zusätzlich nach RFC
  4515 maskiert (`\`, `*`, `(`, `)`), ein DN ebenso. Namen kommen aus der Namensregel oder aus
  Ausdrücken, die nur `a-z`, `0-9`, Punkte und Bindestriche erzeugen. Zweck und Begründung gehen
  nie in einen Filter. Wer ein Formular erweitert, hält sich an dieselbe Regel: Muster im Formular
  **und** Prüfung im Filterausdruck, weil eine Antwort auch an der Oberfläche vorbei kommen kann.
- **Befristung und Rezertifizierung.** Berechtigungen, Admin-, Service-, Test- und externe Konten
  tragen `maxDays`: Ihr Recht erhält ein Enddatum (ADR-0344). Wer zusätzlich die beiden
  Beispiele `examples/befristung.bpmn` (gibt täglich überfällige Rechte über die Bestellung zurück)
  und `examples/rezertifizierung.bpmn` (vierteljährliche Kampagne, ADR-0341) installiert,
  aktiviert die Rückgabe abgelaufener Rechte und die Rezertifizierung auch für die Rechte dieses
  Pakets; die Rückgabe läuft dann durch die Lebenszyklen hier. Konten tragen zusätzlich
  `accountExpires`, sodass AD die Anmeldung auch dann ablehnt, wenn die Rückgabe noch nicht
  gelaufen ist. Sicherheitsgruppen und Versetzungen sind nicht befristet.
- **Nur aktive Konten** findet eine Suche mit `(!(userAccountControl:1.2.840.113556.1.4.803:=2))`,
  AD's bitweiser Vergleichsregel: Das Bit ACCOUNTDISABLE ist nicht gesetzt. So beantwortet der
  Worker «gibt es ein aktives Konto?» mit einem Wahrheitswert, ohne dass die Engine die Flags des
  Kontos liest.
- **`accountExpires`** ist eine FILETIME: 100-Nanosekunden-Intervalle seit 1601-01-01 UTC. Gesetzt
  wird 00:00 UTC des Tages nach dem Ablaufdatum, so wie die Konsole «Ende des Tages» meint, aber in
  UTC statt in der Ortszeit. FEEL rechnet die Tage in zwei Teilen (bis 1901 und ab 1901), weil
  eine Dauer über 292 Jahre überläuft.

### Passwortübergabe – eine Entscheidung der Installation

**Vorgabe in diesem Paket:** Die empfangende Person (beim Servicekonto die verantwortliche, beim
externen Konto der Sponsor) gibt das Passwort in einer Aufgabe ein. Das Formular
`ad-konto-passwort` schreibt `passwort`, das als Personendatum deklariert ist und verschlüsselt
gespeichert wird (ADR-0314). `set-password` setzt es über LDAPS. Danach **überschreibt** eine
Suche die Variable mit dem Konto selbst: Die Engine darf ein Personendatum nicht schreiben, also
auch nicht leeren, ein Worker-Ergebnis darf es. Das Passwort steht dann nicht mehr in den
Variablen der Instanz; **im Verlauf bleibt der frühere Wert verschlüsselt**, bis der Schlüssel der
Person gelöscht wird.

**Empfohlene Ablösung:** eine Passwort-Ablage (ein Vault oder Passwortmanager mit eigener
Schnittstelle), in die der Prozess ein erzeugtes Passwort legt und aus der die Person es abholt,
ohne dass es je in Atlas steht. Das ist nicht modelliert, und welche Ablage es wird, entscheidet die
Installation.

### Personendaten

Seit der Ergänzung des AD-Worker-Typs vom 2026-11-03 (ADR-0166) versiegelt dieses Paket die
Kennungen und Namen von Personen, auch die, die ins Verzeichnis geschrieben werden (ADR-0314). Zwei
Fähigkeiten des Workers machen das möglich:

- **`attributes`:** Die Attribute von `create-user`, `create-group` und `update-attributes` stehen
  inline in einer JSON-Vorlage, deren `=…`-Werte der AD-Worker auswertet, wenn er den Job erhält,
  nicht die Engine. Ein versiegelter Wert darf darin vorkommen, ebenso in `dn`, `newDN`,
  `memberDN` und `filter`. `null` lässt ein Attribut beim Anlegen weg und leert es bei
  `update-attributes`.
- **`foundVariable` und `select`:** Eine Suche schreibt ihr Ergebnis (DNs, die meist den Namen der
  Person tragen) in eine versiegelte Variable und das Ergebnis «gefunden» als Wahrheitswert in eine
  zweite, die ein Gateway lesen darf. `select` beschränkt, was die Suche liefert; die Kontosuchen
  fragen `1.1`, also nur den DN.

**Versiegelt** (`atlas:personal`, Datensubjekt `recipient`):

| Prozess | Variablen |
|---|---|
| Sicherheitsgruppe | `verantwortlich`, `mitglied`, `neuerVerantwortlicher` (Anmeldenamen) und die gefundenen Konten `verantwortlichKonto`, `mitgliedKonto`, `neuesKonto` |
| Berechtigung | `konto`, `kontoTreffer` |
| Genehmigung durch die Gruppenverantwortung | `konto` (der Prozess liest es nicht) |
| Admin-Konto | `hauptkonto`, `haupt` (DN und Anzeigename des Hauptkontos), `konto` (DN des Admin-Kontos), `anlass`, `passwort` |
| Servicekonto, Testkonto | `passwort` |
| Externes Konto | `vorname`, `nachname`, `firma`, `email`, `sponsor`, `sponsorKonto`, `konto` (DN des externen Kontos), `passwort` |
| Versetzung | `konto`, `kontoTreffer` |

Die Formulare tragen an diesen Feldern kein `personal` = `false` mehr. Die Gateways lesen nur
Wahrheitswerte (`verantwortlichAktiv`, `kontoGefunden`, `istMitglied`, `belegt` …). Versiegelt wird
unter dem Schlüssel der empfangenden Person, auch der Anmeldename eines Mitglieds, das jemand in eine
Gruppe aufnimmt; nach einer Löschung dieses Schlüssels ist auch er nicht mehr lesbar.

**Im Klartext bleibt**, mit Grund:

- **Die Referenzen des Auftrags:** `orderId`, `positionId`, `itemId` und `recipient` (das
  Datensubjekt selbst; der Schlüssel, unter dem versiegelt wird, muss über einen Klartextwert zu
  finden sein) und die Herkunftskennzeichnung `atlas-katalog <Auftrag>/<Position>`, über die
  Rückgabe und Aktionen ihre Objekte finden.
- **Antworten, die niemanden benennen:** Bereich, Bezeichnung, Zweck, Gruppenbereich, Ablaufdatum,
  gewünschter SPN, die Organisationseinheiten. Sie tragen in den Formularen `personal` = `false`.
- **Objekte, die keine Person benennen:** Name und DN einer Sicherheitsgruppe und ob sie Mitglieder
  hat (eine Suche auf `(member=*)`; gezählt wird nicht), Servicekonten und Testkonten.
- **Die Gruppen einer Versetzung:** Die DNs der Gruppen beider Einheiten, in denen das Konto Mitglied
  ist (`gruppen.weg`, `gruppen.dazu`), stammen aus der Tabelle `AdOe`, und die Mehrfachinstanzen,
  die sie abarbeiten, wertet die Engine aus.
- **Die verantwortliche Person einer Gruppe in der Genehmigung:** UPN, Anzeigename und DN aus
  `managedBy`. Die Engine muss sie lesen, um die Aufgabe ihrem Atlas-Konto zuzuweisen, und sie
  gehören nicht der empfangenden Person, unter deren Schlüssel versiegelt würde
  (`proc_m365_genehmigung_vorgesetzter` hält es mit dem Vorgesetzten gleich). Die Suchen liefern mit
  `select` nur diese drei Attribute.
- **Was im Verzeichnis steht:** Ein versiegelter Wert ist nur in Atlas versiegelt. Im Active
  Directory stehen Name, Firma und Adresse der externen Person und die DNs in `managedBy`,
  `manager`, `seeAlso` und `member` im Klartext, wie bei jedem anderen Eintrag.
- **Die Antworten im Auftrag:** Was ein Bestellformular beantwortet, speichert auch die Bestellung
  (der Shop zeigt es den Genehmigenden); das versiegelt nicht der Prozess.

## Voraussetzungen auf der Zielinstanz

1. **Worker `ad_verzeichnis`** (Worker-Typ AD, Console → Workers) mit einer Verbindung
   **`ldaps://`**: AD nimmt ein Passwort (`unicodePwd`) nur über eine verschlüsselte Verbindung
   an, und jedes Konto dieses Pakets bekommt eines. Das Dienstkonto des Workers braucht im
   Verzeichnis die folgenden Rechte. Sie stehen hier als das, was die Operationen brauchen; an
   einem echten Verzeichnis **nicht geprüft**:

   | Ort | Rechte | Wofür |
   |---|---|---|
   | ganze Domäne | lesen (haben authentifizierte Benutzer meist schon) | Suchen nach Konten und Gruppen, Prüfung freier Namen |
   | `AD_GRUPPEN_OU` | Gruppenobjekte erstellen und löschen; an Gruppenobjekten `member`, `managedBy`, `description`, `info` schreiben | Sicherheitsgruppe anlegen, Mitglieder, Verantwortung, Rückgabe |
   | OU der Berechtigungsgruppen und der Gruppen der Organisationseinheiten | an Gruppenobjekten `member` schreiben («Mitglieder schreiben») | Berechtigungen, Versetzung |
   | `AD_ADMIN_OU`, `AD_SERVICE_OU`, `AD_TEST_OU`, `AD_EXTERN_OU` | Benutzerobjekte erstellen und löschen; «Kennwort zurücksetzen»; an Benutzerobjekten `userAccountControl`, `accountExpires`, `lockoutTime`, `description`, `info`, `seeAlso`, `manager`, `displayName`, `userPrincipalName`, `givenName`, `sn`, `company`, `mail` schreiben | Konten anlegen, aktivieren, sperren, entsperren, Passwort, Rückgabe |
   | OUs der Organisationseinheiten unter `AD_PERSONEN_OU` | Benutzerobjekte löschen (Quelle) und erstellen (Ziel), `name`/`cn` schreiben | `move` bei der Versetzung (laut Microsoft braucht ein Verschieben das Löschen am alten und das Erstellen am neuen Ort) |

   Für den Versuch ohne Verzeichnis: `ATLAS_AD_MOCK` (ADR-0181) mit einem Seed (LDIF), der die
   Konten, die Gruppen aus `AdBerechtigungen` und `AdOe` und die OUs der Einheiten enthält. Der Mock
   kennt die bitweise Vergleichsregel und die Maskierung nach RFC 4515, die die Suchen verwenden.
2. **Secret `atlas`** im Vault: ein API-Token mit der Rolle `operator` (Console → Administration →
   API tokens, Scope `full`, «Direkt im Vault ablegen», Name `atlas`). Damit melden die Prozesse
   ihre Positionen und geben Testkonten und Versetzungen zurück.
3. **Gruppen** in Atlas: die Zielgruppe, die Informationssicherheit und die AD-Administration.
   Die Genehmigung der Berechtigungen fällt ohne `approval.ref` auf die Gruppe `AD-Administration`
   über ihren Namen zurück.
4. **Atlas-Konten der Gruppenverantwortlichen**, am einfachsten aus der Kontenspiegelung
   (ADR-0332); wer keines hat, wird durch die AD-Administration vertreten. Für die Genehmigungsart
   `superior` braucht die bestellende Person eine vorgesetzte Person in ihrem Atlas-Konto.
5. **Der Applikationsschlüssel** `beispiel-ad-dienste`: Testkonto und Versetzung nennen ihn in
   `commandedBy`, um sich selbst zurückzugeben; das Admin-Konto nennt `beispiel-m365-dienste`. Wer
   unter anderen Schlüsseln installiert, ändert `commandedBy` in `katalog.json`.
6. **Applikationskonstanten** (Console → Applikation → Constants), nach dem Import:

   | Konstante | Fallback | Bedeutung |
   |---|---|---|
   | `AD_DOMAIN_DN` | `DC=contoso,DC=local` | Domäne; Basis der Prüfung freier Namen und der Suche nach Mitgliedschaften |
   | `AD_UPN_SUFFIX` | `contoso.ch` | Suffix der `userPrincipalName` neuer Konten; muss in der Gesamtstruktur als UPN-Suffix eingetragen sein |
   | `AD_PERSONEN_OU` | `OU=Personen,DC=contoso,DC=local` | Basis der Suche nach bestehenden Konten (Personen, Verantwortliche, Sponsoren) |
   | `AD_GRUPPEN_OU` | `OU=Gruppen,OU=Atlas,DC=contoso,DC=local` | wo Sicherheitsgruppen entstehen |
   | `AD_ADMIN_OU` | `OU=Admins,OU=Atlas,DC=contoso,DC=local` | wo Admin-Konten entstehen |
   | `AD_SERVICE_OU` | `OU=Servicekonten,OU=Atlas,DC=contoso,DC=local` | wo Servicekonten entstehen |
   | `AD_TEST_OU` | `OU=Testkonten,OU=Atlas,DC=contoso,DC=local` | wo Testkonten entstehen |
   | `AD_EXTERN_OU` | `OU=Extern,OU=Atlas,DC=contoso,DC=local` | wo externe Konten entstehen |
   | `ATLAS_API_BASE` | `http://127.0.0.1:8080` | Adresse, unter der die Prozesse Atlas selbst erreichen, wenn eine Rückgabe oder Aktion sie nicht mitbringt |
7. **Die Tabellen `AdBerechtigungen` und `AdOe`** mit den Gruppen und OUs des eigenen
   Verzeichnisses, und die Auswahl der Organisationseinheiten im Formular
   `ad-versetzung-bestellung` passend zu `AdOe`.

## Installieren

```sh
ATLAS_TOKEN=atlasat_… ./installieren.sh https://atlas.intern.example.ch antworten.json
```

bzw. unter Windows

```powershell
$env:ATLAS_TOKEN = 'atlasat_…'
.\installieren.ps1 -Server https://atlas.intern.example.ch -Antworten antworten.json
```

Das Skript lädt die drei Entscheidungsmodelle unter ihren Handles hoch
(`POST /api/v1/dmn-models?handle=…`; der Quellbaum trägt sie ebenfalls, sodass `atlas import`
und atlas hub ohne diesen Schritt auskommen), importiert die Applikation als Quellbaum
(`POST /api/v1/applications/source`), veröffentlicht sie und füllt die Platzhalter des
Katalogdokuments mit den Gruppen-IDs der Instanz, bevor es den Katalog importiert
(`POST /api/v1/catalogs/import`).

Das Token braucht die Rolle `modeler`. **Den Katalog importiert nur, wer `productmanager` ist,
und ein API-Token trägt diese Rolle nie.** Auf einer Instanz mit Anmeldung legt das Skript das
ausgefüllte Dokument dann als `katalog-zum-import.json` ab, und eine angemeldete Person mit
`productmanager` lädt es in der Console unter **Catalogue → Import** hoch, mit «veröffentlichen».
Die importierte Applikation gehört dem Token, das sie angelegt hat; ein anderes Token oder eine
Sitzung sieht sie erst, wenn sie übergeben oder geteilt ist.

`antworten.json` beantwortet `fragen.json` (Vorlage: `antworten.beispiel.json`); Gruppen dürfen
mit Namen oder ID angegeben werden. Ohne Skript: die drei `.dmn`-Dateien unter den Handles
`ad-berechtigungen`, `ad-namensregel` und `ad-oe` hochladen, die Applikation über **Import
source** importieren und veröffentlichen, dann `katalog.json` mit den Gruppen-IDs anstelle der drei
Platzhalter importieren.

## Was geprüft ist und was nicht

**Nach der Umstellung auf versiegelte Kennungen** (2026-11-03, `attributes`, `select`,
`foundVariable`) liefen die Prozesse gegen einen lokalen Atlas-Server aus diesem Repository ohne
Anmeldung und ohne Katalog, der AD-Worker im Mock-Modus (`ATLAS_AD_MOCK`, LDIF-Seed, `ldaps://`).
Gestartet wurden die Operationen über die Trigger-Route
(`POST /api/v1/processes/{id}/triggers/{nachricht}`), die Personendaten beim Eintreten versiegelt wie
der Shop; die Meldungen an den Auftrag scheiterten dort mangels Auftrag, wie erwartet. Geprüft:

- In jeder Instanz stehen die deklarierten Variablen im Verlauf versiegelt (`kind` `personal`), und
  keiner der Anmeldenamen, DNs, Namen oder Adressen kommt im Verlauf im Klartext vor.
- **Sicherheitsgruppe:** Gruppe mit `managedBy` auf ein Konto mit maskiertem Komma im DN
  (`CN=Meier\, Arno,…`), `groupType`, `info`; Mitglied aufnehmen, ein zweites Mal (schon Mitglied:
  die Suche mit dem maskierten DN fand es); eine Kennung mit Klammer abgelehnt (die Suche fand
  nichts); Verantwortung übergeben; Mitglied entfernen; Rückgabe einer nicht leeren Gruppe mit «ja»:
  `member` geleert, Gruppe gelöscht.
- **Berechtigung CRM:** aufgenommen, ein gesperrtes Konto abgelehnt, Rückgabe entfernt die
  Mitgliedschaft.
- **Admin-Konto:** aus dem Hauptkonto `AMaier` entstand `adm-amaier` mit `seeAlso`, `displayName`
  «Anna Maier (Admin)», `accountExpires`; Passwort, aktiviert (512), die Variable `passwort`
  überschrieben; `administrator` als Hauptkonto abgelehnt; entsperren setzt `lockoutTime` 0.
- **Externes Konto:** Zoë-Anna Müller-Lüdenscheidt wurde `ext-zoeanna.muellerl` mit `givenName`,
  `sn`, `displayName`, `mail`, `manager` und ohne `company` (leer); eine zweite Bestellung mit
  demselben Namen abgelehnt.
- **Versetzung** FIN → IT: aus der Gruppe der bisherigen Einheit ausgetragen, die gemeinsame Gruppe
  behalten, in die der neuen eingetragen, Konto verschoben.
- **Servicekonto** und **Testkonto** angelegt.

Die folgenden Ergebnisse stammen vom ersten Lauf vor der Umstellung, geprüft am 2026-11-03 gegen einen lokalen Atlas-Server aus diesem Repository mit Anmeldung, der
überwachte AD-Worker im Mock-Modus (`ATLAS_AD_MOCK` mit einem LDIF-Seed, Verbindung
`ldaps://`). Installation mit `installieren.sh` (Schritt 4 mit Anmeldung über eine Sitzung),
Bestellungen über `POST /api/v1/orders` mit den Antworten der Bestellformulare, Genehmigungen und
Aufgaben über `POST /api/v1/tasks/{key}/complete` als die Person, der sie gehörten:

- **Testkonto:** Prüfung, Namensregel, freie Namen, `create-user` gesperrt mit `accountExpires`
  (FILETIME von Atlas berechnet und in der Instanz nachgelesen), Passwort-Aufgabe,
  `set-password` über LDAPS, die überschriebene Variable `passwort`, `enable` (514 → 512), `done`;
  die Aktionen «Konto entsperren» und «Passwort zurücksetzen» (`completed`); die Rückgabe
  (`disable`, `delete`, `returned`).
- **Admin-Konto:** Genehmigung durch die Gruppe der Informationssicherheit, Hauptkonto gesucht,
  `adm-amaier` mit `seeAlso` und `displayName` angelegt, Passwort, aktiviert, `done`; Rückgabe
  `returned`.
- **Berechtigung CRM:** Genehmigung einmal an die Ersatzgruppe (die verantwortliche Person hatte
  kein Atlas-Konto, `entscheidungsweg` sagt es) und einmal an das Atlas-Konto `bhuber`, das über
  `managedBy` → `userPrincipalName` → Benutzername gefunden wurde; `add-group-member`, `done`;
  Rückgabe mit `remove-group-member`, `returned`.
- **Sicherheitsgruppe:** Genehmigung durch die vorgesetzte Person, Gruppe mit `groupType`,
  `managedBy` und `info` angelegt; Mitglied aufnehmen, Verantwortung übergeben (`completed`); eine
  Kennung mit Klammer abgelehnt (`rejected`), ohne dass eine Suche lief; eine reservierte
  Bezeichnung mit Begründung abgelehnt; Rückgabe einer nicht leeren Gruppe: mit «nein» in der
  Aufgabe «Gruppe hat noch Mitglieder» `returnFailed`, danach lehnte der Shop die Aktion «Mitglied
  entfernen» ab; mit «ja» Mitglieder entfernt, Gruppe gelöscht, `returned`.
- **Servicekonto** und **externes Konto** bis `done` (`manager` auf das Konto des Sponsors; das
  externe Konto hiess damals `ext-<Auftrags-ID>`); Rückgabe des externen Kontos.
- **Versetzung** FIN → IT: aus der Gruppe der bisherigen Einheit ausgetragen, die gemeinsame Gruppe
  behalten, in die der neuen eingetragen, Konto verschoben, `done` und Selbstrückgabe `returned`.
  Ein erster Lauf mit einem Seed, dem die Gruppe der neuen Einheit fehlte, endete als Incident am
  Eintragen (so verhält sich auch ein echtes Verzeichnis).

Nicht geprüft: nach der Umstellung der Weg über den Shop (Bestellung, Genehmigung, Meldungen an
den Auftrag, Shop-Aufgaben der Aktionen) und mit Anmeldung; irgendein Schritt gegen ein echtes
Active Directory (die Rechte des Dienstkontos, die Kennwortrichtlinie, `accountExpires` in der
Konsole, `move` mit den Rechten der OUs, `lockoutTime`, `member` mit `replace` ohne Werte leeren,
die bitweise Vergleichsregel, `select` `1.1`), der Zeitgeber des Testkontos bis zur Selbstrückgabe und der Pfad
`SHOP_COMMAND_REFUSED`, die Rückgabe des Admin-Kontos durch den Austrittsprozess der
Microsoft-365-Dienste, die Befristung und Rezertifizierung mit den beiden Beispielen,
`installieren.ps1`, und die Formulare in der Oberfläche des Shops (die Antworten kamen über die
API).

## Annahmen

1. Die Werte von `groupType` (global -2147483646, domänenlokal -2147483644, universell
   -2147483640) sind die von Microsoft dokumentierten für Sicherheitsgruppen.
2. Ein `sAMAccountName` eines Benutzerobjekts hat höchstens 20 Zeichen (Microsofts Angabe); für
   Gruppen gilt hier die Grenze von `cn` (64 Zeichen), weil AD längere Gruppennamen zulässt.
3. `lockoutTime` lässt sich nur auf 0 setzen, und das hebt eine Kontosperre auf.
4. `accountExpires` ist eine FILETIME in UTC; der Wert 00:00 UTC nach dem Ablaufdatum entspricht
   dem «Ende des Tages» in UTC.
5. `seeAlso`, `manager`, `managedBy` und `info` gibt es an Benutzer- bzw. Gruppenobjekten im
   Standardschema; `extensionAttribute*` nur mit dem Schema von Exchange.
6. Ein Konto ist gesperrt, wenn in `userAccountControl` das Bit 2 (ACCOUNTDISABLE) gesetzt ist;
   514 ist ein normales gesperrtes, 512 ein normales aktives Konto.
7. Ein Konto lässt sich gesperrt und ohne Passwort anlegen; das Passwort folgt über LDAPS.
8. AD führt die Mitgliedschaften eines Kontos beim Verschieben mit (verknüpfte Attribute); die
   Versetzung trägt die Gruppen trotzdem vor dem Verschieben aus und ein.
9. Wer im Formular `konto`, `hauptkonto` oder `sponsor` angibt, nennt sein eigenes Konto bzw. das
   der empfangenden Person; der Prozess prüft das nicht (die Genehmigenden sehen die Angabe).
10. Die Gruppen in `AdBerechtigungen` und `AdOe` bestehen, und die Gruppenverantwortlichen tragen
    in `userPrincipalName` den Namen, unter dem Atlas ihr Konto kennt.
11. AD leert ein Attribut, auch `member`, mit einem `replace` ohne Werte, und es beantwortet
    `(userAccountControl:1.2.840.113556.1.4.803:=2)` als «Bit 2 gesetzt»; `1.1` als Attributauswahl
    liefert nur den DN.
12. Vor- und Nachname einer externen Person enthalten mindestens einen Buchstaben (das Formular
    verlangt es); sonst entstünde ein Name wie `ext-.muster`.

## Bekannte Grenzen

- **Was im Klartext bleibt**, steht unter «Personendaten»: die Referenzen des Auftrags, die
  Gruppen einer Versetzung, die verantwortliche Person in der Genehmigung und alles, was im
  Verzeichnis selbst steht.
- **Ein vergebener Name eines externen Kontos** lehnt die Bestellung ab; zwei externe Personen mit
  demselben Namen (oder denselben ersten 16 Zeichen) brauchen die AD-Administration. Einen zweiten
  Kandidaten bildet der Prozess nicht.
- **Ein Anmeldename in falscher Form** sieht für den Prozess aus wie ein Konto, das es nicht gibt:
  Die Engine darf den versiegelten Wert nicht prüfen, also findet die Suche nichts. Bei der Rückgabe
  einer Berechtigung heisst das «nichts zu entziehen» (`returned`) statt `returnFailed`.
- **Nach `returnFailed` keine Aktionen.** Wer eine Gruppe behalten will, nachdem die Rückgabe
  nicht ausgeführt wurde, muss die Rückgabe wiederholen, um sie wieder ändern zu können; Mitglieder
  lassen sich solange nur im Verzeichnis selbst entfernen.
- **Geschützte Konten.** Wird ein Admin-Konto Mitglied einer geschützten Gruppe (etwa
  Domänen-Admins), setzt AdminSDHolder seine Berechtigungen auf die der Vorlage zurück; die
  delegierten Rechte auf `AD_ADMIN_OU` gelten dann nicht mehr, und Sperren, Löschen und Passwort
  scheitern als Incident. Solche Konten gibt die AD-Administration von Hand zurück.
- **Keine Erinnerung und Eskalation** bei der Genehmigung durch die Gruppenverantwortung.
- **Massgebend sind die Tabellen beim Ausführen:** Wird `AdBerechtigungen` geändert, entzieht die
  Rückgabe die Mitgliedschaft der neuen Zeile; wird `AdOe` geändert, versetzt eine spätere
  Versetzung nach der neuen.
- **Das Konto muss direkt in der OU der bisherigen Einheit liegen**; eine tiefere OU lehnt die
  Versetzung ab.
- **Höchstens 100 Gruppen** liest die Suche nach den Mitgliedschaften einer Versetzung; mehr
  lässt sie scheitern, statt abzuschneiden.
- **Der SPN eines Servicekontos** wird nicht gesetzt (siehe dort).
- **Das Ablaufdatum gilt in UTC**, nicht in der Ortszeit.

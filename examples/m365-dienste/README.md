# Microsoft-365-Dienste – ein Shop mit Governance

Ein Katalog aus der Sicht einer IT-Abteilung, die ihre Microsoft-365-Dienste über Atlas
anbietet: Was eine Person, ein Team oder eine Funktion bestellen kann, wird von den
Worker-Typen **Entra**, **Exchange**, **Teams Phone** und **SharePoint** bereitgestellt,
geändert und wieder entzogen. Das Paket enthält die Kataloge, die Produkte, alle Prozesse, die
Entscheidungstabellen, die Formulare (darunter zwei öffentliche) und eine wöchentliche
Governance-Prüfung, zweisprachig (Deutsch und Englisch).

Die Worker heissen `ucc_entra`, `ucc_exchange`, `ucc_teams` und `ucc_sharepoint`, dazu
`ad_verzeichnis` (Active Directory), wenn die Lizenzen über Gruppen im lokalen AD laufen (siehe
«Lizenzen: direkt oder über Gruppen»). Wer andere Namen verwendet, ersetzt sie in den `.bpmn`-Dateien (Attribut `connector`) und im
Bestellformular der SharePoint-Teamwebsite (Block `atlasAvailability`).

## Inhalt

| Datei | Inhalt |
|---|---|
| `atlas.json` | Manifest der Applikation «Beispiel: Microsoft-365-Dienste» (Schlüssel `beispiel-m365-dienste`): 23 Prozesse, 50 Formulare, 8 Entscheidungen samt ihren Modellen (Format 2) |
| `katalog.json` | Das Katalogdokument: drei Kataloge, sechzehn Produkte, vier Beziehungen |
| `fragen.json` | Was jeder Platzhalter im Katalogdokument fragt (Zielgruppen, Genehmigende) |
| `*.bpmn` | Die Prozesse, je mit Dokumentation an Prozess und Elementen |
| `*.dmn` | Die acht Entscheidungsmodelle |
| `*.form.json` | Bestell-, Aktions-, Aufgaben- und öffentliche Formulare |
| `installieren.sh`, `installieren.ps1` | Installation in eine Atlas-Instanz: Entscheidungsmodelle, Applikation, Veröffentlichung, Katalog |
| `antworten.beispiel.json` | Vorlage für die Antworten auf `fragen.json` |

## Die Kataloge

| Katalog | Rang | Zielgruppe | Produkte |
|---|---|---|---|
| Microsoft 365 für Mitarbeitende (`cat-m365-mitarbeitende`) | 500 | `{{zielgruppe}}` | Lizenz, Telefonie, Team, SharePoint-Teamwebsite, Viva-Engage-Community, Funktionspostfach, Verteiler, Postfachzugriff |
| Microsoft 365: Kommunikation und Telefonie (`cat-m365-ucc`) | 510 | `{{zielgruppe-ucc}}` | die dreizehn Produkte des Shops |
| Microsoft 365: HR-Eintritte (`cat-m365-hr`) | 520 | keine | Eintrittspaket, Lizenz mit Postfach (Eintritt), Mitgliedschaften der Organisationseinheit |

Eine Person sieht im Shop jeden Katalog, den ihre Gruppen erreichen (ADR-0522); Kategorien
gleichen Namens werden zusammengelegt. Führen zwei Kataloge dasselbe Produkt, stellt es der
mit dem höheren Rang. Der UCC-Katalog führt die allgemeinen Produkte trotzdem auch selbst,
damit er für sich allein vollständig bleibt.
Die Ränge müssen auf der Zielinstanz frei sein; sonst sind sie in `katalog.json` anzupassen.

Der Katalog `cat-m365-hr` hat keine Zielgruppe und erreicht deshalb niemanden: Im Shop sieht ihn keine
Person. Bestellt wird aus ihm nur vom Eintrittsprozess (siehe «Eintritt und Austritt (HR-Ereignis)»).
Sein Rang muss trotzdem frei sein, weil das Veröffentlichen jeden Rang nur einmal zulässt.

## Die Produkte

| Produkt | Worker | Genehmigung | Prozess | Aktionen nach der Bereitstellung |
|---|---|---|---|---|
| Microsoft 365: Lizenz mit Postfach (`m365-lizenz`), Varianten E3, E5, F3 | Entra, Exchange, im Modus `ad-gruppe` AD | vorgesetzte Person der betroffenen Person | `lizenz.bpmn` (Lebenszyklus) | Lizenz zurückgeben, Abwesenheitsnotiz setzen und beenden, Postfachkontingent erhöhen, Weiterleitung setzen und aufheben, Zusätzliche Mailadresse, Mailadresse entfernen, Kalenderstellvertretung erteilen und entziehen |
| Teams-Telefonie mit eigener Rufnummer (`m365-telefonie`), setzt die Lizenz voraus | Entra, Teams Phone | `{{telefonie-team}}` | `telefonie.bpmn` (Lebenszyklus) | Telefonie aufheben |
| Microsoft Teams: Team (`m365-team`) | Entra | vorgesetzte Person der verantwortlichen Person | `team.bpmn` (Lebenszyklus) | Team archivieren, Person aufnehmen, Kanal anlegen |
| SharePoint: Teamwebsite (`m365-sharepoint-site`) | Entra, SharePoint | vorgesetzte Person der Besitzerin oder des Besitzers | `sharepoint-site.bpmn` (Lebenszyklus) | Site endgültig zurückgeben, Externe Person einladen, Site-Besitzer ändern, Speicherkontingent erhöhen, Site löschen, Gelöschte Site wiederherstellen |
| Viva Engage: Community (`m365-engage`) | Entra | vorgesetzte Person der Besitzerin oder des Besitzers | `engage-community.bpmn` (Lebenszyklus) | Community löschen, Person aufnehmen, Person entfernen |
| Funktionspostfach (`m365-funktionspostfach`) | Exchange | vorgesetzte Person der verantwortlichen Person | `funktionspostfach.bpmn` (Lebenszyklus) | entfernen, Zugriff erteilen, Zugriff entziehen |
| Verteilerliste (`m365-verteiler`) | Exchange | keine | `verteiler.bpmn` (Lebenszyklus) | entfernen, Mitglied aufnehmen, Mitglied entfernen |
| Zugriff auf ein Postfach (`m365-postfachzugriff`), 365 Tage befristet | Exchange | vorgesetzte Person der Person, die den Zugriff erhält, je nach Regel zusätzlich Informationssicherheit | `postfachzugriff-erteilen.bpmn`, `postfachzugriff-entziehen.bpmn` (getrennte Prozesse) | Rückgabe |
| Sitzungszimmer (`m365-raum`) | Exchange | `{{facility-management}}` | `raum-einrichten.bpmn`, `raum-entfernen.bpmn` (getrennte Prozesse) | Rückgabe |
| Gerät (`m365-geraet`): Beamer, Fahrzeug, Leihgerät | Exchange | `{{facility-management}}` | `geraet-einrichten.bpmn`, `geraet-entfernen.bpmn` (getrennte Prozesse) | Rückgabe |
| Anrufwarteschlange (`m365-warteschlange`) | Teams Phone, Entra | `{{telefonie-team}}` | `warteschlange.bpmn` (Lebenszyklus) | entfernen, Agenten ändern |
| Telefonzentrale (`m365-telefonzentrale`) | Teams Phone, Entra | `{{telefonie-team}}` | `telefonzentrale.bpmn` (Lebenszyklus) | entfernen |
| Aufbewahrung eines Postfachs (`m365-aufbewahrung`, Litigation Hold) | Exchange | `{{compliance}}` | `aufbewahrung.bpmn` (Lebenszyklus) | Aufbewahrung aufheben |
| Eintrittspaket Microsoft 365 (`m365-eintritt`), Paket aus den beiden folgenden Produkten | – | keine | `eintritt-paket.bpmn` (Lebenszyklus) | Eintrittspaket zurückgeben (nur Betrieb und Eintrittsprozess) |
| Microsoft 365: Lizenz mit Postfach (Eintritt) (`m365-eintritt-lizenz`), ohne Varianten | Entra, Exchange, im Modus `ad-gruppe` AD | keine | `lizenz.bpmn` (Lebenszyklus) | die Aktionen am Postfach wie bei `m365-lizenz`; Rückgabe nur durch den Betrieb |
| Mitgliedschaften der Organisationseinheit (`m365-oe-mitgliedschaften`), setzt die Lizenz voraus | Entra, Exchange | keine | `oe-mitgliedschaften.bpmn` (Lebenszyklus) | Mitgliedschaften entfernen (nur Betrieb) |

Sitzungszimmer, Gerät, Anrufwarteschlange, Telefonzentrale und Aufbewahrung stehen nur im UCC-Katalog,
die drei Produkte des Eintritts nur in `cat-m365-hr`.

Die Rufnummer wählt die Gruppe `M365-Telefonie` aus ihrem Nummernplan (Aufgabe «Rufnummer
wählen»), weil Atlas keinen Nummernbestand führt. Zugewiesen wird sie danach vom
Teams-Phone-Worker (`ensure-phone-number`, als Direct-Routing-Nummer); gehört sie schon
jemandem, kommt die Aufgabe mit einem Hinweis zurück (Grenzereignis auf
`TEAMSPHONE_NUMBER_IN_USE`). Bei der Rückgabe gibt `remove-phone-number` die persönliche
Nummer frei, bevor die Lizenz geht, und `remove-resource-account` gibt die Nummer einer
Warteschlange oder Telefonzentrale frei, bevor es das Konto löscht. Ein Ressourcenkonto
nimmt eine Rufnummer nur mit Nutzungsort und der Lizenz *Microsoft Teams Phone Resource
Account* an; beides gibt ihm der Entra-Worker gleich nach dem Anlegen.

## Das Postfach an der Lizenz

Die Lizenz bringt das Postfach in Exchange Online mit; die Aktionen am Produkt `m365-lizenz`
ändern dieses Postfach über den Worker `ucc_exchange`. Jede startet eine eigene Instanz von
`lizenz.bpmn` über ihren Nachrichtenstart und meldet ihr Ergebnis mit einer Shop-Aufgabe.

| Aktion | Formular | Ablauf | Ergebnis |
|---|---|---|---|
| Postfachkontingent erhöhen (`kontingent`) | `m365-postfach-kontingent` | `get-mailbox-statistics` liest die Belegung, `M365Postfachkontingent` entscheidet über Stufe und Lizenz, bei 100 GB prüft `M365-Administration` (Aufgabe «Kontingent prüfen»), `set-mailbox-quota` setzt die drei Grenzen | `completed`, bei Ablehnung `rejected` |
| Weiterleitung setzen (`weiterleitung`) | `m365-postfach-weiterleitung` | Ziel nur in der eigenen Domain (`MAIL_DOMAIN`), sonst Ablehnung mit Grund; `set-forwarding`, auf Wunsch mit Kopie im Postfach | `completed`, bei fremder Domain `rejected` |
| Weiterleitung aufheben (`weiterleitung-aus`) | – | `clear-forwarding` | `completed` |
| Zusätzliche Mailadresse (`alias`) | `m365-postfach-adresse` | `add-email-address` mit `smtp:<Lokalteil>@<MAIL_DOMAIN>` | `completed` |
| Mailadresse entfernen (`alias-weg`) | `m365-postfach-adresse` | `remove-email-address` mit derselben Adresse | `completed` |
| Kalenderstellvertretung erteilen (`stellvertretung`) | `m365-postfach-stellvertretung` | `run-get-cmdlet` mit `Get-MailboxFolderStatistics -FolderScope Calendar` liest den Namen des Kalenders, dann `add-folder-permission` auf `<upn>:\<Kalender>` mit `Editor` und `Delegate` | `completed` |
| Kalenderstellvertretung entziehen (`stellvertretung-weg`) | `m365-postfach-stellvertretung` | Kalender lesen wie oben, `remove-folder-permission` | `completed` |

**Kontingent.** Exchange Online begrenzt die Grösse eines Postfachs nach der Lizenz: mit E3 und
E5 auf 100 GB, mit F3 auf 2 GB. Einen grösseren Wert weist Exchange laut Microsofts Dokumentation
ab. Die Regel vergibt deshalb nur 50 und 100 GB und lehnt eine Erhöhung mit der Variante
Frontline ab. Ein Postfach mit E3 oder E5 hat von sich aus schon 100 GB; die Stufe 50 GB hat nur
Sinn, wo die Organisation ihre Postfächer tiefer begrenzt. Die Belegung ist ein Personendatum
(das Ergebnis trägt Anzeigenamen und letzte Anmeldung); sie entscheidet deshalb nicht mit, und
nur das Formular der Prüfung zeigt sie.

**Weiterleitung nur in die eigene Domain.** Die Zieladresse benennt eine Person und ist ein
Personendatum, das keine Bedingung lesen darf. Das Formular fragt sie deshalb in zwei Teilen:
den Teil vor dem «@» (Personendatum) und die Domain (nicht personenbezogen). Eine Script-Task
vergleicht die Domain mit `MAIL_DOMAIN` und bildet daraus `weiterleitungPruefung` mit `intern`
und `grund`; das Gateway liest nur dieses Ergebnis. Der Exchange-Worker setzt den Lokalteil erst
beim Aufruf vor die geprüfte Domain, sodass eine fremde Adresse nicht durch die Prüfung kommen
kann. Eine Ablehnung trägt keinen Text; der Grund steht in der Instanz
(`weiterleitungPruefung.grund`).

**Kalendername.** Exchange benennt den Kalender in der Sprache des Postfachs (Kalender,
Calendar, Calendrier, Calendario …), und eine Ordnerberechtigung braucht diesen Namen
(`<upn>:\Kalender`). Der Prozess liest ihn deshalb zuerst und nimmt den Ordner mit
`FolderType` Calendar.

## Lizenzen: direkt oder über Gruppen

Die Lizenz der Produkte `m365-lizenz` und `m365-eintritt-lizenz` kann `lizenz.bpmn` auf drei Wegen
vergeben. Welchen, sagt die Applikationskonstante `LIZENZ_MODUS`:

| `LIZENZ_MODUS` | Zuweisen | Entziehen | Erledigt bzw. zurückgegeben gemeldet |
|---|---|---|---|
| `direkt` (Fallback) | `assign-license` mit der SKU aus `M365Lizenzprofil` | `assign-license` mit `removeLicenses` | sofort nach dem Aufruf |
| `entra-gruppe` | `get-user` (Objekt-ID), dann `add-group-member` in die Cloud-Gruppe `entraGruppe` | `remove-group-member` aus derselben Gruppe | sobald die Lizenz aktiv ist bzw. die Mitgliedschaft entfernt ist |
| `ad-gruppe` | `get-user` (`onPremisesSyncEnabled`), AD `search` nach dem Konto, AD `add-group-member` in `CN=<adGruppe>,<AD_LIZENZ_OU>` | AD `search`, AD `remove-group-member` | wie `entra-gruppe` |

Ohne gesetzte Konstante gilt `direkt`, und eine bestehende Installation verhält sich wie bisher. Ein
anderer Wert als diese drei (Gross- und Kleinschreibung und Leerzeichen gleichgültig) hält die
Instanz am Gateway «Lizenzweg?» mit einem Incident an, statt still einen Weg zu wählen. Den
Nutzungsort setzt der Prozess in allen drei Modi vorher, weil Entra ID eine Lizenz ohne ihn auch über
eine Gruppe nicht vergibt.

**Warum Gruppen.** Microsoft beschreibt die gruppenbasierte Lizenzierung von Entra ID als den Weg,
Lizenzen in grösserem Umfang zu verwalten: Einer Sicherheitsgruppe wird eine Lizenz zugewiesen, und
jedes Mitglied erhält sie, solange es Mitglied ist. Wer die Lizenzen einer Organisation sehen will,
sieht sie an wenigen Gruppen statt an jedem Konto, und eine Lizenz entsteht und verschwindet mit der
Mitgliedschaft, auch wenn jemand sie ausserhalb von Atlas setzt. Laut Microsoft braucht die
gruppenbasierte Lizenzierung Microsoft Entra ID P1 oder eine Lizenz, die P1 enthält (etwa
Microsoft 365 E3); das ist Microsofts Dokumentation und an keinem Tenant geprüft. Dieser Kunde führt
seine Lizenzgruppen als **Gruppen im lokalen AD**, die Entra Connect nach Entra ID synchronisiert
(`ad-gruppe`); andere führen **Cloud-Gruppen** in Entra ID (`entra-gruppe`).

**Der Preis: Die Lizenz kommt später, und ein Fehler auch.** Mit `assign-license` weist Graph die
Lizenz sofort zu oder lehnt den Aufruf ab, und die Aufgabe wird zum Incident. In den Gruppenmodi
setzt der Prozess nur die Mitgliedschaft; die Lizenz vergibt Entra ID danach in seiner eigenen
Verarbeitung, eine AD-Gruppe erst nach dem nächsten Lauf von Entra Connect (üblich alle 30 Minuten).
Scheitert die Vergabe (keine freie Lizenz, widersprüchliche Servicepläne, ein Nutzungsort, in dem
die Lizenz nicht erhältlich ist), sieht man das erst am Konto, in `licenseAssignmentStates`. Der
Prozess prüft deshalb:

1. **Prüfintervall abwarten** (`LIZENZ_PRUEFINTERVALL`, Fallback `PT10M`).
2. **Lizenzstand lesen:** `get-user` mit `select` `assignedLicenses,licenseAssignmentStates`.
3. **Lizenzstand auswerten** (Script-Task, `lizenzStatus`): `aktiv`, wenn ein Eintrag für die SKU
   der Variante den Zustand `Active` hat (auch `ActiveWithError`: Die Lizenz wirkt, nur ein
   Serviceplan hat einen Fehler, dessen Code in `lizenzStatus.fehler` steht); `fehler` bei `Error`,
   mit dem Code von Entra ID (`CountViolation`, `MutuallyExclusiveViolation`, `DependencyViolation`,
   `ProhibitedInUsageLocationViolation`, `UniquenessViolation`, …); `zeit` nach `LIZENZ_PRUEFUNGEN`
   Prüfungen (Fallback 18, also drei Stunden); sonst `wartet`, und es geht zurück zu Schritt 1.
4. **Aktiv:** Erst jetzt meldet der Prozess die Position als erledigt. Damit beginnen auch die
   Positionen, die die Lizenz voraussetzen (die Mitgliedschaften des Eintrittspakets mit ihren
   Verteilern, die das Postfach brauchen), und die Aktionen am Postfach stehen der Person erst
   dann zur Verfügung, wie bisher erst nach der Zuweisung.
5. **Fehler oder Zeit abgelaufen:** Aufgabe «Lizenz klären» für `M365-Administration` mit dem
   Fehlercode und dem, was zu prüfen ist (Formular `m365-lizenz-klaeren`). «Erneut prüfen» beginnt
   die Prüfung von vorn, ohne die Mitgliedschaft noch einmal zu setzen; «Beenden» meldet die Position
   als `failed`.

Im Modus `ad-gruppe` liest der Prozess zuerst, ob das Konto aus dem AD synchronisiert ist
(`onPremisesSyncEnabled`). Ein Konto, das nur in Entra ID besteht, kann nicht Mitglied einer
AD-Gruppe sein; es geht an die Aufgabe «AD-Konto klären» (dasselbe Formular). Das betrifft die
Konten, die `hr-eintritt.bpmn` heute in Entra ID anlegt: Sie bestehen nur in der Cloud.

**Rückgabe.** In den Gruppenmodi entfernt der Prozess die Mitgliedschaft und meldet die Rückgabe
sofort. Die Lizenz verschwindet danach von selbst und ebenso verzögert (bei einer AD-Gruppe nach dem
nächsten Lauf von Entra Connect), mit ihr das Postfach (30 Tage wiederherstellbar). Der Prozess wartet
darauf nicht, weil das Recht, das Atlas vergeben hat, die Mitgliedschaft ist: Ihr Entfernen kann
nicht an fehlenden Lizenzen oder Serviceplänen scheitern, und Entra ID braucht dafür niemanden mehr.
Ein Warten hielte die Position stundenlang auf `returning`, ohne dass jemand etwas tun könnte. Hält
die Person dieselbe SKU zusätzlich direkt oder über eine andere Gruppe, bleibt ihr die Lizenz; das
ist dann ein anderes Recht als das zurückgegebene.

**Die Suche im AD.** Gesucht wird unter `AD_DOMAIN_DN` mit
`(&(objectCategory=person)(objectClass=user)(userPrincipalName=<upn>))` und höchstens einem Treffer.
Die UPN kommt aus einem Bestellformular und ist ein Personendatum; sie steht deshalb nur im Ausdruck,
den der AD-Worker auswertet. Damit sie den Filter nicht verändern kann (etwa mit `*)(cn=*`), maskiert
der Ausdruck die Zeichen, die in einem LDAP-Filter etwas bedeuten, nach RFC 4515 (`\` → `\5c`,
`*` → `\2a`, `(` → `\28`, `)` → `\29`); der Wert bleibt so immer ein Vergleichswert. Das Muster im
Formular (`^[^@\s]+@[^@\s]+\.[^@\s]+$`) genügt dafür nicht: Es lässt diese Zeichen zu, und ob der
Server es bei einer Bestellung über die API durchsetzt, ist nicht geprüft. Die Alternative, das Konto
über `onPremisesSamAccountName` aus Entra ID zu suchen, bräuchte dieselbe Maskierung (ein
sAMAccountName darf Klammern enthalten) und einen Aufruf mehr. Die Maskierung lokal gegen
`*)(cn=*` und `a*b(c)\d@x.ch` ausgewertet, nicht gegen ein AD.

**Berechtigungen.**

- `entra-gruppe`: am Worker `ucc_entra` `GroupMember.ReadWrite.All` (in `Group.ReadWrite.All`
  enthalten, das der Worker für die Teams ohnehin hat) und `User.Read.All` für `get-user`. Laut
  Microsoft lässt sich eine Gruppe, die Rollen zuweisen kann (role-assignable), damit nicht ändern;
  Lizenzgruppen sollten keine solchen sein.
- `ad-gruppe`: am Worker `ad_verzeichnis` (Worker-Typ AD, in der Console angelegt; derselbe Name wie im
  Paket `ad-dienste`) Lesen der Benutzerkonten unter `AD_DOMAIN_DN` und Schreiben des Attributs
  `member` an den Gruppen der OU `AD_LIZENZ_OU` (delegiert: «Mitglieder schreiben» für
  Gruppenobjekte), sonst nichts. Am Worker `ucc_entra` `User.Read.All`.
- In beiden Gruppenmodi braucht kein Worker eine Berechtigung auf Lizenzen: Die vergibt Entra ID
  selbst. `update-user` für den Nutzungsort braucht weiterhin `User.ReadWrite.All`.

**Voraussetzungen der Gruppenmodi.** Die Lizenzgruppen bestehen, und ihnen ist in Entra ID die SKU
derselben Zeile von `M365Lizenzprofil` zugewiesen; ihre Objekt-IDs (`entraGruppe`) bzw. ihr cn
(`adGruppe`) stehen in der Tabelle, deren Werte heute **Platzhalter** sind (`00000000-…-0000e003`,
`LIC-M365-E3`). Im Modus `ad-gruppe` liegen die Gruppen in `AD_LIZENZ_OU`, und Entra Connect
synchronisiert sie (die OU liegt im Bereich der Synchronisation). Die Person ist direkt Mitglied:
Verschachtelte Gruppen geben laut Microsoft keine Lizenz weiter, nur die direkten Mitglieder der
lizenzierten Gruppe erhalten sie (Microsofts Dokumentation, nicht geprüft).

**Geprüft und nicht geprüft.** Geprüft ohne Server und ohne Tenant: `lizenz.bpmn` kompiliert, auch
gegen die Regel, dass keine Bedingung und keine Script-Task ein Personendatum liest; das Diagramm hält
die Layout-Regeln ein; `M365Lizenzprofil` wurde mit allen Regeln lokal ausgewertet; die Ausdrücke für
den Modus, die Auswertung des Lizenzstands (wartet, aktiv, Fehler mit Code, Zeit abgelaufen), die
Hinweise, den Gruppen-DN und den maskierten LDAP-Filter wurden lokal ausgewertet. **Nicht geprüft**,
weder gegen einen Server noch gegen Entra ID oder ein AD: dass Graph `licenseAssignmentStates` in der
beschriebenen Form liefert und mit `select` keine weiteren Eigenschaften mitgibt, wie lange Entra ID
und Entra Connect brauchen, die Bedeutung der Fehlercodes, dass Graph und das AD eine schon
bestehende Mitgliedschaft abweisen, die AD-Suche und das Schreiben von `member` mit dem Worker
`ad_verzeichnis`, kein Durchlauf der drei Modi durch den Shop.

**Grenzen.**

- **Den Modus nicht wechseln, solange Personen Lizenzen halten.** Massgebend ist `LIZENZ_MODUS`
  beim Entziehen: Wer von `direkt` auf eine Gruppe wechselt, entfernt bei der Rückgabe einer früher
  direkt vergebenen Lizenz eine Mitgliedschaft, die es nicht gibt (Incident), und die direkte
  Zuweisung bleibt. Vor einem Wechsel die bestehenden Konten in die Gruppen aufnehmen und die
  direkten Zuweisungen entfernen.
- **Eine bestehende Mitgliedschaft** lässt das Zuweisen als Incident enden, eine fehlende das
  Entziehen.
- **Beenden lässt die Mitgliedschaft stehen.** Wird «Lizenz klären» beendet, bleibt die Person
  Mitglied; erhält sie die Lizenz später doch, steht sie nicht im Bestand. Wer beendet, entfernt die
  Mitgliedschaft von Hand.
- **Die UPN im AD ist dieselbe wie in Entra ID** (die übliche Einrichtung von Entra Connect). Wo
  Entra ID eine andere Anmelde-ID als die UPN des AD verwendet (Alternate Login ID), findet die Suche
  das Konto nicht; dann ist der Filter in «Konto im AD suchen» anzupassen.
- **Telefonie, Warteschlange und Telefonzentrale** weisen ihre Lizenzen weiter direkt zu; für sie
  hat `M365Lizenzprofil` keine Gruppe.

## Gerät und Aufbewahrung

**Gerät** (`m365-geraet`): ein Gerätepostfach (Equipment Mailbox) für einen Beamer, ein Fahrzeug
oder ein Leihgerät, gebaut wie das Sitzungszimmer. Exchange führt Räume und Geräte getrennt: Ein
Raum erscheint in Outlook unter den Räumen und hat eine Kapazität, ein Gerät wird als Ressource
eingeladen. Name nach der Namensregel mit dem Präfix GE, Buchungen werden automatisch angenommen
(`AutomateProcessing` AutoAccept, keine Überschneidungen, 180 Tage im Voraus). Die
Herkunftskennzeichnung trägt in `CustomAttribute4` den Standort und in `CustomAttribute5` die
Geräteart; die Rückgabe findet das Postfach über `CustomAttribute3` (Auftrag/Position).

**Aufbewahrung** (`m365-aufbewahrung`): Litigation Hold auf dem Postfach einer Person, die das
Bestellformular nennt (`upn`), mit Grund und Dauer in Tagen («unbegrenzt» wird zu `Unlimited`).
Solange sie gilt, bewahrt Exchange Online jede Nachricht auf, auch was die Person löscht. Die
Rückgabe hebt sie mit `disable-litigation-hold` wieder auf. Genehmigt wird jede Bestellung von
der Gruppe `{{compliance}}`. `LitigationHoldOwner` nennt den Auftrag; `RetentionComment` ist ein
neutraler Text und nicht der Grund, weil Microsoft den Kommentar als Hinweis beschreibt, den
Outlook der Person selbst zeigt. Der Grund bleibt als Personendatum in der Instanz.

Laut Microsofts Dokumentation braucht ein Postfach für Litigation Hold eine Lizenz mit Exchange
Online Plan 2 (in E3 und E5 enthalten) oder das Add-on Exchange Online Archiving, und eine
Aufbewahrung wirkt erst bis zu 60 Minuten nach dem Setzen.

## Die SharePoint-Teamwebsite

Das Produkt `m365-sharepoint-site` bietet, was ein Self-Service-Portal für SharePoint mit den
Kacheln *Invite*, *Site Quota*, *Site Owners*, *Delete* und *Undelete* und einem Formular
*Create a new Team Site* anbietet. Eine moderne, gruppenverbundene Teamwebsite ist eine
Microsoft-365-Gruppe (`groupTypes` Unified, ohne Team), deren Alias (`mailNickname`) die URL
der Site ist: `https://<SHAREPOINT_HOST>/sites/<alias>`. Microsoft legt die Site zur Gruppe
selbst an; der Prozess legt deshalb die Gruppe an und wartet auf die Site.

**Bestellformular** (`m365-sharepoint-site-bestellung`): Titel, URL, Sprache, anfängliches
Kontingent (1, 5, 10 oder 25 GB), Zweck, Sichtbarkeit, Besitzer und Stellvertretung (UPN). Das
Feld URL prüft beim Tippen, ob die URL noch frei ist (ADR-0519): bei
den Gruppen in Entra ID, bei den gelöschten Gruppen, die ihren Alias 30 Tage lang behalten,
bei SharePoint selbst unter `https://contoso.sharepoint.com/sites/<url>` und bei den
Exchange-Empfängern. Die Gruppe erhält die Mailadresse `<url>@<Domain>`, und ein
Funktionspostfach, ein Verteiler oder ein Kontakt kann diese Adresse schon tragen, ohne dass
Entra ID eine Gruppe dieses Namens kennt; die Quelle `recipients` des Exchange-Workers fragt
`Get-Recipient` nach dem Alias und nach dem Teil vor dem «@» jeder SMTP-Adresse. Der Wert wird
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
   `mailNickname eq '<alias>'`, `get-site` auf `<SHAREPOINT_HOST>:/sites/<alias>` und
   `run-get-cmdlet` mit `Get-Recipient -Filter "(Alias -eq '<alias>') -or (EmailAddresses -like
   'smtp:<alias>@*')"` beim Worker `ucc_exchange`. Ein Treffer irgendwo lehnt die Position mit
   «Die URL ist bereits vergeben» ab.
4. `create-group` (Unified, `mailEnabled` true, `securityEnabled` false, Sichtbarkeit aus dem
   Formular, Beschreibung mit Zweck und Katalog-Position; ohne `preferredLanguage`, siehe unten),
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

## Die Viva-Engage-Community

Das Produkt `m365-engage` legt eine Community in Viva Engage an. Eine Community steht auf einer
Microsoft-365-Gruppe, die Viva Engage zu ihr anlegt; Mitglieder und Admins führt der Prozess auf
dieser Gruppe.

**Bestellformular** (`m365-engage-bestellung`): Bereich, Bezeichnung, Beschreibung, Sichtbarkeit
(privat oder öffentlich) und Besitzerin oder Besitzer (UPN). Genehmigt wird von deren
vorgesetzter Person (`proc_m365_genehmigung_vorgesetzter`).

**Bereitstellung** (`m365.community.erstellen`, `engage-community.bpmn`):

1. Namensregel `M365Namensregel` mit dem Objekttyp `community`: Anzeigename
   «EC-<BEREICH> <Bezeichnung>», reservierte und zu lange Namen werden mit Begründung abgelehnt.
2. `get-user` für die Besitzerin oder den Besitzer.
3. Verbindliche Prüfung: `list-groups` mit `displayName eq '<Anzeigename>'`. Den Alias der Gruppe
   wählt Viva Engage, geprüft wird deshalb der Anzeigename. Ein Treffer lehnt die Position mit
   «Der Name ist bereits vergeben» ab.
4. `create-community` mit Anzeigename, Beschreibung samt Katalog-Position und Sichtbarkeit; die
   Person aus dem Formular wird erste Admin. Graph antwortet mit einem Vorgang, dem der Worker
   bis zu seiner Grenze folgt.
5. Ist die Community dann noch nicht fertig (`community.id` ist null), erhält
   `M365-Administration` die Aufgabe «Community in Viva Engage prüfen» und trägt die id der
   Gruppe ein; `get-group` prüft sie. Der Worker legt in diesem Fall nicht noch einmal an, damit
   keine zweite Community entsteht.
6. `done`.

**Aktionen:**

| Aktion | Formular | Ablauf | Ergebnis |
|---|---|---|---|
| Community löschen (`deprovision`) | – | Gruppe über den Anzeigenamen suchen, `delete-group` | `returned` |
| Person aufnehmen (`mitglied`) | `m365-engage-person` | Gruppe suchen, `get-user`, als Mitglied `add-group-member`, als Admin `add-group-owner` | `completed` |
| Person entfernen (`mitglied-weg`) | `m365-engage-person-weg` | Gruppe suchen, `get-user`, auf Wunsch zuerst `remove-group-owner`, dann `remove-group-member` | `completed` |

Rolle und «auch als Admin» sind nicht personenbezogene Felder; nur sie liest ein Gateway.

**Annahmen** (Microsofts Beschreibung, an keinem Tenant geprüft): Die Gruppe einer Community trägt
deren Anzeigenamen; Besitzer der Gruppe sind Admins der Community; und wer die Gruppe löscht,
löscht die Community mit.

## Die Genehmigung durch die vorgesetzte Person

Lizenz, Team, SharePoint-Teamwebsite, Viva-Engage-Community, Funktionspostfach und Postfachzugriff nennen als Genehmigungsart den Prozess
`proc_m365_genehmigung_vorgesetzter` (`genehmigung-vorgesetzter.bpmn`) und nicht die
eingebaute Art «superior». Der Grund: Die eingebaute Art fragt die vorgesetzte Person der
**bestellenden** Person und kennt dafür nur deren Atlas-Konto. Bestellt wird hier aber für
jemanden, den das Bestellformular nennt: `upn` (Lizenz), `besitzer` (Team,
SharePoint-Teamwebsite, Community, Funktionspostfach) oder `person` (Postfachzugriff). Diese Antworten erhält nur ein
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

## Eintritt und Austritt (HR-Ereignis)

Wer eintritt, braucht ein Konto, eine Lizenz mit Postfach und die Mitgliedschaften seiner
Organisationseinheit. Hier bestellt das niemand von Hand: Das HR-System meldet den Eintritt,
und der Prozess `hr-eintritt.bpmn` (`proc_m365_hr_eintritt`) legt das Konto in Entra ID an und
bestellt danach das **Eintrittspaket** `m365-eintritt` im Namen seiner Applikation
(Shop-Aufgabe im Modus `order`, [ADR-0520](../../docs/adr/0520-process-places-an-order.md)).
Meldet das HR-System den Austritt, sperrt derselbe Prozess das Konto und gibt das Paket zurück
(Shop-Aufgabe im Modus `command`, ADR-0429 §10), daneben die AD-Adminkonten der Person aus dem
Paket [`ad-dienste`](../ad-dienste/) (siehe [Kopplung an ad-dienste](#kopplung-an-ad-dienste-adminkonten)).

### Ablauf

1. **Eintritt gemeldet** (`hr.eintritt`). Ein Nachrichtenstart mit `singletonStart`: Solange eine
   Instanz mit derselben Personalnummer läuft, startet keine zweite.
2. **Lizenzvariante bestimmen.** E3 oder `standard` → `standard`, E5 oder `erweitert` →
   `erweitert`, F3 oder `frontline` → `frontline`, keine Angabe → `standard`. Eine andere Angabe
   legt der Prozess `M365-Administration` vor (Aufgabe «Lizenzvariante wählen»), statt still eine
   Lizenz zu wählen.
3. **Initialpasswort festlegen** (Aufgabe für `M365-Administration`). Die Person muss es bei der
   ersten Anmeldung ändern. Eine Aufgabe und kein erzeugtes Passwort, weil jemand es übergeben
   muss; ein Temporary Access Pass ist nicht modelliert.
4. **Belegte Namen lesen** (`list-users` mit `startswith(userPrincipalName, '<vorname.nachname>')`)
   und **Entra-Konto anlegen** (`create-user`). Namensregel: `vorname.nachname@<MAIL_DOMAIN>`,
   kleingeschrieben, Umlaute umschrieben (ä → ae), Akzente entfernt, Leerzeichen und andere
   Zeichen gestrichen; ist der Name belegt, der erste freie mit 2 bis 9 (`anna.mueller2`).
   Gesetzt werden `accountEnabled` true, Anzeige-, Vor- und Nachname, `employeeId` (Personalnummer),
   `department` (Organisationseinheit), `usageLocation` (`USAGE_LOCATION`) und das Passwort.
5. Daneben, ohne den Ablauf aufzuhalten: **Zugangsdaten übergeben** (Aufgabe für
   `M365-Administration` mit UPN, Initialpasswort, Eintrittsdatum und vorgesetzter Person).
6. **Auf das Atlas-Konto warten.** Die Bestellung braucht das Atlas-Konto der Person, und das legt
   die Kontenspiegelung aus Entra ID an (ADR-0332). Der Prozess merkt sich nach dem Anlegen die
   Revision der Spiegelung (`GET /api/v1/directory-sync`) und prüft alle 15 Minuten, bis sie um
   zwei weiter ist: Erst der zweite Lauf hat seine Änderungen sicher nach dem Anlegen gelesen.
   Nach 16 Versuchen (vier Stunden) prüft `M365-Administration` (Aufgabe «Atlas-Konto prüfen»).
7. **Atlas-Konto lesen** (`GET /api/v1/pending-work?principal=<Objekt-ID in Entra ID>`). Die Route
   löst eine Person über Konto-ID, Benutzernamen, Verzeichnis-ID oder Mailadresse auf und antwortet
   mit der Konto-ID; die Spiegelung trägt die Objekt-ID als Verzeichnis-ID ein, der Treffer ist
   also exakt. Fehlt das Konto, antwortet sie 404, und die Aufgabe wird zum Incident.
8. **Eintrittspaket bestellen** (Shop-Aufgabe im Modus `order`) für diese Konto-ID, mit den Antworten
   der Teile: `{"m365-eintritt-lizenz": {upn, lizenzvariante}, "m365-oe-mitgliedschaften": {upn, oe}}`.
   Besteller ist `system:application:beispiel-m365-dienste`. Lehnt die Bestellung ab
   (`SHOP_ORDER_REFUSED`), legt der Prozess sie `M365-Administration` vor (Aufgabe «Ablehnung
   prüfen»): erneut bestellen oder beenden.
9. **Austritt gemeldet** (`hr.austritt` mit derselben Personalnummer) → **Entra-Konto sperren**
   (`disable`) → nebeneinander **Eintrittspaket zurückgeben** und **AD-Adminkonto zurückgeben**
   (je eine Shop-Aufgabe im Modus `command`, `deprovision`). Lehnt die Bestellung eine der beiden
   Rückgaben ab (`SHOP_COMMAND_REFUSED`), legt der Prozess sie `M365-Administration` vor (Aufgabe
   «Paket von Hand zurückgeben» bzw. «Admin-Konto von Hand zurückgeben»), und der Austritt endet,
   wenn beide Zweige fertig sind. Gesperrt wird zuerst, damit keine abgelehnte oder hängende
   Rückgabe den Zugang offen hält; die Rückgaben laufen nebeneinander, damit eine Ablehnung, die
   auf eine Person wartet, die andere nicht aufhält.

### Wie das HR-System meldet

Beide Meldungen braucht ein API-Token mit der Rolle `operator` (Console → Administration → API
tokens, Scope `full`).

**Eintritt** über die Trigger-Route, nicht über `POST /api/v1/messages`:

```sh
curl -X POST "$SERVER/api/v1/processes/proc_m365_hr_eintritt/triggers/hr.eintritt" \
  -H "Authorization: Bearer $HR_TOKEN" -H 'Content-Type: application/json' -d '{
    "triggerId": "eintritt-P-4711",
    "source": "hr",
    "variables": {
      "personalnummer": "P-4711", "vorname": "Anna", "nachname": "Müller", "oe": "FIN",
      "eintrittsdatum": "2026-12-01", "variante": "E3", "vorgesetzter": "chef@contoso.ch"
    }}'
```

`personalnummer`, `vorname`, `nachname` und `oe` sind nötig; `eintrittsdatum`, `variante` und
`vorgesetzter` (UPN) sind freiwillig. Die Route verschlüsselt die deklarierten Personendaten,
bevor sie in den Verlauf kommen, und beantwortet eine wiederholte `triggerId` mit der ersten
Instanz (201 beim Start, 200 bei der Wiederholung, 409 solange eine Instanz für diese
Personalnummer läuft). **Eine Nachricht über `POST /api/v1/messages` kann den Eintritt nicht
starten:** Der Server verschlüsselt Personendaten nur dort, wo sie eintreten (Formular,
Worker-Ergebnis, Start über die API), und ein Name in der Nutzlast einer Nachricht wird zum
Incident («arrived in the clear»). So an einem lokalen Server beobachtet.

**Austritt** als Nachricht, ohne Personendaten:

```sh
curl -X POST "$SERVER/api/v1/messages" \
  -H "Authorization: Bearer $HR_TOKEN" -H 'Content-Type: application/json' -d '{
    "name": "hr.austritt", "correlationKey": "P-4711",
    "variables": {"personalnummer": "P-4711"},
    "timeToLiveMs": 604800000, "messageId": "austritt-P-4711"}'
```

`correlationKey` ist die Personalnummer. Mit `timeToLiveMs` hält der Server eine Meldung, die
eintrifft, bevor der Prozess auf sie wartet (etwa während er noch auf das Atlas-Konto wartet),
höchstens so lange, wie `--message-ttl-max` erlaubt; ohne geht sie verloren. Der Prozess handelt
beim Eintreffen: Das HR-System meldet den Austritt deshalb an dem Tag, an dem der Zugang enden soll.

### Das Paket und seine Teile

| Produkt | Kante | Genehmigung | Prozess |
|---|---|---|---|
| `m365-eintritt` (das Ganze) | – | keine | `eintritt-paket.bpmn`: meldet die Bereitstellung sofort als erledigt, gibt bei der Rückgabe die Teile zurück |
| `m365-eintritt-lizenz` | `composition` vom Paket | keine | `lizenz.bpmn` |
| `m365-oe-mitgliedschaften` | `composition` vom Paket, `requires` die Lizenz | keine | `oe-mitgliedschaften.bpmn` |

`m365-eintritt` nennt die Applikation `beispiel-m365-dienste` in `orderableBy` (der Prozess darf es
bestellen) und in `commandedBy` (er darf es zurückgeben); die beiden Teile nennen sie in
`commandedBy`, weil das Paket sie zurückgibt. Was dabei in Atlas nachgelesen wurde:

- **Varianten.** Eine Shop-Aufgabe im Modus `order` kann noch keine Variante nennen, und die
  Bestellung lehnt jedes Produkt mit Varianten ab, das keine genannt bekommt, auch ein integrales
  Teil. `m365-lizenz` mit seinen drei Varianten taugt deshalb nicht als Teil. `m365-eintritt-lizenz`
  ist dieselbe Lizenz **ohne Varianten**, gebunden an denselben Prozess `lizenz.bpmn` mit denselben
  Aktionen am Postfach; die Stufe kommt als Antwort `lizenzvariante` aus dem Bestellformular
  `m365-eintritt-lizenz-bestellung`. Die drei Entscheidungen in `lizenz.bpmn` lesen dazu
  `variantId` und, wo diese leer ist, `lizenzvariante`. Für `m365-lizenz` ändert sich nichts.
- **Genehmigung der Teile.** Jede Position eines Auftrags trägt die Genehmigungsregel ihres eigenen
  Produkts (`api/order/service.go`, `linesFor`); ein Teil erbt die Regel des Ganzen nicht. Als Teil
  hätte `m365-lizenz` also auf die vorgesetzte Person gewartet. Alle drei Produkte des Eintritts
  sind deshalb ohne Genehmigung: Der Eintrag im HR-System ist die Freigabe.
- **Rückgabe.** Eine Rückgabe gibt genau die Position zurück, um die gebeten wird; die Teile, die
  das Ganze trägt, bleiben im Bestand (`api/order/return.go` prüft nur die Kante `requires`, und
  ein integrales Teil lässt sich zurückgeben, nur nicht einzeln stornieren). `eintritt-paket.bpmn`
  gibt die Teile deshalb selbst zurück: zuerst die Mitgliedschaften, dann, sobald deren Rückgabe
  gemeldet ist (`GET …/lines/m365-oe-mitgliedschaften/outcomes`, alle 5 Minuten, höchstens zwei
  Stunden, danach eine Aufgabe für `M365-Administration`), die Lizenz, zuletzt das Paket selbst.
  Die Reihenfolge ist nötig, weil die Bestellung eine Lizenz nicht zurücknimmt, solange die
  Mitgliedschaften sie brauchen. So gibt jede Rückgabe des Pakets die Teile zurück, auch eine aus
  der Console.
- **Ein Paket ohne eigenes Zielsystem** braucht trotzdem einen Lebenszyklus mit `provision` und
  `deprovision`; der Katalog veröffentlicht kein Produkt ohne. `eintritt-paket.bpmn` meldet seine
  Position sofort als erledigt. Dass eine Meldung, die schneller ist als der Start-Vermerk der
  Bestellung, nicht mehr mit «running» überschrieben wird, behebt derselbe Änderungssatz in der
  Plattform (`order.MarkStarted`); ältere Server brauchten dafür eine Wartezeit im Modell.

**Mitgliedschaften** (`oe-mitgliedschaften.bpmn`): Die Entscheidung `M365OeMitgliedschaften`
bildet die Organisationseinheit auf drei Listen ab (Teams und Gruppen als Objekt-IDs, Verteiler
als Alias). Zuweisen: `get-user` für die Objekt-ID, je Team `add-team-member`, je Gruppe
`add-group-member`, und wenn Verteiler dabei sind, nach 30 Minuten (das Postfach entsteht erst nach
der Lizenz) je Verteiler `add-distribution-group-member`. Entziehen in umgekehrter Reihenfolge,
Teams mit `remove-group-member`. Eine Organisationseinheit, die die Tabelle nicht kennt, geht an
`M365-Administration` (Aufgabe «Organisationseinheit klären»). Die IDs in der Tabelle sind Beispiele.

### Kopplung an ad-dienste: Adminkonten

Wer austritt, verliert auch seine **AD-Adminkonten**. Die bestellt man nicht in diesem Paket,
sondern im Paket [`examples/ad-dienste`](../ad-dienste/) (Applikation `beispiel-ad-dienste`),
als Produkt `ad-admin-konto` mit der Aktion `deprovision`. Der Austritt gibt sie zurück mit:

```xml
<atlas:shopTask mode="command" product="ad-admin-konto" action="deprovision"
                recipient="=atlasKontoId" resultVariable="adRueckgabe" retries="3"/>
```

- **Adressiert über die Person**, nicht über Auftrag und Position
  ([ADR-0521](../../docs/adr/0521-command-by-recipient.md)): Der Prozess
  kennt das Atlas-Konto der Person aus dem Eintritt, aber nicht den Auftrag, aus dem ihr Adminkonto
  stammt, und den Bestand einer anderen Person darf nur lesen, wer `admin` ist (ADR-0312; ein
  API-Token ist das nie). Der Server sucht die Position im Bestand selbst. Der Bestand führt je
  Person und Produkt ein Recht, also gibt es höchstens eine Position; eine Mehrfachinstanz braucht
  es nicht.
- **Die Berechtigung liegt beim Produkt:** `ad-admin-konto` muss `beispiel-m365-dienste` in
  `commandedBy` nennen, und seine Aktion `deprovision` muss ein `operator` (oder `system`) auslösen
  dürfen (ADR-0429 §10). Das ist eine Einstellung im Katalog von `ad-dienste`; wer dieses Paket
  unter einem anderen Schlüssel installiert, ändert sie dort.
- **Ohne `ad-dienste` geht der Austritt weiter.** Ist das Paket nicht installiert oder hält die
  Person kein Adminkonto (oder nur ein übernommenes ohne Bestellung), endet die Aufgabe ohne etwas
  zu tun, mit `adRueckgabe` = `""`. Sonst steht dort die Kennung der Rückgabe; der Austritt wartet
  nicht darauf, dass sie im Active Directory ankommt.
- **Eine Ablehnung** (Produkt nennt die Applikation nicht, Aktion nur für den Kunden, Auftrag
  fehlt) wirft die Aufgabe als BPMN-Fehler `SHOP_COMMAND_REFUSED`; der Prozess fängt ihn und legt
  die Aufgabe «Admin-Konto von Hand zurückgeben» `M365-Administration` vor (Formular
  `m365-austritt-abgelehnt`). Ein Serverfehler bleibt eine Störung mit Wiederholungen und danach
  ein Incident. Die Rückgabe des Eintrittspakets fängt denselben Fehler auf dieselbe Weise.

### Personendaten

`vorname`, `nachname`, `vorgesetzter`, `initialpasswort`, die gelesenen Namen `belegteUpns` und das
angelegte Konto `konto` (mit der UPN) sind in `hr-eintritt.bpmn` deklariert und unter dem Schlüssel
der Personalnummer verschlüsselt. Die Namensregel steht deshalb nicht in einer Script-Task, sondern
in den Ausdrücken des Entra-Workers (Filter von `list-users`, Attribute von `create-user`, dort
zweimal: in `userPrincipalName` und `mailNickname`, die gleich bleiben müssen); die Kontosuche liest
die Objekt-ID in der Adresse des REST-Workers, die Antworten der Bestellung entstehen im Ausdruck
der Shop-Aufgabe. Die Gateways lesen nur `lizenzvariante`, Revisionen, Zähler und `entscheid`.
**`personalnummer` bleibt im Klartext:** Sie ist der Korrelationsschlüssel beider Meldungen, und
den wertet die Engine aus. Sie benennt die Person bei einem Löschbegehren (`atlas:dataSubject`,
`atlas:searchable`). Die Konto-ID in Atlas (`atlasKontoId`) ist wie `recipient` in jedem
Lebenszyklus kein Personendatum. In `oe-mitgliedschaften.bpmn` sind `upn` und `personKonto`
Personendaten der empfangenden Person.

### Voraussetzungen

- **Die Kontenspiegelung aus Entra ID** läuft (ADR-0332, z.B. stündlich). Ihr Intervall bestimmt,
  wie lange Schritt 6 dauert: Zwei Läufe nach dem Anlegen sind bei einem Stundentakt bis zu zwei
  Stunden. Ohne Spiegelung kommt kein Atlas-Konto, und nach vier Stunden geht die Aufgabe an
  `M365-Administration`. Die Route `/api/v1/directory-sync` gibt es nur auf einem Server mit
  Anmeldung.
- **Der Applikationsschlüssel** `beispiel-m365-dienste` (Schlüssel in `atlas.json`). Wer die
  Applikation unter einem anderen Schlüssel installiert, ändert `orderableBy` und `commandedBy` in
  `katalog.json`.
- **Der Katalog `cat-m365-hr`** ist veröffentlicht; seine Katalog-ID nennt die Shop-Aufgabe wörtlich.
- **Gruppe `M365-Administration`**, Secret `atlas` (API-Token mit der Rolle `operator`), Worker
  `ucc_entra` (`User.ReadWrite.All`, `Group.ReadWrite.All`, für Teams laut Microsoft
  `TeamMember.ReadWrite.All`) und `ucc_exchange`, Konstanten `MAIL_DOMAIN` (eine verifizierte Domain
  des Tenants, sonst nimmt Entra ID die UPN nicht an), `USAGE_LOCATION` und `ATLAS_API_BASE`.
- **Die Tabelle `M365OeMitgliedschaften`** mit den Teams, Gruppen und Verteilern des eigenen Tenants.
- **Für die Rückgabe der Adminkonten** (freiwillig): das Paket `examples/ad-dienste` installiert,
  sein Produkt `ad-admin-konto` nennt `beispiel-m365-dienste` in `commandedBy`. Ohne das Paket
  findet der Schritt nichts und geht weiter.

### Was geprüft ist und was nicht

Geprüft gegen einen lokalen Atlas-Server aus diesem Repository (Installation mit
`installieren.sh`, Katalog über eine angemeldete Sitzung, Wartezeiten der Prüfung auf Sekunden
verkürzt). Die Jobs der Entra- und Exchange-Worker hat die Prüfung über `POST /api/v1/jobs/activate`
und `…/complete` selbst beantwortet, die Kontenspiegelung über `POST /api/v1/directory-sync`:

- Start über die Trigger-Route, Personendaten verschlüsselt; ein zweiter Start mit derselben
  Personalnummer mit 409 abgelehnt; der Start über `POST /api/v1/messages` mit Namen in der Nutzlast
  als Incident (deshalb die Trigger-Route);
- die Namensregel mit Umlauten und einem belegten Namen (`anna.mueller2`), Filter und Attribute im
  Klartext beim Worker;
- das Warten auf zwei Läufe der Spiegelung, die Kontosuche über die Objekt-ID, die Bestellung im
  Namen der Applikation mit den Antworten beider Teile, Besteller
  `system:application:beispiel-m365-dienste`;
- die Bereitstellung aller drei Positionen: E5 und F3 über `lizenzvariante`, zwei Teams, eine
  Gruppe und zwei Verteiler, eine unbekannte Organisationseinheit und eine unbekannte Variante
  über die Aufgaben; der Bestand der Person mit allen drei Produkten, die Aktionen am Postfach für
  die Person sichtbar, die Rückgaben nicht; die Aktion «Postfachkontingent erhöhen» an
  `m365-eintritt-lizenz`;
- der Austritt: Sperren, Rückgabe des Pakets, Entfernen der Mitgliedschaften, danach Entzug der
  Lizenz mit der SKU aus `lizenzvariante`; alle drei Positionen `returned`.

Nicht geprüft: irgendein Schritt gegen einen echten Tenant (`create-user` mit `passwordProfile`,
`startswith` auf `userPrincipalName`, `add-team-member` für ein eben lizenziertes Konto, die
Zeit, bis Exchange Online das Postfach anlegt), der Pfad `SHOP_ORDER_REFUSED` mit der Aufgabe
«Ablehnung prüfen», die Aufgaben nach Ablauf der Wartezeiten und `installieren.ps1`. Die Rückgabe
der AD-Adminkonten und die beiden Pfade `SHOP_COMMAND_REFUSED` sind nur so weit geprüft, wie
`go test ./examples/... ./api/layout/...` reicht (das Modell lässt sich übersetzen und ist sauber
gezeichnet), nicht gegen einen laufenden Server mit installiertem `ad-dienste`.

### Bekannte Grenzen

- **Keine Varianten in der Bestellung des Prozesses.** Deshalb die eigene Lizenz ohne Varianten
  (siehe oben). Wer später ins Paket ein Produkt mit Varianten aufnimmt, macht jede Bestellung
  unmöglich (`SHOP_ORDER_REFUSED`).
- **Das Atlas-Konto muss bestehen**, bevor bestellt wird; ohne Kontenspiegelung wartet der Eintritt.
- **Versetzung nicht modelliert.** Ändert die Organisationseinheit, bleiben die Mitgliedschaften
  der alten. Ein Weg wäre eine dritte Meldung `hr.versetzung`, die die Position
  `m365-oe-mitgliedschaften` zurückgibt und für die neue Einheit eine neue bestellt.
- **Das Konto ist sofort aktiv**, nicht erst am Eintrittsdatum, und das Initialpasswort wählt
  eine Person; die vorgesetzte Person setzt der Prozess nicht in Entra ID (der Entra-Worker hat
  dafür keine Operation).
- **Höchstens neun Personen mit demselben Grundnamen**; ist auch `vorname.nachname9` belegt, lehnt
  Entra ID das Anlegen ab, und die Aufgabe wird zum Incident. Ein Zeichen ausserhalb der
  Umschreibung (etwa ł, ø) fällt aus dem Namen.
- **Massgebend ist die Tabelle beim Entziehen**, nicht die bei der Zuweisung: Wurde
  `M365OeMitgliedschaften` dazwischen geändert, entfernt die Rückgabe die heutigen Listen. Wer schon
  kein Mitglied mehr ist, lässt die Entfernung als Incident enden.
- **Eine Paket-Lizenz neben einer bestellten.** Wer `m365-eintritt-lizenz` hält, kann im Shop
  zusätzlich `m365-lizenz` bestellen; die Bestellung kennt die beiden nicht als dasselbe Recht.
- **Wird der Eintritt nach einer Ablehnung beendet**, bleibt das Konto in Entra ID bestehen.

## Die Entscheidungen

| Modell (Handle) | Entscheidung | Wofür |
|---|---|---|
| `m365-namensregel` | `M365Namensregel` | Namenskonvention: Präfix je Objekttyp (TM, FP, VL, RM, GE, CQ, AA, EC), Sperrliste reservierter Bezeichnungen, Längengrenze, Begründung jeder Ablehnung; für SharePoint-Sites (Objekttyp `site`) die Form der URL statt eines Präfixes |
| `m365-site-kontingent` | `M365SiteKontingent` | gewünschtes Speicherkontingent einer Site und ihre Belegung → erlaubt ja/nein, Prüfung durch `M365-SharePoint` ja/nein (bis 25 GB ohne, bis 250 GB mit Prüfung, darüber und unter der Belegung abgelehnt) |
| `m365-postfachkontingent` | `M365Postfachkontingent` | gewünschte Stufe (50 oder 100 GB) und Lizenzvariante → erlaubt ja/nein, Prüfung durch `M365-Administration` ja/nein, die drei Grenzen für `Set-Mailbox` (F3 abgelehnt, 50 GB ohne, 100 GB mit Prüfung, alles andere abgelehnt) |
| `m365-lizenzprofil` | `M365Lizenzprofil` | Variante, Zusatzlizenz der Telefonie oder Lizenz eines Ressourcenkontos → SKU-ID, dazu für die drei Varianten die Lizenzgruppe in Entra ID (`entraGruppe`) und im AD (`adGruppe`, Platzhalter); ohne Auffangregel, eine unbekannte Variante ergibt nichts, und der nächste Schritt wird zum Incident |
| `m365-postfachzugriff` | `M365PostfachzugriffFreigabe` | Postfachtyp und Zugriffsart → Zusatzfreigabe durch die Informationssicherheit ja/nein |
| `m365-telefonie` | `M365TelefonieSprachregion` | Sprachregion → Sprache und Zeitzone der Teams-Telefonie |
| `m365-verteiler-zuordnung` | `M365VerteilerZuordnung` | öffentliche Auswahl → interne Verteilerliste und Prüfpflicht |
| `m365-oe-mitgliedschaften` | `M365OeMitgliedschaften` | Organisationseinheit → Teams und Gruppen (Objekt-IDs), Verteiler (Alias), unbekannt ja/nein (vier Beispiele, Auffangregel mit leeren Listen) |

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
  `CustomAttribute4` = verantwortliche Person (beim Raum und beim Gerät: Standort; das Gerät
  trägt in `CustomAttribute5` zusätzlich die Geräteart). Rückgabe und
  Änderungen finden das Objekt über diese Kennzeichnung, nicht über den Namen.
- **Governance-Prüfung.** `governance-pruefung.bpmn` sucht wöchentlich Funktionspostfächer
  und Verteiler ohne Kennzeichnung sowie Microsoft-365-Gruppen ausserhalb der Namensregel
  (ausgenommen die gekennzeichneten Gruppen der SharePoint-Teamwebsites und die Gruppen der
  Communities, deren Anzeigename mit «EC-» der Namensregel folgt) und legt den Befund `M365-Administration` vor. Der Befund entzieht nichts; gehandelt wird
  durch Menschen.
- **Weiterleitung nur in die eigene Domain.** Eine Weiterleitung an eine fremde Domain lehnt der
  Prozess mit Begründung ab, bevor Exchange etwas erfährt (siehe «Das Postfach an der Lizenz»).
- **Aufbewahrung nur mit Compliance.** Eine Litigation Hold entsteht nur nach der Zustimmung der
  Gruppe `{{compliance}}`, und ihr Grund wird der betroffenen Person nicht angezeigt.
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
     `TeamSettings.ReadWrite.All` (an einem Tenant nicht geprüft). Für die
     Viva-Engage-Community zusätzlich `Community.ReadWrite.All`, und der Tenant muss in Viva
     Engage im nativen Modus (Native Mode) laufen; beides ist Microsofts Dokumentation und an
     keinem Tenant geprüft.
   - `ucc_exchange` (Microsoft Exchange): `Exchange.ManageAsApp` und eine Exchange-Rolle
     (z.B. *Exchange Recipient Administrator*). Die Rolle muss die Cmdlets der Prozesse
     decken; welche Verwaltungsrolle ein Cmdlet verlangt, sagt
     `Get-ManagementRole -Cmdlet <Name>`:
     - Weiterleitung, Mailadressen, Kontingent, Gerätepostfach (`Set-Mailbox`, `New-Mailbox`,
       `Set-CalendarProcessing`) und die Ordnerberechtigungen der Kalenderstellvertretung
       (`Add-`/`Remove-MailboxFolderPermission`, `Get-MailboxFolderStatistics`) gehören nach
       Microsofts Dokumentation zur Rolle *Mail Recipients*, die *Exchange Recipient
       Administrator* (Rollengruppe *Recipient Management*) enthält.
     - **Litigation Hold** (`Set-Mailbox -LitigationHoldEnabled`) verlangt zusätzlich die Rolle
       *Legal Hold*; Microsoft weist sie Rollengruppen wie *Compliance Management*, *Discovery
       Management* und *Organization Management* zu. Welche Gruppe sie im eigenen Tenant
       trägt, zeigt `Get-ManagementRoleAssignment -Role "Legal Hold"`.
     - Beides ist Microsofts Dokumentation und an keinem Tenant geprüft.
   - Für die Lizenz über Gruppen (`LIZENZ_MODUS`, siehe «Lizenzen: direkt oder über Gruppen»):
     im Modus `entra-gruppe` am Worker `ucc_entra` `GroupMember.ReadWrite.All` (in
     `Group.ReadWrite.All` enthalten); im Modus `ad-gruppe` ein Worker `ad_verzeichnis` (Worker-Typ
     AD) mit Leserecht auf die Konten unter `AD_DOMAIN_DN` und Schreibrecht auf `member` an den
     Gruppen in `AD_LIZENZ_OU`. Keine Berechtigung auf Lizenzen.
   - `ucc_teams` (Microsoft Teams Phone): Rolle *Teams Administrator* und
     `User.ReadWrite.All`; PowerShell 7 mit dem Modul MicrosoftTeams auf dem Worker-Host.
   - Für die SharePoint-Teamwebsite zusätzlich: am Worker `ucc_entra` `User.Invite.All`
     (Gäste einladen; `Group.ReadWrite.All` aus der ersten Zeile deckt Anlegen, Löschen und
     Wiederherstellen der Gruppe und das Lesen gelöschter Gruppen ab), und ein Worker
     `ucc_sharepoint` (Microsoft SharePoint) mit `Sites.ReadWrite.All` oder mit
     `Sites.Selected` und Schreibrecht auf der Inventar-Site, dazu `Sites.Read.All` für
     `get-site` auf beliebigen Sites (Existenz und Belegung).
   - **Live-Prüfung der URL** im Bestellformular: Bei den drei Workern `ucc_entra`,
     `ucc_sharepoint` und `ucc_exchange` in der Console den Schalter «formLookups»
     (Formular-Abfragen erlauben) einschalten. `ucc_exchange` ist dabei, weil die Gruppe der Site
     die Adresse `<url>@<Domain>` erhält und ein Funktionspostfach, ein Verteiler oder ein
     Kontakt sie schon tragen kann. Der SharePoint-Worker muss dafür als Worker Instance laufen
     (beaufsichtigt oder extern), nicht im Server-Prozess. Fehlt eines davon, kann das Formular nur nicht
     vorwarnen; bestellt werden kann trotzdem, und die Bereitstellung prüft wie immer.
2. **Secret `atlas`** im Vault: ein API-Token mit der Rolle `operator`. Damit melden die
   Prozesse ihre Positionen an die Bestellung. Am einfachsten unter **Console → Administration → API tokens**:
   Scope `full`, «Direkt im Vault ablegen» angekreuzt, Name `atlas`. Das Geheimnis verlässt
   den Server dann nie.
3. **Für die Genehmigung durch die vorgesetzte Person** (Lizenz, Team, SharePoint-Teamwebsite,
   Community, Funktionspostfach, Postfachzugriff):
   - der Worker `ucc_entra` aus Punkt 1; er liest den Vorgesetzten mit `User.Read.All`,
     das in `User.ReadWrite.All` enthalten ist;
   - Atlas-Konten der vorgesetzten Personen, am einfachsten aus der Kontenspiegelung von
     Entra ID; wer kein Konto hat, wird durch die Ersatzgruppe vertreten;
   - der Vault des Servers (Vorgabe), weil der Prozess Personendaten deklariert;
   - für die Benachrichtigung ein Mail-Worker `mail`. Fehlt er, bleibt nur die Nachricht
     liegen; die Genehmigung läuft.
4. **Gruppen** in Atlas: die beiden Zielgruppen, `M365-Telefonie`, `M365-Administration`,
   `M365-SharePoint`, `Informationssicherheit`, die Gruppe des Facility Managements und die der
   Compliance. Die
   Aufgaben der Prozesse adressieren `M365-Telefonie`, `M365-Administration`,
   `M365-SharePoint` und `Informationssicherheit` über ihren Namen.
5. **Applikationskonstanten** (Console → Applikation → Constants), nach dem Import:

   | Konstante | Fallback | Bedeutung |
   |---|---|---|
   | `MAIL_DOMAIN` | `contoso.ch` | Domain der Adressen von Funktionspostfächern, Verteilern, Räumen, Geräten und Ressourcenkonten, der zusätzlichen Mailadressen und die einzige Domain, an die weitergeleitet werden darf |
   | `USAGE_LOCATION` | `CH` | Nutzungsort, ohne den Graph keine Lizenz zuweist |
   | `PARTNER_UPN_DOMAIN` | `contoso.onmicrosoft.com` | Domain der Partnerkonten |
   | `ATLAS_API_BASE` | `http://127.0.0.1:8080` | Adresse, unter der die Prozesse Atlas selbst erreichen, wenn eine Rückgabe oder Aktion sie nicht mitbringt |
   | `NAME_PREFIX` | `""` (Typ **json**) | Präfix vor jedem Namen und Alias, den die Namensregel bildet, vor UPN und Anzeigename eines Partnerkontos (so finden es auch die Aufräumskripte der Testumgebung, die nach dem Anzeigenamen suchen) und als Grenze der Governance-Prüfung. Für einen geteilten Tenant, z.B. `"atlas-test-58o4-"` (mit Anführungszeichen: der Typ ist json, weil ein leerer Text kein Fallback einer gebundenen Konstante sein kann) |
   | `SHAREPOINT_HOST` | `contoso.sharepoint.com` | SharePoint-Host des Tenants; die Sites liegen unter `https://<SHAREPOINT_HOST>/sites/<alias>` |
   | `SITE_INVENTAR` | `contoso.sharepoint.com:/sites/m365-governance` | Site, die die Liste «Site-Inventar» trägt (Host und Pfad, eine Site-ID oder die URL) |
   | `LIZENZ_MODUS` | `direkt` | Wie `lizenz.bpmn` die Lizenz vergibt: `direkt` (`assign-license`), `entra-gruppe` (Mitgliedschaft in einer Cloud-Gruppe) oder `ad-gruppe` (Mitgliedschaft in einer Gruppe im lokalen AD, die Entra Connect synchronisiert); siehe «Lizenzen: direkt oder über Gruppen» |
   | `LIZENZ_PRUEFINTERVALL` | `PT10M` | In den Gruppenmodi: wie lange der Prozess zwischen zwei Prüfungen wartet, ob die Lizenz aktiv ist (ISO-8601-Dauer) |
   | `LIZENZ_PRUEFUNGEN` | `18` | In den Gruppenmodi: wie oft er prüft, bevor `M365-Administration` die Aufgabe «Lizenz klären» erhält (18 × 10 Minuten = drei Stunden) |
   | `AD_DOMAIN_DN` | `DC=contoso,DC=ch` | Im Modus `ad-gruppe`: wo im AD das Konto der Person gesucht wird |
   | `AD_LIZENZ_OU` | `OU=Lizenzen,OU=Gruppen,DC=contoso,DC=ch` | Im Modus `ad-gruppe`: die OU der Lizenzgruppen; der DN einer Gruppe ist `CN=<adGruppe>,<AD_LIZENZ_OU>` |
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

1. lädt die acht Entscheidungsmodelle unter ihren Handles hoch
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

Ganz ohne Skript geht es auch in der Console: die acht `.dmn`-Dateien im Modeler unter den
Handles `m365-lizenzprofil`, `m365-namensregel`, `m365-oe-mitgliedschaften`, `m365-postfachkontingent`,
`m365-postfachzugriff`, `m365-site-kontingent`, `m365-telefonie` und
`m365-verteiler-zuordnung` hochladen, die Applikation über **Import source** (ein
`.tar.gz` mit `atlas.json` und den genannten Dateien) importieren und veröffentlichen,
dann `katalog.json` mit den Gruppen-IDs anstelle der fünf Platzhalter unter
**Catalogue → Import** hochladen.

Bezieht die Zielinstanz ihre Entscheidungen aus einem temis-Dienst
(`ATLAS_DMN_RESOLVER_URL`), werden die acht Modelle dort unter denselben Namen angelegt,
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

**Gegen einen echten Tenant geprüft (2026-11-02 und 2026-11-03)**, geteilter Tenant mit `NAME_PREFIX`,
Exchange-Worker in einer Rollengruppe mit Schreibbereich (`deploy/opentofu/m365-devenv`,
`shared_tenant = true`), über die Shop-Oberfläche bestellt:

- **Verteiler** bestellt: Namensregel mit Präfix, `New-DistributionGroup` im Schreibbereich,
  Herkunftskennzeichnung `CustomAttribute1–4`, «Verwaltet von» aus der Personensuche.
- **Funktionspostfach** bestellt und zurückgegeben (gelöscht), mit Genehmigung durch die
  vorgesetzte Person: `get-user-manager` gegen Graph, Atlas-Konto der vorgesetzten Person aus
  der Entra-Anmeldung, Mail an sie über RBAC for Applications.
- **Team** bestellt und zurückgegeben (archiviert).
- **Verteiler** zurückgegeben (`Remove-DistributionGroup` mit `BypassSecurityGroupManagerCheck`,
  nachdem die Rolle *Security Group Creation and Membership* in Exchange wirksam war).
- **Postfachzugriff** erteilt und entzogen: persönliches Postfach, Vollzugriff, die Regel
  `m365-postfachzugriff` verlangte die Zusatzfreigabe, die Informationssicherheit gab frei,
  `Add-MailboxPermission` bzw. `Remove-MailboxPermission` setzten und entfernten `FullAccess`;
  die Begründung liegt als personenbezogene Variable verschlüsselt.
- **Sitzungszimmer** bestellt und zurückgegeben (Genehmigung durch die Rolle Facility-Management): Raumpostfach
  mit Präfix `RM`, `ResourceCapacity`, Buchungsregeln `AutoAccept`, keine Überschneidungen,
  180 Tage, Herkunftskennzeichnung mit dem Standort; die Rückgabe findet es über die
  Kennzeichnung und entfernt es.
- **Lizenz** bestellt (Variante «erweitert»; im Testtenant gibt es nur eine SKU, ein Wechsel
  zwischen Lizenzen war daher nicht prüfbar) und die Aktion **Abwesenheit** an der Bestellung
  (`Set-MailboxAutoReplyConfiguration`, geplant, intern und extern).
- **Partnerzugang** über den öffentlichen Link beantragt, von `M365-Administration` abgelehnt
  bzw. freigegeben: Mitgliedskonto mit Präfix in UPN und Anzeigename, `employeeType` Partner,
  Firma, Initialpasswort mit Änderung bei der ersten Anmeldung; Namen und Firma ohne
  Leerzeichen am Rand. Die Sperre nach Ablauf (frühestens 90 Tage) ist nicht abgewartet.
- **Governance-Prüfung** von Hand gestartet: keine Postfächer und Verteiler ohne
  Kennzeichnung, zwei Microsoft-365-Gruppen der Testumgebung ausserhalb der Namensregel,
  Befund als Aufgabe an `M365-Administration`.
- **Personensuche** in den Bestellformularen (ADR-0473, Amendment 2026-11-02), fest gefiltert
  auf die Abteilung der Testpersonen: echte Konten des Tenants erscheinen nicht.

Dabei korrigiert: das Team wartet die Replikation **vor** dem Besitzer ab (`add-group-owner`
23 s nach `create-group` antwortete 404) und noch einmal zwei Minuten **nach** dem Besitzer
(`create-team` eine Sekunde nach `add-group-owner` antwortete 400 «must have one or more owners»,
2026-11-04); der Entra-Worker legt Teams mit `POST /teams` an
(`PUT /groups/{id}/team` antwortete einer App 403 «StampTenantRegionRequest»); die
Verteilerliste entsteht ohne `-Type`; der Exchange-Worker liest Antworten unkomprimiert und
wiederholt ein `Set-`/`Add-`, das ein eben angelegtes Objekt noch nicht findet, und wartet
auf ein Cmdlet bis zu zwei Minuten statt zehn Sekunden (`Add-MailboxPermission` auf ein eben
angelegtes Funktionspostfach brauchte länger und wirkte trotzdem); Personen werden
auch über Vor- und Nachname gefunden.

Gefunden und korrigiert: Die Governance-Prüfung zählte keine Teams, solange der Vergleich mit
der Namensregel in einem Filter stand (`gruppen[not(matches(mailNickname, "^" +
application.NAME_PREFIX + …))]`): In einem Filter über Kontexte liest ein Name, der kein
Schlüssel des Elements ist, nicht die Variable der Instanz. Der Vergleich steht jetzt in einem
`for`.

Gefunden, nicht korrigiert: Findet der Server keinen Empfänger für eine Mail (eine Gruppe ohne
Mitglieder mit Adresse), verschluckt er den Fehler beim Auflösen (`api/handlers.go`,
`mail.Resolve`) und der Worker meldet irreführend «is this server offloading the mail kind?».
Die Mailadresse eines Teams ist die Standard-Domain des Tenants, nicht `MAIL_DOMAIN`.

Wer sich erst nach der Installation zum ersten Mal anmeldet, ist in keiner Zielgruppe und
sieht keinen Katalog; `install-m365-dienste.py` erneut ausführen nimmt die inzwischen
angemeldeten Testpersonen auf (oder sie von Hand in die Gruppen aufnehmen).

Nicht geprüft: die Sperre eines Partnerkontos nach Ablauf, die übrigen Aktionen an Bestellungen,
die Rückgabe einer Lizenz, die Eskalation nach sieben Tagen,
Teams Phone und `installieren.ps1`.

**SharePoint-Teamwebsite.** Geprüft ist nur ohne Server und ohne Tenant: Der Prozess
kompiliert, das Katalogdokument veröffentlicht sich mit dem Produkt und allen sieben
Aktionen, der Block `atlasAvailability` des Bestellformulars wird vom Parser des Servers
angenommen, das Diagramm hält die Layout-Regeln ein, und beide Entscheidungen
(`M365Namensregel` mit dem Objekttyp `site` und die bisherigen Objekttypen unverändert,
`M365SiteKontingent` mit allen Regeln) wurden lokal ausgewertet. **Nichts davon lief gegen
einen echten Tenant**, und keine Bestellung lief durch den Shop: weder `create-group`,
`invite-guest`, `restore-group` und `list-deleted-groups` gegen Microsoft Graph noch
`get-site` und die Inventarliste gegen SharePoint, noch die Live-Prüfung im Formular.

**Gerät, Aktionen am Postfach, Aufbewahrung und die Exchange-Prüfung der Site-URL.** Geprüft
ist nur ohne Server und ohne Tenant: Die Prozesse kompilieren (auch gegen die Regel, dass keine
Bedingung ein Personendatum liest), das Katalogdokument veröffentlicht sich mit den beiden neuen
Produkten und den sieben neuen Aktionen ohne Warnung zu Personendaten, die Diagramme halten die
Layout-Regeln ein, `M365Postfachkontingent` und die neuen Regeln von `M365Namensregel` wurden
lokal mit allen Regeln ausgewertet, und der Ausdruck, der die Identität des Kalenders bildet
(`<upn>:\<Name>`), wurde lokal ausgewertet. **Nichts davon lief gegen Exchange Online**, und
keine Bestellung lief durch den Shop. Insbesondere nicht geprüft:

- ob der REST-Endpunkt von Exchange Online, über den der Worker Exchange Online erreicht,
  `Get-MailboxFolderStatistics` und `Get-Recipient` über `run-get-cmdlet` bedient und in welcher
  Form er die Ordner zurückgibt (`FolderType`, `Name`);
- in welcher Form `get-mailbox-statistics` `TotalItemSize` liefert (das Prüfformular zeigt den
  Wert so, wie er kommt);
- dass Exchange Online ein Kontingent über der Grenze der Lizenz abweist (Microsofts
  Dokumentation);
- `enable-litigation-hold` mit `RetentionComment` und die Rolle *Legal Hold*;
- dass Outlook `RetentionComment` der Person zeigt (Microsofts Dokumentation).

**Viva-Engage-Community.** Ebenso nur ohne Server und ohne Tenant: Der Prozess kompiliert gegen die
Operation `create-community` des Entra-Workers, das Katalogdokument veröffentlicht sich mit dem
Produkt in beiden Katalogen, das Diagramm hält die Layout-Regeln ein, und die Regel `community`
von `M365Namensregel` wurde lokal ausgewertet. Nicht geprüft: `create-community` gegen Microsoft
Graph (der Vorgang, seine Dauer, das Ergebnis mit `groupId`), die Annahmen oben und die
Governance-Prüfung, die Gruppen mit einem Anzeigenamen «EC-…» nicht mehr meldet.


## Annahmen, die vor dem Einsatz zu prüfen sind

- **Lizenzgruppen** in `m365-lizenzprofil.dmn` (`entraGruppe`, `adGruppe`) sind Platzhalter. Einzutragen
  sind die Gruppen, denen in Entra ID die SKU derselben Zeile zugewiesen ist; gebraucht nur, wenn
  `LIZENZ_MODUS` nicht `direkt` ist.
- **Nutzungsort im Modus `ad-gruppe`.** Der Prozess setzt `usageLocation` in Entra ID. Wo Entra Connect
  den Nutzungsort aus dem AD synchronisiert (etwa aus `msExchUsageLocation`), gilt der Wert aus dem AD;
  ob Graph das Schreiben dann annimmt, ist nicht geprüft.

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

- **Teams, Communities und Ressourcenkonten werden über ihren Namen wiedergefunden.** Ändert sich ein
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
- **Sprache der Site.** Microsoft Graph nimmt `preferredLanguage` beim Anlegen einer Gruppe
  nicht an (HTTP 400 «Property preferredLanguage should not be set», am Tenant gesehen). Die
  Gruppe entsteht deshalb ohne Sprache, und die Site erhält die Standardsprache des Tenants.
  Die bestellte Sprache (`de-CH`, `en-US`, `fr-CH`, `it-CH`) steht nur im Inventar. Die
  Sprache einer Site lässt sich nach ihrer Erstellung nicht mehr ändern; wer sie verbindlich
  braucht, legt die Site anders an (etwa über die SharePoint-Schnittstelle mit `lcid`).
- **Sites in der Governance-Prüfung.** Der Alias einer Site ist ihre frei gewählte URL und
  folgt keinem Präfix. Die Gruppe einer Site, die dieser Katalog angelegt hat, trägt deshalb in
  ihrer Beschreibung «| SharePoint-Site | Katalog-Position …», und die Governance-Prüfung zählt
  so gekennzeichnete Gruppen nicht als Abweichung. Die Kennzeichnung steht in einem Feld, das
  Besitzerinnen und Besitzer ändern können; sie ist ein Hinweis, kein Nachweis. Massgebend
  bleibt das Site-Inventar.
- **Community: nur mit Viva Engage im nativen Modus.** Ein Tenant, der Viva Engage nicht im
  nativen Modus betreibt, kennt Communities auf Microsoft-365-Gruppen nicht; `create-community`
  scheitert dann als Incident.
- **Community: der Anzeigename ist die Identität.** Die Aktionen finden die Gruppe über
  `displayName`. Benennt jemand die Community in Viva Engage um, finden Rückgabe und Aktionen sie
  nicht mehr; die Aufnahme einer Person scheitert dann als Incident, die Rückgabe meldet
  `returned`, ohne etwas zu löschen. Eine Community, die nach dem Vorgang von Hand angelegt
  wurde, muss denselben Anzeigenamen tragen.
- **Community löschen löscht die Gruppe.** Dass Microsoft die Community mit ihrer Gruppe
  löscht, ist Microsofts Beschreibung des Löschens einer Gruppe und nicht geprüft. Die Gruppe
  liegt danach 30 Tage im Papierkorb.
- **Die Exchange-Prüfung der Site-URL sieht keine Personen.** Damit das Gateway zählen kann,
  ist das Ergebnis von `Get-Recipient` nicht als Personendatum deklariert. Damit es nie die
  Daten einer Person enthält, schliesst der Filter Empfänger aus, die für Personen stehen
  (`UserMailbox`, `MailUser`, `GuestMailUser`, `MailContact`). Hält das Postfach oder der Kontakt
  einer Person den Alias der bestellten URL, lehnt erst `create-group` die doppelte Adresse ab;
  der Job wiederholt sich und endet als Incident. Die Live-Prüfung im Bestellformular fragt alle
  Empfänger, speichert nichts und erkennt diesen Fall in der Regel schon bei der Eingabe.
- **Weiterleitung: genau die eigene Domain.** Verglichen wird die Domain mit `MAIL_DOMAIN`, ohne
  Gross- und Kleinschreibung. Eine Subdomain oder eine zweite akzeptierte Domain des Tenants gilt
  als fremd und wird abgelehnt; wer mehrere Domains zulassen will, ändert die Script-Task «Ziel
  prüfen». Ob die Adresse im Tenant besteht, prüft der Prozess nicht: Eine Weiterleitung an eine
  unbekannte Adresse der eigenen Domain nimmt Exchange an, und die Mails kommen nicht an.
- **Belegte Mailadresse, bestehende Kalenderberechtigung.** Trägt schon ein anderes Objekt die
  gewünschte Mailadresse, oder hat die Stellvertretung auf dem Kalender schon eine Berechtigung,
  weist Exchange den Aufruf ab, und die Aufgabe wird zum Incident. Findet `Get-MailboxFolderStatistics`
  keinen Ordner mit `FolderType` Calendar, fehlt die Identität, und auch das wird zum Incident.
- **Kontingent: 50 GB ist mit E3 oder E5 keine Erhöhung.** Exchange Online gibt einem solchen
  Postfach von sich aus 100 GB; die Stufe 50 GB senkt dann das Kontingent. Der Prozess vergleicht
  nicht mit dem bisherigen Kontingent (das stünde in `get-mailbox`, nicht in der Statistik).
- **Aufbewahrung: Personendaten unter dem Schlüssel der empfangenden Person.** Die Bestellung
  verschlüsselt `upn` und `aufbewahrungGrund` unter dem Schlüssel der Person, die das Recht im
  Bestand hält (meist jemand aus der Compliance), nicht unter dem der betroffenen Person. Ein
  Löschbegehren der betroffenen Person erreicht diese Werte deshalb nicht von selbst.
- **Keine Herkunftskennzeichnung auf persönlichen Postfächern.** Die Aktionen an der Lizenz und
  die Aufbewahrung ändern das Postfach einer Person und schreiben keine `CustomAttribute`: Diese
  Felder gehören dort oft schon der Personalverwaltung oder der Synchronisation.
- **Gäste** lassen sich nur einladen, wenn die Einstellungen des Tenants für externe
  Zusammenarbeit (Entra ID) und für das Teilen (SharePoint) es erlauben; sonst endet die Aktion
  mit einem Incident.

## Was die Tests prüfen

`go test ./examples` prüft ohne Server: alle Prozesse kompilieren (und keiner liest ein
Personendatum in einem Ausdruck, den die Engine auswertet), das Katalogdokument
veröffentlicht sich, Lebenszyklus-Bindungen und Shop-Aufgaben passen zu den Prozessen,
jedes Formular ist vorhanden, jedes Bestellformular liefert nur Text (keine Zahlenfelder,
Checkboxen oder Mehrfachauswahlen), jede Antwort eines Bestellformulars ist als Personendatum
deklariert oder als nicht personenbezogen markiert, jeder Platzhalter wird gefragt, und
`atlas.json` nennt genau die Prozesse und Formulare dieses Verzeichnisses.
`go test ./api/layout` hält alle Diagramme an die Layout-Regeln (keine Überlappungen,
keine Kreuzungen, Beschriftungen frei).

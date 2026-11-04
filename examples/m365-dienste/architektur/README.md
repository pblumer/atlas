# Architektur des Beispiels m365-dienste

Dieses Verzeichnis beschreibt, *was* die Prozesse des Beispiels bewegen und *wofuer* sie da sind, damit die Dokumente, die Atlas aus einem Release erzeugt (Architektur-Skizze in SVG und ArchiMate, ADR-0471 und ADR-0495), alle Schichten zeigen: Strategie (Capabilities, Wertstroeme), Geschaeft, Applikation, Daten (Datenobjekte, Speicher, Klassen) und Technologie.

| Datei | Inhalt | Wohin |
|---|---|---|
| `informationsmodell.json` | Klassen, Lebenszyklen, Beziehungen, Datenspeicher (ADR-0230, ADR-0259) | Informationsmodell der Applikation (`POST /api/v1/infomodel/import`) |
| `capabilities.json` | Capabilities und Wertstroeme (ADR-0305) | serverweit (`/api/v1/capabilities`, `/api/v1/value-streams`) |
| `einspielen.py` | legt beides an oder ersetzt es; `installieren.sh`/`.ps1` rufen es vor dem Veroeffentlichen auf | |

In den Prozessen (`*.bpmn`) stehen dazu typisierte Datenobjekte und Datenspeicher (ADR-0053): Jeder Schritt, der ein Geschaeftsobjekt anlegt, aendert oder zurueckgibt, schreibt dessen Datenzustand ueber eine Datenausgabe-Assoziation **ohne Zuweisung**. Das ist nach ADR-0058 eine reine Zustandsaenderung: Sie setzt keine Prozessvariable und aendert den Ablauf nicht. Lesende Zugriffe sind bewusst nicht gezeichnet, weil eine Dateneingabe-Assoziation in Atlas eine Variable schreibt (ADR-0059).

## Reihenfolge beim Installieren

Die Architektur-Skizze eines Release haelt fest, was beim **Veroeffentlichen** bekannt ist. Deshalb: Applikation importieren, dann Informationsmodell und Capabilities anlegen, dann veroeffentlichen. `installieren.sh`, `installieren.ps1` und `deploy/opentofu/m365-devenv/scripts/install-m365-dienste.py` tun das in dieser Reihenfolge. Wer die Applikation ueber den Hub in der Console importiert, bekommt nur Prozesse, Formulare und Entscheidungen: Ein Applikationsbuendel (`atlas.json`, Format 3) traegt weder ein Informationsmodell noch Capabilities. Dann `einspielen.py` von Hand ausfuehren und ein neues Release veroeffentlichen.

## Informationsmodell

| Klasse | Art | Geschaeftsschluessel | Lebenszyklus | Beschreibung |
|---|---|---|---|---|
| `KontoHerkunft` | enumeration |  | Werte: Cloud, Synchronisiert | Woher ein Konto in Entra ID stammt: in der Cloud angelegt oder aus dem lokalen AD synchronisiert (Entra Connect). |
| `Zugriffsart` | enumeration |  | Werte: Vollzugriff, SendenAls, SendenImAuftrag, Kalenderstellvertretung | Die Rechte, die Atlas an einem Postfach erteilt. |
| `Sichtbarkeit` | enumeration |  | Werte: Public, Private | Sichtbarkeit einer Microsoft-365-Gruppe. |
| `InventarStatus` | enumeration |  | Werte: Active, Returned, Removed | Die Werte der Spalte Status in der SharePoint-Liste «Site-Inventar». |
| `Katalogposition` | businessObject | orderId, positionId | Bestellt → Erledigt → Abgelehnt → Fehlgeschlagen → Zurueckgegeben | Eine bestellte Position im Atlas-Shop: was eine Person fuer wen bestellt hat. Jeder Prozess des Beispiels meldet ihr Ergebnis an diese Position (erledigt, abgelehnt, fehlgeschlagen, zurueckgegeben); erst «erledigt» gibt das Recht im Bestand. |
| `Genehmigung` | businessObject |  | Offen → Eskaliert → Erteilt → Abgelehnt | Der Entscheid ueber eine Bestellung, die eine Freigabe braucht: durch die vorgesetzte Person aus Entra ID oder eine Ersatzgruppe, mit Erinnerung und Eskalation. |
| `Konto` | businessObject | userPrincipalName | Angelegt → Aktiv → Synchronisiert → CloudVerwaltet → Gesperrt | Ein Benutzerkonto in Entra ID. Personenbezogen: Name, Abteilung und Anmeldename gehoeren einer Person. |
| `Partnerkonto` | businessObject |  | Beantragt → Aktiv → Gesperrt | Ein Konto fuer eine externe Person (Partnerzugang), mit Ablaufdatum. Personenbezogen. |
| `AdKonto` | businessObject | samAccountName | Angelegt → Aktiv → Gesperrt → AusserhalbSync | Ein Benutzerkonto im lokalen Active Directory, das Entra Connect nach Entra ID synchronisiert (hybride Identitaet). Personenbezogen. |
| `Lizenzzuweisung` | businessObject |  | Beantragt → Zugewiesen → Entzogen | Eine Microsoft-365-Lizenz an einem Konto, direkt oder ueber eine Lizenzgruppe zugewiesen (Lizenzprofil M365Lizenzprofil). |
| `Empfaenger` | businessObject | primarySmtpAddress |  | Ein Empfaenger in Exchange: alles, was eine Mailadresse hat. Oberbegriff fuer Postfaecher, Verteiler und Kontakte. |
| `Postfach` | businessObject |  | Bereit → Abwesend → Weitergeleitet | Das persoenliche Postfach einer Person, mit Abwesenheit, Kontingent, Weiterleitung und weiteren Adressen. Personenbezogen. |
| `Funktionspostfach` | businessObject |  | Angelegt → Entfernt | Ein gemeinsames Postfach (Shared Mailbox) fuer eine Funktion oder ein Team, ohne eigene Lizenz. |
| `Ressourcenpostfach` | businessObject |  |  | Ein buchbares Postfach mit Buchungsregeln (Kalenderverarbeitung). Den Lebenszyklus tragen Raum- und Geraetepostfach selbst, weil ein Datenzustand gegen die Klasse aufgeloest wird, die das Datenobjekt nennt. |
| `Raumpostfach` | businessObject |  | Angelegt → Buchbar → Entfernt | Ein Besprechungsraum. |
| `Geraetepostfach` | businessObject |  | Angelegt → Buchbar → Entfernt | Ein buchbares Geraet, etwa ein Beamer oder ein Fahrzeug. |
| `Verteiler` | businessObject |  | Angelegt → Entfernt | Eine Verteilerliste in Exchange. |
| `Mailkontakt` | businessObject |  | Angelegt | Eine externe Adresse als Kontakt in Exchange, damit sie Mitglied eines Verteilers sein kann. Personenbezogen. |
| `Verteilermitgliedschaft` | businessObject |  | Beantragt → Aufgenommen → Entfernt | Die Mitgliedschaft eines Empfaengers in einem Verteiler (Selbstanmeldung oder Bestellung). |
| `Postfachberechtigung` | businessObject |  | Beantragt → Erteilt → Entzogen | Ein Recht einer Person an einem Postfach: Vollzugriff, Senden als, Senden im Auftrag oder Kalenderstellvertretung. |
| `Aufbewahrung` | businessObject |  | Beantragt → Aktiv → Aufgehoben | Litigation Hold auf einem Postfach: nichts darin wird endgueltig geloescht, solange er gilt. |
| `M365Gruppe` | businessObject | mailNickname | Aktiv → Geloescht | Eine Microsoft-365-Gruppe (Unified Group) in Entra ID: die Mitgliedschaft hinter Team, SharePoint-Site und Viva-Engage-Community. |
| `Team` | businessObject | groupId | Aktiv → Archiviert | Ein Microsoft Team auf einer Microsoft-365-Gruppe. |
| `SharePointSite` | businessObject | url | Angefordert → Bereit | Die Teamwebsite einer Microsoft-365-Gruppe. Microsoft legt sie zur Gruppe an; Atlas wartet, bis sie erreichbar ist. |
| `SiteInventarEintrag` | businessObject | alias | Active → Returned → Removed | Ein Eintrag in der SharePoint-Liste «Site-Inventar»: welche Site zu welcher Katalog-Position gehoert. Bewusst ohne Personendaten. |
| `EngageCommunity` | businessObject | groupId | Angelegt → Geloescht | Eine Community in Viva Engage auf einer Microsoft-365-Gruppe. |
| `Rufnummer` | businessObject | nummer | Frei → Zugewiesen | Eine Telefonnummer aus Teams Phone, einer Person oder einem Ressourcenkonto zugewiesen. |
| `Ressourcenkonto` | businessObject | userPrincipalName | Bereit → Entfernt | Das Konto, ueber das eine Telefonzentrale oder Warteschlange eine Rufnummer traegt. |
| `Telefonzentrale` | businessObject |  | Aktiv → Entfernt | Eine automatische Telefonzentrale (Auto Attendant) mit Begruessung und Weiterleitung. |
| `Anrufwarteschlange` | businessObject |  | Aktiv → Entfernt | Eine Anrufwarteschlange (Call Queue) mit ihren Agentinnen und Agenten. |
| `OeMitgliedschaften` | businessObject |  | Bestimmt → Zugeteilt → Entzogen | Die Mitgliedschaften, die eine Organisationseinheit vorgibt (Teams, Gruppen, Verteiler; M365OeMitgliedschaften). |
| `GovernanceBefund` | businessObject |  | Erfasst → Geprueft | Das Ergebnis der Governance-Pruefung: Postfaecher, Verteiler und Gruppen, die keine Atlas-Kennzeichnung tragen. |

Beziehungen:

- `Partnerkonto` ist ein `Konto`
- `Postfach` ist ein `Empfaenger`
- `Funktionspostfach` ist ein `Empfaenger`
- `Ressourcenpostfach` ist ein `Empfaenger`
- `Raumpostfach` ist ein `Ressourcenpostfach`
- `Geraetepostfach` ist ein `Ressourcenpostfach`
- `Verteiler` ist ein `Empfaenger`
- `Mailkontakt` ist ein `Empfaenger`
- `Konto` traegt `Lizenzzuweisung` (composition, 1 : 0..*)
- `AdKonto` wird synchronisiert als `Konto` (association, 0..1 : 0..1)
- `Konto` besitzt `Postfach` (association, 1 : 0..1)
- `Empfaenger` gewaehrt `Postfachberechtigung` (composition, 1 : 0..*)
- `Postfachberechtigung` fuer `Konto` (association, 0..* : 1)
- `Verteiler` hat `Verteilermitgliedschaft` (composition, 1 : 0..*)
- `Verteilermitgliedschaft` von `Empfaenger` (association, 0..* : 1)
- `Postfach` steht unter `Aufbewahrung` (composition, 1 : 0..1)
- `M365Gruppe` gehoert `Konto` (association, 0..* : 1..*)
- `Team` beruht auf `M365Gruppe` (association, 0..1 : 1)
- `SharePointSite` gehoert zu `M365Gruppe` (association, 1 : 1)
- `SiteInventarEintrag` beschreibt `SharePointSite` (association, 0..1 : 1)
- `EngageCommunity` beruht auf `M365Gruppe` (association, 0..1 : 1)
- `Rufnummer` gehoert `Konto` (association, 0..1 : 0..1)
- `Ressourcenkonto` traegt `Rufnummer` (association, 1 : 0..1)
- `Telefonzentrale` erreichbar ueber `Ressourcenkonto` (association, 0..1 : 1)
- `Anrufwarteschlange` erreichbar ueber `Ressourcenkonto` (association, 0..1 : 1)
- `Anrufwarteschlange` verteilt an `Konto` (association, 0..* : 0..*)
- `OeMitgliedschaften` fuer `Konto` (association, 0..* : 1)
- `Katalogposition` braucht `Genehmigung` (composition, 1 : 0..1)
- `Genehmigung` entschieden von `Konto` (association, 0..* : 0..1)
- `Partnerkonto` betreut von `Konto` (association, 0..* : 1)

Datenspeicher (nur lesend modelliert; Schreiben ueber einen Speicher ist in Atlas noch nicht entschieden):

| Speicher | Klasse | Worker | |
|---|---|---|---|
| Entra ID: Konten | `Konto` | `ucc_entra` | Benutzerkonten im Verzeichnis des Tenants (Microsoft Graph). |
| Entra ID: Gruppen | `M365Gruppe` | `ucc_entra` | Microsoft-365-Gruppen hinter Teams, Sites und Communities. |
| Exchange Online: Empfaenger | `Empfaenger` | `ucc_exchange` | Postfaecher, Verteiler und Kontakte in Exchange Online. |
| SharePoint Online: Sites | `SharePointSite` | `ucc_sharepoint` | Die Teamwebsites der Gruppen. |
| SharePoint Online: Site-Inventar | `SiteInventarEintrag` | `ucc_sharepoint` | Die Liste «Site-Inventar» auf der Site SITE_INVENTAR. |
| Teams Phone: Rufnummern | `Rufnummer` | `ucc_teams` | Rufnummern, Ressourcenkonten, Telefonzentralen und Warteschlangen. |
| Active Directory: Konten | `AdKonto` | `ad_verzeichnis` | Das lokale Verzeichnis (LDAPS), aus dem Entra Connect synchronisiert. |
| Exchange Server: Empfaenger | `Empfaenger` | `ucc_exchange_onprem` | nur in der OnPrem-Variante; das Installationsskript benennt Speicher und Modell um |

Personenbezogen sind `Konto`, `Partnerkonto`, `AdKonto`, `Postfach` und `Mailkontakt`. Das Modell haelt nur ihre Struktur; welche Prozessvariablen Personendaten tragen, deklarieren die Prozesse selbst (`atlas:personal`, ADR-0314). Ein Attribut des Informationsmodells kann sich heute nicht als personenbezogen markieren; das steht deshalb in der Beschreibung der Klasse.

## Datenobjekte in den Prozessen

| Prozess | Datenobjekte (Zustaende, die er schreibt) | Datenspeicher |
|---|---|---|
| Exchange: Aufbewahrung (Litigation Hold) (`aufbewahrung.bpmn`) | Aufbewahrung [Aktiv, Aufgehoben]; Katalogposition [Erledigt, Zurueckgegeben] | Exchange Online: Empfaenger |
| Microsoft 365: Eintrittspaket (`eintritt-paket.bpmn`) | Katalogposition [Erledigt, Zurueckgegeben] |  |
| Viva Engage: Community (`engage-community.bpmn`) | Katalogposition [Erledigt, Zurueckgegeben, Abgelehnt]; Community [Angelegt, Geloescht] | Entra ID: Konten, Entra ID: Gruppen |
| Exchange: Funktionspostfach (`funktionspostfach.bpmn`) | Katalogposition [Erledigt, Zurueckgegeben, Abgelehnt]; Funktionspostfach [Angelegt, Entfernt]; Postfachberechtigung [Erteilt, Entzogen] | Exchange Online: Empfaenger |
| Microsoft 365: Genehmigung durch Vorgesetzte (`genehmigung-vorgesetzter.bpmn`) | Genehmigung [Erteilt, Abgelehnt, Eskaliert] | Entra ID: Konten |
| Exchange: Gerätepostfach einrichten (`geraet-einrichten.bpmn`) | Katalogposition [Erledigt, Abgelehnt]; Geraetepostfach [Angelegt, Buchbar] | Exchange Online: Empfaenger |
| Exchange: Gerätepostfach entfernen (`geraet-entfernen.bpmn`) | Katalogposition [Zurueckgegeben]; Geraetepostfach [Entfernt] | Exchange Online: Empfaenger |
| Microsoft 365: Governance-Prüfung (`governance-pruefung.bpmn`) | Governance-Befund [Erfasst, Geprueft] | Entra ID: Gruppen, Exchange Online: Empfaenger |
| Microsoft 365: Eintritt und Austritt (HR-Ereignis) (`hr-eintritt.bpmn`) | Konto [Angelegt, Aktiv, Gesperrt] | Entra ID: Konten |
| Microsoft 365: Konto (hybrid) anlegen (`konto-hybrid.bpmn`) | Katalogposition [Erledigt, Zurueckgegeben, Abgelehnt]; AD-Konto [Angelegt, Aktiv, Gesperrt, AusserhalbSync]; Konto [Synchronisiert] | Entra ID: Konten, Active Directory: Konten |
| Microsoft 365: Konto in die Cloud übernehmen (`konto-in-cloud.bpmn`) | Katalogposition [Erledigt, Zurueckgegeben, Abgelehnt]; Konto [CloudVerwaltet, Synchronisiert]; AD-Konto [Gesperrt] | Entra ID: Konten, Active Directory: Konten |
| Microsoft 365: Lizenz und Postfach (`lizenz.bpmn`) | Katalogposition [Erledigt, Zurueckgegeben, Fehlgeschlagen]; Lizenzzuweisung [Zugewiesen, Entzogen]; Postfach [Abwesend, Bereit, Weitergeleitet]; Postfachberechtigung [Erteilt, Entzogen] | Entra ID: Konten, Entra ID: Gruppen, Exchange Online: Empfaenger, Active Directory: Konten |
| Microsoft 365: Mitgliedschaften der Organisationseinheit (`oe-mitgliedschaften.bpmn`) | Katalogposition [Erledigt, Zurueckgegeben]; OE-Mitgliedschaften [Zugeteilt, Entzogen] | Entra ID: Konten, Entra ID: Gruppen, Exchange Online: Empfaenger |
| Partnerzugang (öffentliches Formular) (`partnerzugang.bpmn`) | Partnerkonto [Aktiv, Gesperrt] | Entra ID: Konten |
| Exchange: Postfachzugriff entziehen (`postfachzugriff-entziehen.bpmn`) | Katalogposition [Zurueckgegeben]; Postfachberechtigung [Entzogen] | Exchange Online: Empfaenger |
| Exchange: Postfachzugriff erteilen (`postfachzugriff-erteilen.bpmn`) | Katalogposition [Erledigt, Abgelehnt]; Postfachberechtigung [Erteilt] | Exchange Online: Empfaenger |
| Exchange: Sitzungszimmer einrichten (`raum-einrichten.bpmn`) | Katalogposition [Erledigt, Abgelehnt]; Raumpostfach [Angelegt, Buchbar] | Exchange Online: Empfaenger |
| Exchange: Sitzungszimmer entfernen (`raum-entfernen.bpmn`) | Katalogposition [Zurueckgegeben]; Raumpostfach [Entfernt] | Exchange Online: Empfaenger |
| SharePoint: Team Site (`sharepoint-site.bpmn`) | Katalogposition [Erledigt, Zurueckgegeben, Abgelehnt]; M365-Gruppe [Aktiv, Geloescht]; SharePoint-Site [Bereit]; Site-Inventar-Eintrag [Active, Returned, Removed] | Entra ID: Konten, Entra ID: Gruppen, Exchange Online: Empfaenger, SharePoint Online: Sites, SharePoint Online: Site-Inventar |
| Microsoft Teams: Team (`team.bpmn`) | Katalogposition [Erledigt, Zurueckgegeben, Abgelehnt]; M365-Gruppe [Aktiv]; Team [Aktiv, Archiviert] | Entra ID: Konten, Entra ID: Gruppen |
| Teams-Telefonie: persönliche Rufnummer (`telefonie.bpmn`) | Katalogposition [Erledigt, Zurueckgegeben]; Lizenzzuweisung [Zugewiesen, Entzogen]; Rufnummer [Zugewiesen, Frei] | Entra ID: Konten, Teams Phone: Rufnummern |
| Teams Phone: Telefonzentrale (`telefonzentrale.bpmn`) | Katalogposition [Erledigt, Zurueckgegeben, Abgelehnt]; Ressourcenkonto [Bereit, Entfernt]; Lizenzzuweisung [Zugewiesen]; Telefonzentrale [Aktiv, Entfernt]; Rufnummer [Zugewiesen] | Entra ID: Konten, Teams Phone: Rufnummern |
| Exchange: Verteilerliste (`verteiler.bpmn`) | Katalogposition [Erledigt, Zurueckgegeben, Abgelehnt]; Verteiler [Angelegt, Entfernt]; Verteilermitgliedschaft [Aufgenommen, Entfernt] | Exchange Online: Empfaenger |
| Verteiler-Anmeldung (öffentliches Formular) (`verteiler-anmeldung.bpmn`) | Mailkontakt [Angelegt]; Verteilermitgliedschaft [Aufgenommen, Entfernt] | Exchange Online: Empfaenger |
| Teams Phone: Anrufwarteschlange (`warteschlange.bpmn`) | Katalogposition [Erledigt, Zurueckgegeben, Abgelehnt]; Ressourcenkonto [Bereit, Entfernt]; Lizenzzuweisung [Zugewiesen]; Warteschlange [Aktiv, Entfernt]; Rufnummer [Zugewiesen] | Entra ID: Konten, Teams Phone: Rufnummern |

## Capabilities

Eigentuemerin aller Capabilities ist die Gruppe M365-Administration. Die Schluessel beginnen mit `m365-`, weil Capabilities serverweit gelten und nicht zu einer Applikation gehoeren.

| Schluessel | Capability | Realisiert durch | Braucht |
|---|---|---|---|
| `m365-konten` | **Benutzerkonten verwalten**: Konten fuer Mitarbeitende anlegen, aktivieren, sperren und zurueckgeben, ausgeloest durch HR oder eine Bestellung; auch hybrid aus dem lokalen AD und die Uebernahme der Verwaltung in die Cloud. | Microsoft 365: Eintritt und Austritt (HR-Ereignis); Microsoft 365: Eintrittspaket; Microsoft 365: Konto (hybrid) anlegen; Microsoft 365: Konto in die Cloud übernehmen |  |
| `m365-lizenzen` | **Lizenzen und Postfaecher bereitstellen**: Einer Person die passende Microsoft-365-Lizenz zuweisen, damit Postfach und Apps entstehen, und Postfacheinstellungen pflegen (Abwesenheit, Kontingent, Weiterleitung, Adressen, Stellvertretung). | Microsoft 365: Lizenz und Postfach | `m365-konten` |
| `m365-zugriffe` | **Zugriffe gewaehren und entziehen**: Rechte an Postfaechern und Mitgliedschaften nach Organisationseinheit erteilen und wieder entziehen, mit Pruefung durch die Informationssicherheit, wo die Freigaberegel es verlangt. | Exchange: Postfachzugriff erteilen; Exchange: Postfachzugriff entziehen; Microsoft 365: Mitgliedschaften der Organisationseinheit; `proc_m365_postfachzugriff_erteilen_onprem` (OnPrem); `proc_m365_postfachzugriff_entziehen_onprem` (OnPrem) | `m365-konten` |
| `m365-partner` | **Externe Partner anbinden**: Externen Personen ueber ein oeffentliches Formular einen befristeten Zugang geben und ihn nach Ablauf sperren. | Partnerzugang (öffentliches Formular) | `m365-konten` |
| `m365-zusammenarbeit` | **Zusammenarbeitsraeume bereitstellen**: Teams, SharePoint-Teamwebsites und Viva-Engage-Communities nach Namensregel anlegen, mit Besitzern, Gaesten, Kontingent und Inventar, und sie archivieren oder loeschen. | Microsoft Teams: Team; SharePoint: Team Site; Viva Engage: Community | `m365-konten` |
| `m365-messaging` | **Gemeinsame Postfaecher und Verteiler bereitstellen**: Funktionspostfaecher und Verteiler anlegen, ihre Berechtigten und Mitglieder pflegen und Anmeldungen zu Verteilern annehmen. | Exchange: Funktionspostfach; Exchange: Verteilerliste; Verteiler-Anmeldung (öffentliches Formular); `proc_m365_funktionspostfach_onprem` (OnPrem); `proc_m365_verteiler_onprem` (OnPrem) | `m365-konten` |
| `m365-ressourcen` | **Raeume und Geraete buchbar machen**: Raum- und Geraetepostfaecher mit Buchungsregeln einrichten und wieder entfernen. | Exchange: Sitzungszimmer einrichten; Exchange: Sitzungszimmer entfernen; Exchange: Gerätepostfach einrichten; Exchange: Gerätepostfach entfernen; `proc_m365_raum_einrichten_onprem` (OnPrem); `proc_m365_raum_entfernen_onprem` (OnPrem); `proc_m365_geraet_einrichten_onprem` (OnPrem); `proc_m365_geraet_entfernen_onprem` (OnPrem) |  |
| `m365-telefonie` | **Telefonie bereitstellen**: Personen eine Rufnummer mit Telefonielizenz geben und Telefonzentralen und Anrufwarteschlangen mit Ressourcenkonto einrichten. | Teams-Telefonie: persönliche Rufnummer; Teams Phone: Telefonzentrale; Teams Phone: Anrufwarteschlange | `m365-lizenzen` |
| `m365-aufbewahrung` | **Daten rechtssicher aufbewahren**: Postfaecher unter Aufbewahrung (Litigation Hold) stellen und sie wieder aufheben. | Exchange: Aufbewahrung (Litigation Hold) |  |
| `m365-genehmigung` | **Bestellungen genehmigen**: Bestellungen der vorgesetzten Person aus Entra ID vorlegen, erinnern und eskalieren, mit Ersatzgruppe, wenn niemand zu finden ist. | Microsoft 365: Genehmigung durch Vorgesetzte |  |
| `m365-governance` | **Governance der Umgebung pruefen**: Regelmaessig pruefen, welche Postfaecher, Verteiler und Gruppen nicht ueber Atlas entstanden sind, und den Befund vorlegen. | Microsoft 365: Governance-Prüfung |  |

## Wertstroeme

**Mitarbeitende arbeitsfaehig machen** (`m365-mitarbeitende-arbeitsfaehig`): Vom HR-Eintritt bis zum Austritt: Konto, Lizenz, Zusammenarbeit und Telefonie, am Ende Rueckgabe und Aufbewahrung.

1. Eintritt erfassen: `m365-konten`
2. Lizenz und Postfach bereitstellen: `m365-lizenzen`, `m365-genehmigung`
3. In Teams und Gruppen einbinden: `m365-zugriffe`, `m365-zusammenarbeit`
4. Erreichbar machen: `m365-telefonie`, `m365-messaging`
5. Austritt abwickeln: `m365-konten`, `m365-aufbewahrung`

**Zusammenarbeit ermoeglichen** (`m365-zusammenarbeit-ermoeglichen`): Von der Bestellung eines Arbeitsraums bis zu seiner regelmaessigen Pruefung.

1. Bedarf bestellen und freigeben: `m365-genehmigung`
2. Arbeitsraum bereitstellen: `m365-zusammenarbeit`, `m365-messaging`, `m365-ressourcen`
3. Zugriffe steuern: `m365-zugriffe`, `m365-partner`
4. Pruefen und aufraeumen: `m365-governance`

## Was die Tests pruefen

`examples/m365architektur_test.go`: Das Informationsmodell besteht dieselbe Pruefung wie beim Speichern und kommt beim Import ohne Verlust an; jedes Datenobjekt ist mit einer Klasse des Modells typisiert, jeder geschriebene Zustand gehoert zu deren Lebenszyklus, jeder Speicher ist einer des Modells; jede Capability und jeder Wertstrom besteht die Pruefung des Servers, jeder Prozess der Applikation ist von einer Capability realisiert, und jede Abhaengigkeit und Stufe nennt eine Capability der Datei. Atlas selbst lehnt einen Deploy mit unbekannter Klasse oder unbekanntem Zustand nicht ab (ADR-0259 warnt nur); die Tests halten die drei Dateien deshalb zusammen.

## Grenzen

- Ein Applikationsbuendel (Hub, Quellbaum) traegt kein Informationsmodell und keine Capabilities; sie kommen ueber `einspielen.py` oder das Installationsskript der Testumgebung.
- Capabilities und Wertstroeme sind serverweit. Zwei Installationen des Beispiels auf demselben Server teilen sie.
- Gelesene Objekte sind nicht gezeichnet (siehe oben); die Skizze zeigt deshalb nur schreibende Zugriffe.
- Die Zustaende sind die fachlichen Stationen, die ein Prozess erreicht, nicht der Zustand im Zielsystem. Ob ein Konto in Entra ID tatsaechlich gesperrt ist, weiss nur Entra ID.

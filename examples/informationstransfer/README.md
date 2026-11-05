# Informationstransfer – Ressourcen eines Kontos auf ein anderes übertragen

Wer intern wird (Internalisierung, extern → intern), extern wird (Externalisierung, intern → extern)
oder das Amt oder die Abteilung wechselt, erhält oft ein neues Konto. Dieses Paket überträgt die
Ressourcen des bisherigen Kontos (**Quelle**) auf das neue (**Ziel**): Primäradresse und UPN,
Postfachinhalt, Postfachkontingent, Telefonnummer, Berechtigungen (manuelle Gruppen), Kürzel und –
nur bei einer Internalisierung – das Cloud-Konto in Entra ID. Es bildet den Prozess «Transfer
Resources» des BIT Orchestration Framework fachlich nach, für eine hybride Umgebung aus Active
Directory, Exchange (Server oder Online) und Entra ID.

Jeder Schritt läuft entweder über einen Worker oder geht als klar beschriebene Aufgabe an das
zuständige Team. Was eine Plattform nicht kann, steht unten unter «Was eine Aufgabe ist».

## Inhalt

| Datei | Inhalt |
|---|---|
| `atlas.json` | Manifest der Applikation «Beispiel: Informationstransfer» (Schlüssel `beispiel-informationstransfer`): 9 Prozesse, 9 Formulare, 1 Entscheidung, Informationsmodell und Architektur (Format 4) |
| `katalog.json` | Ein Katalog «Informationstransfer» (`cat-it-identity`, Rang 720) mit dem Produkt `it-informationstransfer` |
| `fragen.json`, `antworten.beispiel.json` | Der eine Platzhalter: die Zielgruppe (Identity Management und Administration) |
| `informationstransfer.bpmn` | Der Lebenszyklus des Produkts (`proc_it_transfer`) |
| `postfaecher.bpmn`, `entra.bpmn`, `rechte.bpmn`, `kontingent.bpmn`, `kuerzel.bpmn`, `telefon.bpmn`, `inhalt.bpmn`, `adresse.bpmn` | Ein Teilprozess je Ressource (Call Activities) |
| `it-optionen.dmn` | Die Entscheidung `InfotransferOptionen`: welche Optionen im Szenario zulässig sind, mit Gründen und Hinweisen |
| `it-*.form.json` | Bestellformular und Aufgabenformulare |
| `architektur/` | Informationsmodell und Vorschläge für Capabilities und Wertstrom (ADR-0528) |
| `installieren.sh`, `installieren.ps1` | Installation in eine Atlas-Instanz |

## Das Produkt

| Produkt | Varianten (Szenario) | Genehmigung | Prozess | Aktionen |
|---|---|---|---|---|
| Informationstransfer (`it-informationstransfer`) | Internalisierung, Externalisierung, Amtswechsel, Abteilungswechsel | im Prozess: Identity Management; Entra-Transfer zusätzlich Informationssicherheit | `informationstransfer.bpmn` | Transfer abschliessen (der Prozess selbst und der Betrieb) |

Die Katalog-Genehmigung ist `none`, weil zuerst die Regel prüft: Die bestellende Person soll die
Gründe einer unzulässigen Kombination sofort lesen, nicht erst nach einer Genehmigung. Identity
Management genehmigt danach mit der Zusammenfassung (Gruppe aus `GRUPPE_IDENTITY_MANAGEMENT`).

Ein Transfer ist kein Recht, das jemand hält. Die Position meldet `done`, sobald alles ausgeführt
ist, und gibt sich dann selbst zurück (Shop-Aufgabe im Modus `command`, wie `ad-versetzung`).

**Bestellformular** (`it-bestellung`): Quell- und Zielkonto über die Personensuche im Active
Directory (ADR-0473; übermittelt wird der Anmeldename), je eine Ja/Nein-Frage für die sieben
Optionen, bei der Primäradresse die Bestätigung für verschiedene Mail-Domains, und eine Begründung.
Die Optionen sind Ja/Nein-Auswahlen statt Kästchen, weil eine Bestellung ihre Antworten als Text
überträgt (`TestConfigFormsAnswerInText`).

## Die Regel `InfotransferOptionen`

Eine Tabelle mit Trefferpolitik COLLECT: Jede verletzte Regel liefert `{art: "grund", text}`, jeder
Hinweis `{art: "hinweis", text}`. Ein Grund verhindert den Transfer, und sein Text geht über
`…/decision` an die bestellende Person; die Hinweise stehen in der Genehmigung.

| Grund | Wann |
|---|---|
| Szenario unbekannt | `variantId` ist keines der vier |
| Keine Ressource gewählt | alle Optionen «Nein» |
| Quelle fehlt / Ziel fehlt oder gleich | nicht unter `AD_PERSONEN_OU` gefunden, oder Quelle = Ziel |
| Schon transferiert | die Quelle trägt den Vermerk «Ressourcen transferiert» |
| Entra nur bei Internalisierung | Entra-Transfer bei Externalisierung, Amts- oder Abteilungswechsel |
| Entra ausgeschaltet | `ENTRA_TRANSFER` ist nicht `ein` |
| Verschiedene Mail-Domain, unbestätigt | Primäradresse ohne gleiche Domain und ohne ausdrückliche Bestätigung |
| Kürzel importiert | `KUERZEL_HERKUNFT` ist nicht `generiert` (bei BIT: Kürzel aus dem CIS) |
| Entra und Telefon | mit dem Entra-Transfer behält das Cloud-Konto seine Nummer |
| Entra und Postfach (Online) | mit Exchange Online behält das Cloud-Konto beim Entra-Transfer sein Postfach |
| Kein Postfach | eine Postfach-Option, aber Quelle oder Ziel hat kein Postfach |

Hinweise: kombinierte Variante ohne Präfix, Präfix `transfered.` ohne Inhalt, Löschung des
Quellpostfachs, Aufgabe für Exchange Online, Kontingent nur bei grösserer Quelle, Telefonie-Team,
welche Gruppen, Kürzel, Entra-Transfer mit endgültiger Löschung, bestätigte Domain, die übliche
Option je Szenario und der Vermerk am Schluss.

## Ablauf

`proc_it_transfer` liest die Konten, prüft, genehmigt lassen und ruft dann die Teilprozesse in
dieser Reihenfolge auf. Jeder Teilprozess prüft selbst, ob seine Option gewählt ist, und endet
sonst sofort.

1. Entra-Transfer – er findet die Cloud-Konten über die noch unveränderten UPNs.
2. Berechtigungen.
3. Kontingent – vor dem Inhalt, damit das Zielpostfach Platz hat.
4. Kürzel.
5. Telefonnummer.
6. Postfachinhalt – vor der Primäradresse, weil die Adresse der Quelle erst frei ist, wenn ihr
   Postfach weg ist.
7. Primäradresse und UPN.

Danach erhält die Quelle den Vermerk und das Protokoll (`protokoll`, eine Zeile je Teilprozess)
bleibt in der Instanz. Es ersetzt den «Provisioning Job Search Scope» von BIT: Der Verlauf der
Instanz und ihrer Teilprozesse zeigt jeden Schritt, jeden Incident und jede Aufgabe.

### `proc_it_transfer` (Informationstransfer)

| Schritt | Art | Wer | Eingaben | Ergebnis | Fehler |
|---|---|---|---|---|---|
| Quellkonto lesen | AD `search` | `ad_verzeichnis` | `quelle`, `AD_PERSONEN_OU` | `quelleKonto` (versiegelt), `quelleDa` | Directory nicht erreichbar → Incident |
| Zielkonto lesen | AD `search` | `ad_verzeichnis` | `ziel`, `quelle` | `zielKonto` (versiegelt), `zielDa` (false, wenn Quelle = Ziel) | wie oben |
| Vermerk der Quelle prüfen | AD `search` | `ad_verzeichnis` | `quelle` | `quelleTransferiert` | wie oben |
| Mail-Domain vergleichen | AD `search` | `ad_verzeichnis` | `ziel`, Domain aus `quelleKonto` | `gleicheDomain` | wie oben |
| Postfächer lesen | Call Activity | `proc_it_postfaecher` | Optionen | `pfQuelle`, `pfZiel`, `pfQuelleDa`, `pfZielDa`, `dbQuelle`, `dbZiel` | siehe dort |
| Optionen prüfen | Business Rule | `InfotransferOptionen` | Szenario, Optionen, Wahrheitswerte, Konstanten | `regel` | – |
| Ergebnis zusammenfassen | Script | Engine | `regel` | `pruefung` = {ok, gruende, hinweise} | – |
| Transfer genehmigen | User Task | `GRUPPE_IDENTITY_MANAGEMENT` | Zusammenfassung | `entscheid`, `kommentar` | – |
| Ablehnung melden | REST | Secret `atlas` | Gründe oder Kommentar | Position abgelehnt | Atlas nicht erreichbar → Incident |
| sieben Call Activities | Call Activity | Teilprozesse | alle Variablen (Kopien) | `ergebnisEntra` … `ergebnisAdresse` | siehe dort |
| Quelle markieren | AD `update-attributes` | `ad_verzeichnis` | `quelleKonto` | `extensionAttribute15` = Vermerk, Anzeigename + « (TRANSFER)» bei Adresse oder Inhalt | Schreibrecht fehlt → Incident |
| Anzeigename in Entra ID | Entra `update-user` | `ucc_entra` | `pfQuelle.ExternalDirectoryObjectId` | Cloud-Anzeigename + « (TRANSFER)» (nur Exchange Online) | Incident |
| Protokoll schreiben | Script | Engine | `ergebnis…` | `protokoll` | – |
| Position erledigt / zurückgeben | REST, Shop-Aufgabe | Secret `atlas` | `orderId`, `positionId` | `done`, danach `deprovision` | Incident |

### `proc_it_postfaecher` (Postfächer lesen)

Nur wenn Primäradresse, Inhalt oder Kontingent gewählt ist.

| Schritt | Art | Wer | Eingaben | Ergebnis | Fehler |
|---|---|---|---|---|---|
| Postfach der Quelle? / des Ziels? (Server) | AD `search` mit `homeMDB=*` | `ad_verzeichnis` | `quelle`, `ziel` | `pfQuelleDa`, `pfZielDa` | Incident |
| Quell- / Zielpostfach lesen (Server) | Exchange `get-mailbox` | `ucc_exchange_onprem` | Anmeldename | `pfQuelle`, `pfZiel` (versiegelt) | Incident |
| Datenbank der Quelle / des Ziels | `run-get-cmdlet` `Get-MailboxDatabase` | `ucc_exchange_onprem` | `pfQuelle.Database` | `dbQuelle`, `dbZiel` (Datenbankvorgaben für das Kontingent) | Incident |
| Postfach der Quelle? / des Ziels? (Online) | Entra `list-users`, `mail ne null`, select `id` | `ucc_entra` | UPN | `pfQuelleDa`, `pfZielDa` | Incident |
| Quell- / Zielpostfach lesen (Online) | Exchange `get-mailbox` | `ucc_exchange` | UPN | `pfQuelle`, `pfZiel` | Incident |

### `proc_it_entra` (Entra-Transfer, nur Internalisierung)

Der Prozess läuft nur mit `optEntra = ja`, `variantId = internalisierung` **und**
`ENTRA_TRANSFER = ein`, auch wenn die Regel das schon geprüft hat. Die Vorgabe der Konstante ist
`aus`: Ohne gesetzte Konstante endet er, ohne einen einzigen Job zu erzeugen
(`TestTheEntraTransferStaysOffByDefault`).

| Schritt | Art | Wer | Eingaben | Ergebnis | Fehler |
|---|---|---|---|---|---|
| ObjectGUID lesen | AD `search`, select `objectGUID` | `ad_verzeichnis` | `ziel` | `zielGuid` (base64, versiegelt), `guidDa` | Incident |
| Altes / neues Cloud-Konto finden | Entra `list-users` nach UPN, nur mit `NAME_PREFIX` | `ucc_entra` | UPN der Quelle / des Ziels | `cloudAlt`, `cloudNeu` (nur Objekt-ID und `onPremisesSyncEnabled`) | Incident |
| Nicht eindeutig | Script + Ende | Engine | – | `ergebnis` | – |
| Synchronisation vorbereiten | User Task | `GRUPPE_IDENTITY_MANAGEMENT` | Konten | – (Entra Connect anhalten, beide Konten cloud-verwaltet) | – |
| Verwaltung prüfen | Entra `list-users` mit beiden IDs | `ucc_entra` | Objekt-IDs | `cloudStand` | Incident |
| Entra-Transfer freigeben | User Task | `GRUPPE_INFORMATIONSSICHERHEIT` | beide Konten, Source Anchor | `freigabe`, `bestaetigt` | – |
| Altes / neues Konto löschen | Entra `delete-user` | `ucc_entra` | Objekt-ID | im Papierkorb | Incident |
| Neues Konto endgültig löschen | Entra `permanently-delete-user` | `ucc_entra` | Objekt-ID des neuen Kontos | weg | eine ID ausserhalb des Papierkorbs → 404 → Incident |
| Altes Konto wiederherstellen | Entra `restore-user` | `ucc_entra` | Objekt-ID des alten Kontos | wiederhergestellt, cloud-verwaltet | Incident |
| Source Anchor setzen | Entra `update-user` | `ucc_entra` | `onPremisesImmutableId` = `zielGuid` | Hard Match vorbereitet | Incident |
| Hard Match bestätigen | User Task | `GRUPPE_IDENTITY_MANAGEMENT` | – | Entra Connect wieder an, Delta-Lauf | – |
| Verknüpfung lesen | Entra `get-user`, select `onPremisesSyncEnabled` | `ucc_entra` | Objekt-ID | `hardMatch` → `ergebnis` | Incident |

Warum erst beide löschen und dann das alte wiederherstellen: Ein wiederhergestelltes Konto ist von
der Cloud verwaltet. So wird aus dem bisher synchronisierten externen Konto eines, das Entra Connect
über den Source Anchor neu übernehmen kann. Die Objekt-ID, Teams, OneDrive, SharePoint und die
Berechtigungen des alten Kontos bleiben.

### `proc_it_rechte` (Berechtigungen)

| Schritt | Art | Wer | Eingaben | Ergebnis | Fehler |
|---|---|---|---|---|---|
| AD-Gruppen der Quelle / des Ziels | AD `search` `member=<DN>` unter `AD_GRUPPEN_OU`, select `1.1` | `ad_verzeichnis` | DN aus `quelleKonto`, `zielKonto` | `adQuelle`, `adZiel`, `adGruppen.dazu` | mehr als 500 Gruppen → Incident |
| In AD-Gruppen aufnehmen | AD `add-group-member`, Mehrfachinstanz | `ad_verzeichnis` | `adGruppen.dazu` | Mitgliedschaften | Recht fehlt → Incident |
| Cloud-Konto der Quelle / des Ziels | Entra `list-users` | `ucc_entra` | UPN | `cloudQuelle`, `cloudZiel` | Incident |
| Cloud-Gruppen der Quelle / des Ziels | Entra `list-user-groups` | `ucc_entra` | Objekt-ID | `gruppenQuelle`, `entraGruppen.dazu` | Incident |
| In Cloud-Gruppen aufnehmen | Entra `add-group-member`, Mehrfachinstanz | `ucc_entra` | `entraGruppen.dazu` | Mitgliedschaften | Incident |

Manuell heisst: direkte Mitgliedschaft (`member=` findet keine verschachtelte), im AD unter
`AD_GRUPPEN_OU` (dort legt `ad-dienste` die bestellten Gruppen an; Gruppen einer
Organisationseinheit, eingebaute Gruppen und die primäre Gruppe liegen ausserhalb), in Entra ID
zugewiesen (keine `DynamicMembership`), nicht aus dem AD synchronisiert und kein Verteiler oder
mail-aktivierte Sicherheitsgruppe (die verwaltet Exchange). Mit dem Entra-Transfer werden nur die
AD-Gruppen übertragen.

### `proc_it_kontingent` (Kontingent)

| Schritt | Art | Wer | Eingaben | Ergebnis | Fehler |
|---|---|---|---|---|---|
| Kontingent setzen (Server) | Exchange `set-mailbox-quota` | `ucc_exchange_onprem` | `pfQuelle`, `pfZiel`, `dbQuelle`, `dbZiel` | drei Grenzen und `UseDatabaseQuotaDefaults` | Incident |
| Kontingent setzen (Online) | Exchange `set-mailbox-quota` | `ucc_exchange` | `pfQuelle`, `pfZiel` | drei Grenzen | über der Lizenzgrenze → Incident |

Verglichen wird `ProhibitSendReceiveQuota` in KB, aus den Bytes in der Klammer von «2 GB
(2,147,483,648 bytes)»; `Unlimited` ist grösser als jede Zahl, mit `UseDatabaseQuotaDefaults` gilt
die Datenbank. Der Vergleich steht in den Parametern, die der Worker auswertet: Ist die Quelle
grösser, erhält das Ziel ihre Grenzen (in Bytes), sonst seine eigenen zurück.

### `proc_it_kuerzel` (Kürzel)

| Schritt | Art | Wer | Eingaben | Ergebnis | Fehler |
|---|---|---|---|---|---|
| Kürzel der Quelle entfernen | AD `update-attributes` | `ad_verzeichnis` | `quelleKonto.dn` | `extensionAttribute10` leer | Incident |
| Kürzel dem Ziel geben | AD `update-attributes` | `ad_verzeichnis` | Kürzel aus `quelleKonto` | `extensionAttribute10` des Ziels | Incident |

Das Kürzel in `extensionAttribute10` ist eine Annahme dieses Pakets. Wer es anderswo führt, ändert
den Attributnamen in `kuerzel.bpmn` und im `select` von `informationstransfer.bpmn`.

### `proc_it_telefon` (Telefonnummer)

| Schritt | Art | Wer | Eingaben | Ergebnis | Fehler |
|---|---|---|---|---|---|
| Nummer der Quelle entziehen | Teams `remove-phone-number` | `ucc_teams` | UPN der Quelle | `rufnummer` = {removed, phoneNumbers} | Incident |
| Nummer dem Ziel geben | Teams `ensure-phone-number`, Direct Routing | `ucc_teams` | UPN des Ziels, erste Nummer | `zuweisung` | Nummer frei im Bestand, Incident; ein Retry gibt sie dem Ziel |
| Telefonie-Team informieren | User Task | `GRUPPE_TELEFONIE` | Nummer, Konten | Verwaltung nachgeführt (bei BIT: Atiras-Mutation) | – |

`rufnummer` ist nicht versiegelt: Die Engine muss lesen, ob eine Nummer entzogen wurde. Es enthält
nur Geschäftsnummern, keinen Namen.

### `proc_it_inhalt` (Postfachinhalt)

| Schritt | Art | Wer | Eingaben | Ergebnis | Fehler |
|---|---|---|---|---|---|
| Löschung freigeben (Server) | User Task | `GRUPPE_IDENTITY_MANAGEMENT` | Quell- und Zielpostfach | `freigabe`, `bestaetigt` | – |
| Quellpostfach abtrennen | Exchange `disable-mailbox` | `ucc_exchange_onprem` | Anmeldename der Quelle | Exchange-Attribute entfernt, Postfach abgetrennt | Incident |
| Abtrennen abwarten | Timer PT2M | Engine | – | – | – |
| Inhalt wiederherstellen | Exchange `restore-mailbox-content` (`New-MailboxRestoreRequest`) | `ucc_exchange_onprem` | `ExchangeGuid`, `Database` aus `pfQuelle`, Ziel | Anforderung `atlas-it-<orderId>-<positionId>` | Postfach noch nicht abgetrennt → Incident, Retry |
| Warten / Stand lesen | Timer `WIEDERHERSTELLUNG_INTERVALL`, `run-get-cmdlet` `Get-MailboxRestoreRequest` | `ucc_exchange_onprem` | Name der Anforderung | `wiederherstellung`, `runde` | Incident |
| Wiederherstellung prüfen | User Task | `GRUPPE_EXCHANGE` | Stand | `inhaltUebertragen` | gescheitert, angehalten oder mehr als `WIEDERHERSTELLUNG_RUNDEN` Runden |
| Inhalt übertragen (Online) | User Task | `GRUPPE_EXCHANGE` | beide Postfächer | `inhaltUebertragen`, `bemerkung` | – |

### `proc_it_adresse` (Primäradresse und UPN)

| Schritt | Art | Wer | Eingaben | Ergebnis | Fehler |
|---|---|---|---|---|---|
| Quelle umbenennen (nur ohne Inhalt) | Exchange `update-mailbox` (`EmailAddresses`, Server auch `EmailAddressPolicyEnabled=false`) | `ucc_exchange_onprem` / `ucc_exchange` | `pfQuelle` | Primäradresse `transfered.<adresse>`, die bisherige entfernt | Incident |
| Adresse dem Ziel geben | Exchange `update-mailbox` | dito | `pfQuelle`, `pfZiel` | Primäradresse der Quelle, die eigene als Proxy | Adresse noch belegt → Incident |
| UPN der Quelle / des Ziels (Entra, nur Online) | Entra `update-user` | `ucc_entra` | `ExternalDirectoryObjectId` | `transfered.<upn>` / bisherige UPN der Quelle | Incident |
| UPN der Quelle / des Ziels (AD) | AD `update-attributes` | `ad_verzeichnis` | `quelleKonto`, `zielKonto` | `userPrincipalName` | UPN belegt → Incident |

Die drei Varianten von BIT: **3.1** nur Adresse – die Quelle erhält das Präfix, das Ziel Adresse und
UPN. **3.2** nur Inhalt – die Quelle verliert ihr Postfach, UPN bleiben. **3.3** beides – kein
Präfix auf der Adresse (die Quelle hat kein Postfach mehr), die UPN der Quelle erhält das Präfix,
weil das Ziel ihre UPN übernimmt. Dass das Ziel Anzeigename und AD-Pfad der Quelle mit «Use Initials
for Uniqueness» übernimmt, bildet das Paket nicht ab.

## Sicherheit bei Löschungen

Drei Schritte zerstören etwas: die beiden Soft Deletes und der Hard Delete des Entra-Transfers und
das Abtrennen des Quellpostfachs. Für alle gilt:

- **Freigabe unmittelbar davor.** Vor dem Abtrennen gibt Identity Management frei, vor der ersten
  Entra-Löschung die Informationssicherheit, je mit einer Zusammenfassung aus dem, was der Prozess
  eben gelesen hat. Weiter geht es nur mit `freigabe = freigegeben` **und** dem angekreuzten
  `bestaetigt`. `TestNothingIsDeletedWithoutARelease` prüft, dass keiner dieser Schritte ohne die
  Freigabe erreichbar ist.
- **Ausgeschaltet als Vorgabe.** `ENTRA_TRANSFER` ist `aus`, bis jemand `ein` setzt; die Regel
  lehnt den Entra-Transfer sonst mit Grund ab, und der Teilprozess prüft es noch einmal.
- **Nur im eigenen Bereich.** Konten werden nur unter `AD_PERSONEN_OU` gefunden; mit `NAME_PREFIX`
  findet der Prozess nur Cloud-Konten, deren UPN so beginnt.
- **Endgültig nur, was eben gelöscht wurde.** `permanently-delete-user` adressiert den Papierkorb;
  eine Objekt-ID, die dort nicht liegt, lässt den Job scheitern.

## Was eine Aufgabe ist

| Was | Warum | An wen |
|---|---|---|
| Postfachinhalt in Exchange Online | Exchange Online hat keine Operation, die den Inhalt eines bestehenden Postfachs in ein anderes verschiebt; `New-MailboxRestoreRequest` nimmt dort nur ein gelöschtes oder inaktives Postfach. Über die App-only-REST-Schnittstelle ist das nicht zu automatisieren. | `GRUPPE_EXCHANGE` |
| Gescheiterte oder zu lange Wiederherstellung | ein Mensch muss den Bericht lesen | `GRUPPE_EXCHANGE` |
| Meldung an die Telefonie | bei BIT eine Mutation in Atiras, ausserhalb von Atlas | `GRUPPE_TELEFONIE` |
| Entra Connect anhalten und wieder einschalten, Hard Match prüfen | Entra Connect hat keine Schnittstelle für Atlas | `GRUPPE_IDENTITY_MANAGEMENT` |

## Applikationskonstanten

| Konstante | Vorgabe | Bedeutung |
|---|---|---|
| `ATLAS_API_BASE` | `http://127.0.0.1:8080` | Adresse von Atlas für die Positionsroute |
| `AD_PERSONEN_OU` | `OU=Personen,DC=contoso,DC=local` | wo Quelle und Ziel liegen müssen |
| `AD_GRUPPEN_OU` | `OU=Gruppen,DC=contoso,DC=local` | wo die manuellen Gruppen liegen |
| `POSTFACH_SYSTEM` | `exchange-server` | `exchange-server` (hybrid wie BIT: Postfächer auf dem Exchange Server, Entra ID folgt dem AD) oder `exchange-online` (Konten, die die Cloud verwaltet) |
| `ENTRA_TRANSFER` | `aus` | `ein` erlaubt den Entra-Transfer |
| `KUERZEL_HERKUNFT` | `generiert` | `generiert` (Atlas, bei BIT: ORA) oder z. B. `importiert` (CIS) |
| `NAME_PREFIX` | leer | wenn gesetzt: nur Cloud-Konten mit diesem UPN-Präfix |
| `GRUPPE_IDENTITY_MANAGEMENT` | `Identity Management` | Genehmigung, Löschfreigabe Postfach, Entra Connect |
| `GRUPPE_INFORMATIONSSICHERHEIT` | `Informationssicherheit` | Freigabe des Entra-Transfers |
| `GRUPPE_EXCHANGE` | `M365-Administration` | Aufgaben zum Postfachinhalt |
| `GRUPPE_TELEFONIE` | `M365-Telefonie` | Meldung an die Telefonie |
| `WIEDERHERSTELLUNG_INTERVALL` | `PT10M` | Takt, in dem der Stand der Wiederherstellung gelesen wird |
| `WIEDERHERSTELLUNG_RUNDEN` | `72` | danach geht eine Aufgabe an den Exchange-Betrieb |

## Personendaten

`quelle`, `ziel`, `begruendung`, die gelesenen Konten und Postfächer und der ObjectGUID sind
versiegelt (ADR-0314); Datensubjekt ist `recipient`. Die Engine vergleicht nichts davon. Was das
Szenario verlangt – gleiche Mail-Domain, schon transferiert, Quelle und Ziel verschieden, ein
Postfach vorhanden – vergleichen die Suchfilter des AD-Workers, und die Engine sieht nur den
Wahrheitswert (`foundVariable`). Adresslisten, UPNs und Kontingente entstehen in den Parametern, die
die Worker auswerten. Im Klartext stehen nur Gruppen-DNs und -IDs, Objekt-IDs der Cloud-Konten, die
entzogenen Geschäftsnummern (`rufnummer`) und der Stand der Wiederherstellung (`wiederherstellung`),
weil die Engine auf sie hin entscheidet.

## Nach der Ausführung

Die Quelle trägt in `extensionAttribute15` «Ressourcen transferiert <Datum> (Auftrag …)». Solange
der Vermerk steht, lehnt die Regel jede weitere Übertragung von diesem Konto ab. Zurücksetzen kann
ihn nur ein AD-Administrator (Attribut leeren); Atlas bietet dafür bewusst keine Aktion an.

## Voraussetzungen

1. Die Worker `ad_verzeichnis` (mit «Formulare dürfen suchen» für die Personensuche),
   `ucc_exchange_onprem` oder `ucc_exchange` (je nach `POSTFACH_SYSTEM`), `ucc_entra` und
   `ucc_teams`. Wer andere Namen verwendet, ersetzt sie im Attribut `connector` der `.bpmn`.
2. Das AD-Konto des Workers darf unter `AD_PERSONEN_OU` `userPrincipalName`, `displayName`,
   `extensionAttribute10` und `extensionAttribute15` schreiben und unter `AD_GRUPPEN_OU` Mitglieder
   eintragen. `extensionAttribute*` gibt es mit dem Schema von Exchange.
3. Exchange Server: `Disable-Mailbox`, `Set-Mailbox` und `New-MailboxRestoreRequest`. Microsoft
   nennt für die Wiederherstellung keine Rolle; welche sie enthält, zeigt
   `Get-ManagementRole -Cmdlet New-MailboxRestoreRequest`.
4. Entra ID: `User.ReadWrite.All`, `GroupMember.ReadWrite.All`, `Directory.Read.All` (für
   `list-user-groups`) und für den Entra-Transfer `User.DeleteRestore.All`
   (`docs/entra-permissions.md`).
5. Das Secret `atlas` mit einem API-Token der Rolle operator.

## Installation

```sh
ATLAS_TOKEN=atlasat_… ./installieren.sh https://atlas.intern.example.ch antworten.json
```

lädt das Entscheidungsmodell, importiert und veröffentlicht die Applikation (Format 4, mit
Informationsmodell und Architektur; `LANDKARTE=uebernehmen|weglassen|fragen`) und importiert den
Katalog mit der Zielgruppe aus `antworten.json`. `installieren.ps1` tut dasselbe unter Windows. In der
M365-Testumgebung installiert `deploy/opentofu/m365-devenv/scripts/install-informationstransfer.py`
das Paket mit den DNs und Gruppen der Umgebung.

## In der Testumgebung

Die gemeinsame Testumgebung (`deploy/opentofu/m365-devenv`) hat einen AD-Wald, einen Exchange Server
SE mit Postfächern für die Testkonten und einen Tenant mit Cloud-Konten derselben UPN – aber **kein
Entra Connect**. AD und Entra ID sind dort zwei getrennte Verzeichnisse. Daraus folgt:

- Mit `POSTFACH_SYSTEM = exchange-server` laufen Adresse, Inhalt und Kontingent gegen den Exchange
  Server, mit `exchange-online` gegen die Cloud-Postfächer.
- Der Entra-Transfer kann den Hard Match nicht vollenden: Das wiederhergestellte Konto bleibt ein
  Cloud-Konto mit gesetzter `onPremisesImmutableId`, das Protokoll sagt «Hard Match offen». Der
  Installer lässt `ENTRA_TRANSFER` auf `aus`. Wer ihn dort trotzdem versucht, löscht ein Testkonto
  endgültig; dafür gibt es die Freigabe und `NAME_PREFIX`.
- Nicht in einem echten Tenant oder auf einem echten Exchange Server ausgeführt sind:
  `disable-mailbox` und `restore-mailbox-content` (insbesondere, wie schnell das Postfach als
  abgetrennt gilt und in welcher Form der Status zurückkommt), `restore-user`,
  `permanently-delete-user`, `list-user-groups`, das Format der Kontingente und Adresslisten, wie
  der Exchange-Worker sie liefert, und der Hard Match mit Entra Connect.

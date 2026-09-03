# Google-Sheets-Demo: Antragseingang 📊

Zwei Prozesse, die den Google-Sheets-Worker-Typ
([ADR-0235](../../docs/adr/0235-google-sheets-worker.md))
an einer echten Tabelle zeigen — den ausgehenden Weg und die Gegenrichtung, in der
eine **neue Zeile** einen Prozess *startet*
([ADR-0234](../../docs/adr/0234-google-inbound-watch.md)).

| Datei | Zweck |
|---|---|
| `google-sheets-verbindungstest.bpmn` | Vier Tasks: Tabelle anlegen, Kopfzeile schreiben, Zeilen anhängen, zurücklesen. Zuerst starten. |
| `antragseingang.bpmn` | Eine neue Zeile startet den Prozess; er entscheidet und schreibt das Ergebnis in dieselbe Zeile zurück. |

## Einrichtung

### 1. Dienstkonto anlegen

In der [Google Cloud Console](https://console.cloud.google.com/) ein Projekt wählen,
die **Google Sheets API** und die **Google Drive API** aktivieren, dann
*IAM & Verwaltung → Dienstkonten → Dienstkonto erstellen*. Beim fertigen Konto
*Schlüssel → Schlüssel hinzufügen → JSON* — die Datei, die dabei heruntergeladen wird,
enthält alles Weitere.

### 2. Secret anlegen

*Console → Secrets* → neues Secret, z. B. `google_sheets_auth`, Wert als JSON.
Die beiden Felder stammen wörtlich aus der Schlüsseldatei (`client_email` und
`private_key`), hier nur in camelCase:

```json
{
  "method": "serviceAccount",
  "clientEmail": "atlas@dein-projekt.iam.gserviceaccount.com",
  "privateKey": "-----BEGIN PRIVATE KEY-----\nMIIE…\n-----END PRIVATE KEY-----\n"
}
```

Die Zeilenumbrüche im Schlüssel bleiben als `\n` stehen — genau so steht es auch in
der Schlüsseldatei. `tokenUrl` und `scope` füllt Atlas selbst aus. Wer als
Workspace-Benutzer handeln will (domänenweite Delegierung), ergänzt `"subject"`.

Für ein privates Google-Konto ohne Cloud-Projekt geht auch
`{"method": "refreshToken", "clientId": …, "clientSecret": …, "refreshToken": …}`.

### 3. Worker anlegen

*Console → Workers → Neuer Worker*: Kind **Google Sheets**, Name `google`,
Credential reference `google_sheets_auth`. Ein Endpoint wird nicht gebraucht —
Googles API-Adressen sind für alle dieselben.

### 4. Das Wichtigste: freigeben

**Ein Dienstkonto besitzt von sich aus nichts.** Es hat eine eigene E-Mail-Adresse und
sieht genau die Dateien, die mit dieser Adresse geteilt sind. Wer das übergeht, sieht
ein Dokument vor sich, das der Worker mit `403` beantwortet.

- Für den Verbindungstest ist nichts zu teilen: er legt die Tabelle selbst an und
  besitzt sie damit.
- Für den Antragseingang die Antragstabelle (bzw. den Ordner) mit der Adresse des
  Dienstkontos teilen, als **Bearbeiter** — der Prozess schreibt zurück.

## Verbindungstest

`google-sheets-verbindungstest.bpmn` deployen und mit `tabellenname` starten. Läuft er
durch, stehen in `datei` die neue Tabelle und in `zeilen` die beiden Zeilen, die der
Prozess selbst angehängt hat — als Liste von **Objekten**, weil der Lesetask
`header="true"` setzt. Die Tabelle liegt im Drive des Dienstkontos;
`=datei.spreadsheetUrl` führt hin (zum Ansehen die Tabelle mit der eigenen Adresse
teilen).

Er zeigt zugleich die drei Wertformen, die `values` annimmt: eine Liste von Listen
(Zeilen), eine Liste von Objekten (mit `columns` als Spaltenreihenfolge) und einen
einzelnen Wert (eine Zelle).

## Antragseingang

Eine Tabelle mit einem Blatt `Antraege` und der Kopfzeile
`Antragsnummer | Name | Status | Betrag` anlegen — oder ein Google-Formular
erstellen und seine Antworten in ein Blatt schreiben lassen, was derselbe Fall ist.

Dann *Console → Workers → beim Google-Worker `⇄ Events`*:

| Feld | Wert |
|---|---|
| Watch | neue Zeilen in einer Tabelle |
| Spreadsheet | die Tabellen-URL, ganz eingefügt |
| Range | `Antraege!A:D` |
| Die erste Zeile benennt die Spalten | ✔ |
| Message name | `antrag.eingegangen` |

`antragseingang.bpmn` deployen, eine Zeile in die Tabelle schreiben, und innerhalb
einer Minute läuft eine Instanz. In der Statusspalte steht danach `Pruefung` oder
`genehmigt` — der Prozess schreibt in **seine eigene Zeile** zurück, weil der Watch
`rowNumber` mitgibt.

Der Korrelationsschlüssel darf `= Antragsnummer` sein, weil die Kopfzeile die Spalten
benennt; ohne Kopfzeile stünde dort `= row[1]`.

## Was man wissen sollte, bevor es produktiv geht

- **Zustellung ist „mindestens einmal", und eine Tabelle hat keinen
  Idempotenzschlüssel.** Stürzt Atlas zwischen „Google hat die Zeile angehängt" und
  „Job fertig" ab, wird beim Wiederholen erneut angehängt und die Zeile steht zweimal
  da. Google bietet nichts dagegen an. Wo das nicht tragbar ist: eine Merkspalte
  schreiben und vor dem Anhängen lesen — genau das Muster, das `antragseingang.bpmn`
  mit der Statusspalte vorführt.
- **Ein Zeilen-Watch folgt der Zeilennummer.** Er sieht angehängte Zeilen, und genau
  das tut ein Formular. Werden Zeilen im beobachteten Bereich **gelöscht**, rutscht
  der Rest hoch, und eine später angehängte Zeile auf einer schon gelieferten Nummer
  wird nicht erneut geliefert. Ein Ordner-Watch (neue Dateien in einem Drive-Ordner)
  merkt sich jede Datei einzeln und kann das nicht.
- **Löschen heisst Papierkorb**, nie endgültig: `delete-spreadsheet` setzt
  `trashed`, was der Eigentümer rückgängig machen kann.
- **Der Scope entscheidet, welche Hälfte funktioniert.** `drive.file` statt `drive`
  reicht, solange der Prozess jede Tabelle selbst anlegt; eine Tabelle, die ein Mensch
  mit dem Dienstkonto teilt, ist damit nicht erreichbar.

# SharePoint-Demo: Fallakte

Zwei Prozesse, die die Listen- und Bibliotheksoperationen des SharePoint-Worker-Typs
([ADR-0141](../../docs/adr/0141-sharepoint-connector.md), ergänzt 2026-10-22) an einem
zusammenhängenden Fall zeigen: Ein Antrag wird in einer SharePoint-Liste erfasst, atlas legt
ihm eine Fallakte an, und wenn er erledigt ist, räumt ein zweiter Prozess sie wieder ab.

| Datei | Zweck | Operationen |
|---|---|---|
| `sharepoint-fallakte.bpmn` | Aus einem Antrag eine Fallakte machen: Ordner, ausgefüllte Eingangsbestätigung, nachgeführter Antrag, Protokolleintrag. | `get-item`, `create-folder`, `download-file`, `get-file`, `upload-file`, `update-item`, `create-item` |
| `sharepoint-aufraeumen.bpmn` | Erledigte Anträge seitenweise finden und zu jedem den Fallordner und den Eintrag löschen. | `list-items` (Filter, Seiten), `delete-file`, `delete-item` |

`list-folder` fehlt in den beiden Abläufen; es blättert wie `list-items`, mit `pageToken`
und `nextPageToken`.

## Was in der Site vorhanden sein muss

Die Spalten werden mit ihrem **internen Namen** angesprochen — dem Namen, den SharePoint beim
Anlegen vergibt und der sich beim Umbenennen nicht ändert.

| Wo | Was |
|---|---|
| Liste `Antraege` | `Title`, `Kunde` (Text), `Betrag` (Zahl), `Status` (Auswahl: `Neu`, `In Bearbeitung`, `Erledigt`), `Dringend` (Ja/Nein), `Fallordner` (Text, eine Zeile) |
| Bibliothek `Fallakten` | `Kunde` (Text), `AntragNr` (Zahl) |
| Bibliothek `Vorlagen` | die Datei `eingangsbestaetigung.txt` mit den Platzhaltern `%KUNDE%` und `%NUMMER%` |
| Liste `Protokoll` | `Title`, `AntragNr` (Zahl) |

Die Adresse der Site und die Namen der Listen stehen an **einer** Stelle, im Script-Task
«Ablageort festlegen» am Anfang beider Prozesse. Wer das Beispiel auf die eigene Site umstellt,
ändert nur diese Zeile.

## Einrichtung

1. **App-Registrierung** in Entra ID mit `Sites.Selected` (und einer Freigabe dieser App auf
   die Site) oder `Sites.ReadWrite.All`, Administratorzustimmung, dazu ein Client-Secret oder —
   für eine App, die unbeaufsichtigt läuft, empfohlen — ein Zertifikat.
2. **Secret** unter *Console → Secrets*, z. B. `sharepoint_auth`:

   ```json
   {"method": "clientCredentials", "tenantId": "…", "clientId": "…", "clientSecret": "…"}
   ```

   oder mit `"certificate": "-----BEGIN CERTIFICATE-----…-----BEGIN PRIVATE KEY-----…"` an
   Stelle von `clientSecret`.
3. **Worker** unter *Console → Workers → New worker*: Typ *Microsoft SharePoint*, Name
   `intranet`, Credential-Referenz `sharepoint_auth`, kein Endpoint.

Die Schritte im Detail stehen im Handbuch unter *Runbooks → SharePoint*.

## Ausprobieren

Einen Antrag in der Liste `Antraege` anlegen, seine ID ablesen (Spalte *ID* in der
Listenansicht einblenden) und den Prozess mit der Variablen `antragId` starten — unter
*Operations* oder über die API:

```bash
BASE=http://localhost:8080

KEY=$(curl -s -X POST $BASE/api/v1/deployments \
  -H 'Content-Type: application/xml' \
  --data-binary @sharepoint-fallakte.bpmn | python3 -c 'import sys,json;print(json.load(sys.stdin)["key"])')

curl -s -X POST $BASE/api/v1/processes/$KEY/instances \
  -H 'Content-Type: application/json' -d '{"variables": {"antragId": "17"}}'
```

`antragId` ist eine Zeichenkette, weil Graph die IDs von Listeneinträgen als Text liefert.

Danach liegt in `Fallakten/Faelle/17` die ausgefüllte Eingangsbestätigung, der Antrag steht
auf *In Bearbeitung*, `Dringend` ist gesetzt, wenn der Betrag über 10 000 liegt, und die
Liste `Protokoll` hat einen Eintrag mehr. Einen Antrag auf *Erledigt* setzen und
`proc_sharepoint_aufraeumen` ohne Variablen starten: Ordner und Eintrag verschwinden in den
Papierkorb der Site.

## Was das Modell vormacht

- **Typisierte Spaltenwerte.** `=antrag.fields.Betrag > 10000` kommt in der Ja/Nein-Spalte
  als Ja oder Nein an, `=number(antragId)` in der Zahlenspalte als Zahl. Ein Literalwert wie
  `In Bearbeitung` bleibt Text. Ein Ausdruck, der `null` ergibt, leert die Spalte.
- **Wiederholbar, ausser einem Schritt.** Ein Ordner, der schon da ist, gilt als angelegt; ein
  Brief, der schon abgelegt ist, wird nicht überschrieben (`get-file` → `exists`); ein
  Löschauftrag ohne Gegenstand meldet `deleted=false`. Nur `create-item` — das Protokoll —
  entsteht bei einem zweiten Lauf ein zweites Mal.
- **Die Null-Falle in FEEL.** Fehlt die Spalte `Kunde`, ergibt `replace(…, null)` den Wert
  `null` statt eines Textes; das `if` im Script «Brief ausfüllen» fängt das ab.
- **Blättern mit dem Token.** `pageToken="=seite.nextPageToken"` ist beim ersten Durchgang
  `null` und liest die erste Seite; danach setzt er hinter der vorigen fort.
- **Erst der Ordner, dann der Eintrag.** Bricht das Aufräumen dazwischen ab, findet der nächste
  Lauf den Antrag wieder.

Beide Abläufe laufen in `connector/sharepoint/example_test.go` gegen eine nachgebildete Site
durch — mit dem echten Compiler, der echten Engine und dem echten Graph-Client.

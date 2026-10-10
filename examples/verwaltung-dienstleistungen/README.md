# Dienstleistungen der Verwaltung – ein ganzer Shop

Das durchgehende Beispiel des [Shop-Handbuchs](../../api/web/shop-handbuch.html):
zwei Kataloge einer Verwaltung, drei Produkte, ihre Prozesse in beiden Formen und
ihre Formulare, zweisprachig (Deutsch und Englisch).

| Datei | Inhalt |
|---|---|
| `atlas.json` | Das Manifest der Applikation «Beispiel: Dienstleistungen der Verwaltung» im Quellbaum-Format (ADR-0134): Schlüssel, Name, Prozesse und Formulare |
| `fragen.json` | Was jeder Platzhalter im Katalogdokument fragt, auf Deutsch und Englisch |
| `katalog.json` | Das Katalogdokument für `POST /api/v1/catalogs/import`: «Dienstleistungen der Verwaltung» (Rang 100) und «Fachbereich Bau» (Rang 110), mit Zutrittsbadge, Parkplatz und Geoportal-Zugang |
| `zutrittsbadge-lebenszyklus.bpmn` | Der Badge als **ein Lebenszyklus-Prozess** mit vier Nachrichtenstarts: ausgeben, sperren (Änderung), Ersatz (Service), einziehen |
| `parkplatz-zuteilen.bpmn`, `parkplatz-freigeben.bpmn` | Der Parkplatz mit **getrennten Prozessen** für Bereitstellen und Entziehen |
| `geoportal-einrichten.bpmn`, `geoportal-entfernen.bpmn` | Der Geoportal-Zugang, ebenfalls mit getrennten Prozessen |
| `vd-*.form.json` | Das Bestellformular des Parkplatzes, das Formular der Aktion «Badge sperren» und die Formulare der Aufgaben |

## Was offen bleibt

Das Dokument kennt die Installation nicht, in die es importiert wird. Vier Werte
stehen deshalb als Platzhalter darin, und `fragen.json` sagt, wonach jeder fragt:
die Zielgruppe jedes Katalogs (`{{zielgruppe}}`, `{{zielgruppe-bau}}`), wer
Parkplätze genehmigt (`{{facility-management}}`, eine Person) und wer
Geoportal-Zugänge genehmigt (`{{geoportal-verantwortliche}}`, eine Gruppe).

## Installieren

Im Browser über den Installer im Shop-Handbuch (Abschnitt «Das Beispiel
installieren»), oder im Terminal und in einer Pipeline:

```sh
atlas import --server https://atlas-test.example.com --answers antworten.json \
  examples/verwaltung-dienstleistungen
```

mit `antworten.json`:

```json
{ "zielgruppe": "Alle Mitarbeitenden", "zielgruppe-bau": "Fachbereich Bau",
  "facility-management": "Fabienne Meier", "geoportal-verantwortliche": "Geoportal-Team" }
```

Gruppen und Personen dürfen mit ID oder Namen angegeben werden; Namen gelten auf
jeder Instanz gleich. Fehlt eine Antwort oder ist sie unbekannt, schreibt der
Befehl nichts. Das Token (`--token` oder `ATLAS_TOKEN`) braucht die Rollen
`modeler` und `productmanager`.

## Was es voraussetzt

- **Ein API-Token mit der Rolle `operator`**, im Vault unter `atlas` oder in
  `ATLAS_CONNECTOR_ATLAS_TOKEN`: Die Prozesse melden ihre Position über
  `POST /api/v1/orders/{orderId}/lines/{positionId}`.
- **Für die Badge-Genehmigung** (Vorgesetzte) einen Verzeichnis-Worker namens
  `verzeichnis`; zum Ausprobieren genügt der AD-Mock.

## Was die Tests prüfen

`examples/shopcatalog_test.go` prüft das Dokument ohne Server, wie der Import es
prüfen wird: das Dokument selbst, die Veröffentlichung jedes Katalogs, die
Lebenszyklus-Bindungen gegen die hier kompilierten Prozesse (Nachrichtenstarts,
Shop-Aufgaben für Änderung und Service), dass jedes genannte Formular hier liegt,
dass `fragen.json` jeden Platzhalter abfragt und dass `atlas.json` genau die
Prozesse und Formulare dieses Verzeichnisses unter ihren eigenen IDs nennt.
`cmd/atlas/importpkg_test.go` installiert das Paket mit `atlas import` in einen
echten Server, zweimal.

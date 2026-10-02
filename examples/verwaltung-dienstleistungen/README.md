# Dienstleistungen der Verwaltung – ein ganzer Shop

Das durchgehende Beispiel des [Shop-Handbuchs](../../api/web/shop-handbuch.html):
zwei Kataloge einer Verwaltung, drei Produkte, ihre Prozesse in beiden Formen und
ihre Formulare, zweisprachig (Deutsch und Englisch).

| Datei | Inhalt |
|---|---|
| `katalog.json` | Das Katalogdokument für `POST /api/v1/catalogs/import`: «Dienstleistungen der Verwaltung» (Rang 100) und «Fachbereich Bau» (Rang 110), mit Zutrittsbadge, Parkplatz und Geoportal-Zugang |
| `zutrittsbadge-lebenszyklus.bpmn` | Der Badge als **ein Lebenszyklus-Prozess** mit vier Nachrichtenstarts: ausgeben, sperren (Änderung), Ersatz (Service), einziehen |
| `parkplatz-zuteilen.bpmn`, `parkplatz-freigeben.bpmn` | Der Parkplatz mit **getrennten Prozessen** für Bereitstellen und Entziehen |
| `geoportal-einrichten.bpmn`, `geoportal-entfernen.bpmn` | Der Geoportal-Zugang, ebenfalls mit getrennten Prozessen |
| `vd-*.form.json` | Das Bestellformular des Parkplatzes, das Formular der Aktion «Badge sperren» und die Formulare der Aufgaben |

## Was offen bleibt

Das Dokument kennt die Installation nicht, in die es importiert wird. Vier Werte
stehen deshalb als Platzhalter darin, und der Installer im Shop-Handbuch fragt sie
ab: die Zielgruppe jedes Katalogs (`{{zielgruppe}}`, `{{zielgruppe-bau}}`), wer
Parkplätze genehmigt (`{{facility-management}}`, eine Person) und wer
Geoportal-Zugänge genehmigt (`{{geoportal-verantwortliche}}`, eine Gruppe).

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
Shop-Aufgaben für Änderung und Service), dass jedes genannte Formular hier liegt
und dass der Installer jeden Platzhalter abfragt.

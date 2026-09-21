# Produkt erfassen

Ein Produkt anzulegen heisst heute: die Konsole öffnen, ein Formular mit zwanzig
Feldern ausfüllen, speichern — und darauf vertrauen, dass jemand nachschaut.

Dieser Prozess macht dasselbe als Ablauf mit Aufgaben und gewinnt damit das, was
ein Formular nicht hat: eine Prüfung durch eine zweite Ansicht, bevor das Produkt
bestellbar wird, und eine Spur, wer was wann entschieden hat.

## Voraussetzung: die Verbindung `atlas`

Jeder Service-Task schreibt über Atlas' **eigene HTTP-API** zurück — mit dem
`rest`-Connector und einer Verbindung namens `atlas`. Das ist derselbe Weg, den
der mitgelieferte Auftragserfüllungsprozess
(`api/systemprocesses/auftrag-erfuellung.bpmn`) geht.

Diese Verbindung wird unter **Console → Workers** eingerichtet: ein Worker vom
Typ `rest` mit dem Namen `atlas`, dessen Basis-URL auf diesen Server zeigt und
dessen Credential ein Zugang mit Katalogpflege-Recht ist.

**Ohne sie parken die Service-Tasks.** Sie scheitern nicht und sie verlieren
nichts — sie warten, bis die Verbindung da ist, und laufen dann weiter. Das ist
der Grund, warum dieses Beispiel im Handbuch keinen „Ausführen"-Knopf hat: er
würde im ersten Schritt einen Token parken und nichts zeigen.

Zwei Gruppen kommen vor: `katalogpflege` für die Erfassung und `administration`
für das Erscheinungsbild.

## Ablauf

| Schritt | Art | API |
|---|---|---|
| Kataloge holen | Service | `GET /api/v1/catalogs` |
| Katalog wählen oder anlegen | Aufgabe | Formular `pe-katalog` |
| Katalog anlegen | Service | `POST /api/v1/catalogs` |
| Erscheinungsbild festlegen | Aufgabe | Formular `pe-theme`, Gruppe `administration` |
| Theme setzen | Service | `PUT /api/v1/catalogs/{id}/theme` |
| Produktdaten erfassen | Aufgabe | Formular `pe-produkt` |
| Produkt als Entwurf sichern | Service | `POST /api/v1/catalog-products` (`state: draft`) |
| Bestehende Produkte holen | Service | `GET /api/v1/catalog-products` |
| Produkt zusammenstellen | Aufgabe | Formular `pe-assemble` |
| Katalog frisch lesen | Service | `GET /api/v1/catalogs/{id}` |
| Angebot und Zusammenstellung schreiben | Service | `PATCH /api/v1/catalogs/{id}` |
| Kommerzielle Sicht | Aufgabe | Formular `pe-preise` |
| Entwurf mit Preis sichern | Service | `POST /api/v1/catalog-products` |
| Produkt verifizieren | Aufgabe | Formular `pe-pruefen` |
| Produkt aktiv setzen | Service | `POST /api/v1/catalog-products` (`state: active`) |
| Katalog publizieren? | Aufgabe | Formular `pe-publizieren` |
| Katalog publizieren | Service | `POST /api/v1/catalogs/{id}/releases` |

## Drei Entscheidungen

### Das Produkt wird gesichert, bevor es zusammengestellt wird

Aufgeschrieben würde man es andersherum: erst alles erfassen, dann speichern.
Das geht nicht. Die Zusammenstellung sind **Kanten am Katalog**, die auf die
Produkt-ID zeigen, und der Katalog weist eine ID zurück, zu der kein Produkt
existiert — sonst erschiene die Absage erst bei der nächsten Freigabe, als
`unknown item`, weit weg von der Ursache.

Der Zustand `draft` trägt dabei genau das, was er soll: das Produkt ist da,
sichtbar in der Pflege, und nicht bestellbar, bis es geprüft ist.

### Vor dem Schreiben wird der Katalog frisch gelesen

Der Schritt sieht überflüssig aus und ist der wichtigste im Prozess.

Ein `PATCH` ersetzt `items` und `edges` **als Ganzes**. Zwischen der Maske und
dem Speichern liegen Minuten, in denen jemand anderes ein Produkt hinzugefügt
haben kann. Ohne den frischen Stand schriebe der Prozess einen veralteten zurück
und das fremde Produkt wäre weg — ohne Fehler und ohne Spur. Genau davor warnt
der Kommentar am Feld `Catalog.Revision`.

Deshalb wird die `revision` mitgeführt: sie macht aus einem lautlosen
Überschreiben eine Absage, die jemand sieht.

### Das Erscheinungsbild ist eine eigene Aufgabe

Ein Katalog-Theme setzt in Atlas die **Administration** und nicht die
Katalogpflege (ADR-0316, Entscheidung 12); der Endpunkt antwortet sonst mit
`403`. Der Prozess umgeht das nicht, er bildet es ab.

## Was dieses Beispiel nicht tut

**Das Logo.** Es gehört zum Erscheinungsbild und ist trotzdem nicht hier: ein
Logo ist eine Datei, und ein Aufgabenformular trägt heute nur Text — die
Dateiauswahl von form-js wird als Text gelesen und ist für den CSV-Import
verdrahtet. Das Theme-Formular sagt, wo das Logo stattdessen gesetzt wird.

**Mehr als zwei Sprachen.** Die Formulare fragen Deutsch und Französisch. Ein
statisches Formular kann die Sprachliste des Katalogs nicht lesen, also braucht
ein Katalog mit weiteren Sprachen je ein Feldpaar mehr in `form-pe-produkt.json`
und `form-pe-pruefen.json`. Wer das oft braucht, baut die Masken besser als eine
Konsolenansicht als als ein Formular.

**Ein Vier-Augen-Prinzip.** Die Verifizierung trägt dieselbe Gruppe wie die
Erfassung, also darf dieselbe Person beides tun. Eine andere Gruppe dort, und
niemand gibt das eigene Produkt frei. Das ist eine Frage der Organisation, und
ein Beispiel sollte sie nicht für jemanden beantworten.

**Produktbilder und Varianten.** Beides kennt der Katalog; beides ist hier
weggelassen, damit der Ablauf lesbar bleibt. Varianten sind ein Textfeld mehr im
selben Muster, das Bild hat dasselbe Dateiproblem wie das Logo.

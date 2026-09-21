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
| Bestehende Produkte holen | Service | `GET /api/v1/catalog-products` |
| **Produkt erfassen** | **Aufgabe** | Formular `pe-erfassung` — Katalog, Produktdaten, Zusammenstellung und Preise in **einer** Maske |
| Katalog anlegen (nur bei „neu") | Service | `POST /api/v1/catalogs` |
| Produkt als Entwurf sichern | Service | `POST /api/v1/catalog-products` (`state: draft`) |
| **Produkt verifizieren** | **Aufgabe** | Formular `pe-pruefen` — zurück zur Erfassung oder freigeben |
| Produkt aktiv setzen | Service | `POST /api/v1/catalog-products` (`state: active`) |
| Katalog frisch lesen | Service | `GET /api/v1/catalogs/{id}` |
| Angebot und Zusammenstellung schreiben | Service | `PATCH /api/v1/catalogs/{id}` |
| Erscheinungsbild (nur bei „neu") | Aufgabe | Formular `pe-theme`, Gruppe `administration` |
| Theme setzen | Service | `PUT /api/v1/catalogs/{id}/theme` |
| Katalog publizieren? | Aufgabe | Formular `pe-publizieren` |
| Katalog publizieren | Service | `POST /api/v1/catalogs/{id}/releases` |

## Vier Entscheidungen

### Die Erfassung ist eine Aufgabe und nicht fünf

Katalogwahl, Produktdaten, Zusammenstellung und Preise sind dieselbe Arbeit
derselben Person in derselben Sitzung. Fünf Aufgaben daraus zu machen hiesse
viermal beanspruchen, abschliessen und auf die nächste warten — ein Assistent,
der sich wie ein Behördengang anfühlt, und langsamer als das Konsolenformular,
das er ersetzen soll.

Der Gewinn dieses Prozesses liegt woanders und bleibt vollständig erhalten: in
der **Verifizierung**, die eine echte zweite Station ist, mit einer Entscheidung
und einer Spur.

### Der Katalog wird erst nach der Freigabe beschrieben

Ein abgelehntes Produkt gelangt damit gar nicht erst ins Angebot. Sonst stünde
ein Entwurf in der Item-Liste des Katalogs, und jede Freigabe scheiterte an ihm
(`state is draft, not active`), bis jemand ihn von Hand entfernt.

Es hat eine zweite Wirkung, die genauso wichtig ist: der Schreibvorgang läuft
**genau einmal**, auch wenn die Verifizierung mehrfach zurückschickt — die
Kanten können sich nicht vervielfachen.

Das Produkt selbst wird trotzdem vor der Prüfung gesichert, als Entwurf: es muss
existieren, damit die Verifizierung etwas Reales vor sich hat, und `draft` trägt
genau das — vorhanden, nicht bestellbar.

### Vor dem Schreiben wird der Katalog frisch gelesen

Der Schritt sieht überflüssig aus und ist der wichtigste im Prozess.

Ein `PATCH` ersetzt `items` und `edges` **als Ganzes**. Zwischen der Maske und
dem Speichern liegen Minuten bis Tage, in denen jemand anderes ein Produkt
hinzugefügt haben kann. Ohne den frischen Stand schriebe der Prozess einen
veralteten zurück und das fremde Produkt wäre weg — ohne Fehler und ohne Spur.
Genau davor warnt der Kommentar am Feld `Catalog.Revision`.

Deshalb wird die `revision` mitgeführt: sie macht aus einem lautlosen
Überschreiben eine Absage, die jemand sieht.

### Das Erscheinungsbild ist eine eigene Aufgabe, und sie liegt hinten

Ein Katalog-Theme setzt in Atlas die **Administration** und nicht die
Katalogpflege (ADR-0316, Entscheidung 12); der Endpunkt antwortet sonst mit
`403`.

Sie liegt hinter der Freigabe des Produkts statt davor. Vorne stehend liesse sie
eine Produkterfassung auf die Farbwahl einer Administratorin warten. Hier wartet
nur die Publikation darauf — und ein neuer Katalog geht nicht ungebrandet live.

### Drei Fallen im FEEL, die im Modell gelöst sind

Sie sind erwähnenswert, weil sie stillschweigend das Falsche tun statt zu
scheitern, und weil jede beim Nachbauen wieder auftritt:

- **`append` verkettet keine Listen.** `append(liste, andereListe)` hängt die
  zweite als *ein Element* an und erzeugt verschachtelte Kanten, die der Katalog
  nicht lesen kann. Richtig ist `concatenate`.
- **`split` lässt Leerzeichen stehen.** `split("de, fr", ",")` ergibt
  `["de", " fr"]`. Die Suchbegriffe werden deshalb mit
  `replace(s, "^\s+|\s+$", "")` beschnitten und leere Einträge fallen weg.
  Die Sprachen umgeht das Problem ganz: sie sind eine Auswahlliste im Formular
  und damit schon eine Liste.
- **Ein Filter über eine Liste von Kontexten filtert nicht.** `edges[from != x]`
  gibt in diesem Build *alles* zurück, statt zu filtern oder zu scheitern.
  Darauf beruht keine Zeile hier — der Katalog wird stattdessen genau einmal
  geschrieben, sodass nichts zu entfernen ist.

Eine nicht gesetzte Variable liest sich in FEEL als `null`. Darauf beruhen zwei
Bedingungen: `katalogId = null` erkennt den ersten Durchlauf, und
`katalogNeu = true` ist falsch, wenn nie ein Katalog angelegt wurde.

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

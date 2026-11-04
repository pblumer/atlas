# AGENTS.md: Katalogprodukte anlegen

Dieser Leitfaden richtet sich an KI-Agenten und Personen, die Produktkataloge in Atlas pflegen, typischerweise über die MCP-Werkzeuge `atlas_*_catalog*`. Er beschreibt, wie man Katalogprodukte anlegt, welcher Kategorie und Produktgruppe man sie zuordnet, was eine Marktleistung ausmacht und was ein Service ist.

Die Entscheidung dahinter steht in [ADR-draft-marktleistung-and-service](../adr/draft-marktleistung-and-service.md). Für Agenten, die am Atlas-Code arbeiten, gilt das [AGENTS.md im Wurzelverzeichnis](../../AGENTS.md).

## 1. Begriffe

| Begriff | Definition | In Atlas |
|---|---|---|
| **Kategorie** | Die oberste Ebene einer Capability, zum Beispiel «Arbeitsalltag bewältigen». | `category` einer Marktleistung, enthält den **Key einer Capability** |
| **Produktgruppe** | Eine untergeordnete Capability der Kategorie, zum Beispiel «Identität und Zugang». «Untergeordnet» heisst: Die Kategorie-Capability führt sie in `requires`. | `productGroup` einer Marktleistung, enthält den **Key einer Capability** |
| **Marktleistung** | Wie ein Legohaus eine Zusammenstellung von Services. Das Einzige, was der Shop anbietet und eine Person bestellt. | `kind: "offering"` |
| **Service** | Wie ein Legobaustein. Er hat immer einen eigenen Lifecycle-Prozess, wird autonom provisioniert und ist in einer Marktleistung enthalten oder dort optional wählbar. | `kind: "service"` |

Die Kette im Shop lautet: **Kategorie › Produktgruppe › Marktleistung › Services**.

## 2. Regeln, die Atlas beim Publizieren prüft

Die Regeln gelten für Kataloge mit `structured: true`. Ein Verstoss verhindert das Publizieren, und die Fehlermeldung nennt das betroffene Produkt.

| Gegenstand | Regel |
|---|---|
| Jedes Produkt | Es trägt `kind` mit dem Wert `offering` oder `service`. Andere Werte werden auch in nicht strukturierten Katalogen abgelehnt. |
| Jedes Produkt | Es hat einen Lifecycle: `provisionProcess` und `deprovisionProcess` oder `lifecycleProcess`. |
| Marktleistung | Sie ist in keinem anderen Produkt enthalten. |
| Marktleistung | Sie enthält mindestens einen Service als `composition`. |
| Marktleistung | Sie enthält ausschliesslich Services, also keine verschachtelte Marktleistung. |
| Marktleistung | `category` und `productGroup` sind gesetzt. Beide sind Keys im Capability-Register, keine ist `deprecated`, und die Kategorie führt die Produktgruppe in `requires`. |
| Service | Er hat keine Teile (keine ausgehende `composition` oder `aggregation`). |
| Service | Er trägt weder Kategorie noch Produktgruppe noch Übersetzungen davon. |
| Service | Mindestens eine Marktleistung desselben Katalogs enthält ihn. |
| Bestellung | Ein Service wird nicht einzeln bestellt. Ausnahme: als Option einer Marktleistung, die die Person bereits besitzt. |

## 3. Vorgehen beim Anlegen einer Marktleistung

1. **Capability klären.** Mit `atlas_list_capabilities` (Tag `katalog:kategorie` bzw. `katalog:produktgruppe`) prüft man, ob Kategorie und Produktgruppe existieren. Fehlt eine Produktgruppe, legt man sie mit `atlas_create_capability` an. Danach nimmt man ihren Key in `requires` der Kategorie auf (`atlas_save_capability`).
2. **Services zuerst anlegen.** Für jeden Baustein speichert man mit `atlas_save_catalog_product` ein Produkt mit `kind: "service"`, Namen in allen Katalogsprachen und gebundenem Lifecycle-Prozess. Kategorie und Produktgruppe bleiben leer.
3. **Marktleistung anlegen.** Man speichert ein Produkt mit `kind: "offering"`, `category` und `productGroup` als Capability-Keys sowie einem Orchestrierungsprozess (Abschluss/Rückbau).
4. **Struktur zeichnen.** Mit `atlas_update_catalog` nimmt man alle Produkte in `items` auf und setzt die Kanten (`edges`):
   - `composition`: Der Service ist fester Bestandteil der Marktleistung und wird immer mitbestellt.
   - `aggregation`: Der Service ist optional und wird angekreuzt.
   - `requires`: Ein Service wird erst provisioniert, wenn ein anderer bereitsteht, zum Beispiel Betriebssystem nach Hardware.
   - `excludes`: Zwei Produkte dürfen nie gleichzeitig gehalten werden.
5. **Publizieren** mit `atlas_publish_catalog`. Jede gemeldete Ablehnung behebt man und publiziert erneut.

`atlas_save_catalog_product` ersetzt immer den ganzen Datensatz. Vor jeder Änderung liest man das Produkt deshalb mit `atlas_list_catalog_products` und sendet es mit der gelesenen `revision` vollständig zurück. Dasselbe gilt für `items` und `edges` eines Katalogs.

## 4. Empfehlungen (nicht von Atlas geprüft)

- **Ein Einzelprodukt ist eine Marktleistung mit einem Service.** Ein Funktionskonto wird zum Beispiel als Marktleistung `funktionsaccount` mit dem Service `funktionsaccount-konto` angelegt. Das erzeugt zwei Datensätze, hält aber die Trennung durch.
- **IDs:** Die Marktleistung trägt den fachlichen Namen (`laptop-dell`). Ein Service trägt den Namen der Marktleistung plus seine Rolle (`laptop-dell-hw`, `laptop-dell-os`) oder einen neutralen Namen, wenn er in mehreren Marktleistungen vorkommt (`huelle-laptop-14`). IDs lassen sich nicht umbenennen.
- **Keine doppelten Kanten.** Eine `requires`-Kante, die eine `composition` wiederholt (zum Beispiel Monitor erfordert Monitor-Hardware), trägt keine Information und gehört nicht in den Katalog.
- **`requires` bedeutet «und».** Eine Hülle, die gleichzeitig iPhone-Hardware *und* Android-Hardware erfordert, kann nie provisioniert werden. Passendes Zubehör hängt man stattdessen als `aggregation` an die jeweilige Marktleistung.
- **Ein eigener Prozess pro Service.** Ein generischer Prozess für viele gleichartige Services, etwa einer für alle Kontotypen, ist zulässig. Teilen sich zwei Bausteine einer Marktleistung denselben Prozess (Hardware und Betriebssystem), sind sie nicht unabhängig provisionierbar. Man prüft dann, ob es wirklich zwei Services sind.
- **Preis** steht an der Marktleistung. Ein Service trägt höchstens «im Preis der Marktleistung enthalten».

## 5. Capability-Struktur «Arbeitsalltag bewältigen»

Stand 2026-11-04, alle Capabilities im Zustand `proposed`:

| Ebene | Capability-Key | Name | Vorgesehene Marktleistungen im Demokatalog |
|---|---|---|---|
| Kategorie | `arbeitsalltag-bewaeltigen` | Arbeitsalltag bewältigen | – |
| Produktgruppe | `digitale-identitaet-bereitstellen` | Identität und Zugang | Benutzerkonto intern, Benutzerkonto extern, Admin-, Funktions-, Service-, Test-, Schulungs- und Rollen-/Ressourcenkonto |
| Produktgruppe | `endgeraet-bereitstellen` | Endgeräte | Laptop Dell, Laptop Apple |
| Produktgruppe | `arbeitsplatz-ausruesten` | Peripherie | Monitor 24", Monitor 32", Tastatur CH-DE |
| Produktgruppe | `mobile-kommunikation-ermoeglichen` | Mobile Kommunikation | iPhone, Android-Smartphone |
| Produktgruppe | `elektronische-kommunikation-ermoeglichen` | Mail und Telefonie | Mailbox, IP-Telefon |
| Produktgruppe | `software-bereitstellen` | Software | noch keine (Lücke) |
| Produktgruppe | `arbeitsplatz-support-leisten` | Support | noch keine (Lücke) |

## 6. Einen bestehenden Katalog umstellen

1. Man setzt bei allen Produkten `kind`. Einzelprodukte ergänzt man um einen Service (siehe Abschnitt 4).
2. Bei allen Services entfernt man Kategorie und Produktgruppe.
3. Bei allen Marktleistungen setzt man Kategorie und Produktgruppe auf Capability-Keys.
4. Man bereinigt die Kanten: doppelte `requires` entfernen, Marktleistungen in Marktleistungen auflösen.
5. Erst danach setzt man `structured: true` und publiziert. Bis dahin publiziert der Katalog nach den bisherigen Regeln.

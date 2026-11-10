# AD-Benutzer prüfen

Jedes Konto eines Active Directory an Geschäftsregeln messen, die in DMN stehen, genau
so, wie eine hochgeladene Datei gemessen wird
([ADR-0548](../../docs/adr/0548-bulk-validation-evaluates-a-decision-per-row-in-one-job.md),
Plan [AD-Suche als Dokument](../../docs/planning/ad-search-as-document.md), S2).

```
Start "Prüfung angestossen"
 → AD-Suche "Konten lesen"           Ergebnis als Dokument → benutzer = {found, count, dn, document}
 → Business-Rule-Task "Konten prüfen" über die Datei benutzer.document
                                      → pruefung = {total, valid, invalid, errors, failures, result, …}
 → Exklusiv-Gateway "Befunde?"
      pruefung.invalid + pruefung.errors > 0 → Ende "Befunde abgelegt"
      sonst (default)                        → Ende "Alle Konten in Ordnung"
```

| Datei | Inhalt |
|---|---|
| [`ad-benutzer-pruefen.bpmn`](ad-benutzer-pruefen.bpmn) | Der Prozess. |
| [`ad-benutzer-pruefen.dmn`](ad-benutzer-pruefen.dmn) | Die Entscheidung `AdBenutzerGueltig`: vier Regeln und das Urteil `valid`. |
| [`benutzer.ldif`](benutzer.ldif) | Startbestand für den Mockup-Modus: 24 erfundene Konten, sechs davon mit Befund. |

## Was das Beispiel zeigt

- **Die Suche schreibt eine Datei, keine Variable.** `resultAs="document"` liest das
  Verzeichnis seitenweise in `benutzer.csv`. Eine Zeile pro Konto: `dn`, dann die Spalten
  aus `select` in dieser Reihenfolge. In `benutzer` stehen `found`, `count` und der kleinste
  DN wie bei jeder Suche, dazu der Verweis `document`. Ein Gateway, das `benutzer.found`
  liest, funktioniert unverändert.
- **Die Prüfung liest die Datei direkt.** `<atlas:rowsFrom source="benutzer.document"/>`
  nennt die Datei über ein Feld in der Variable, ohne einen Schritt, der den Verweis
  umkopiert. Ein Job wertet die Entscheidung für jede Zeile aus und legt das Ergebnis in
  `benutzer.result.csv` ab.
- **Mehrwertige Attribute stehen in einer Zelle.** `memberOf` ist durch `|` getrennt; ein `|`
  oder `\` in einem Wert ist mit `\` maskiert. Die Regel teilt die Zelle mit
  `split(memberOf, "[|]")`. Die eckigen Klammern machen das `|` zum gewöhnlichen Zeichen,
  ohne Escape im FEEL-String.
- **Eine Bitmaske ohne Bitoperationen.** `userAccountControl` steht als Zahl in der Datei.
  Bit 2 (ACCOUNTDISABLE) liest `modulo(floor(number(userAccountControl) / 2), 2)`.

## Die Regeln

| Ausgabe | Regel |
|---|---|
| `kontoAktiv` | Bit 2 von `userAccountControl` ist nicht gesetzt. Ein leerer Wert gilt als nicht aktiv. |
| `mailGesetzt` | `mail` enthält ein `@`. |
| `abteilungBekannt` | `department` ist Buchhaltung, Informatik, Logistik, Personal oder Vertrieb. |
| `lizenzEindeutig` | Genau eine Gruppe in `memberOf` beginnt mit `CN=Lizenz-`. |
| `valid` | Alle vier Regeln sind erfüllt. |

Die Abteilungen und die Lizenzgruppen sind **Annahmen dieses Beispiels**. Eine echte
Prüfung nimmt sie aus den Regeln, die die Qualitätsverantwortliche durchsetzen will.

## Ausprobieren ohne Domain Controller

1. Beispiel installieren (Handbuch → Beispiele → *AD-Benutzer prüfen*) oder Modell und
   Entscheidung selbst deployen.
2. Console → Workers → Karte **Active Directory** → *Serve Active Directory tasks against a
   mockup* einschalten, als Startbestand den Inhalt von `benutzer.ldif` einfügen, speichern.
3. Console → Administration → Secrets: ein Secret `AD_BIND` mit **beliebigem** Wert anlegen.
   Der Mock lehnt einen Bind mit DN und ohne Passwort ab, wie ein Domain Controller; welchen
   Wert das Passwort hat, prüft er nicht.
4. Instanz starten, ohne Variablen.

Erwartet: `pruefung.total = 24`, `valid = 18`, `invalid = 6`, die Instanz endet in *Befunde
abgelegt*. Die sechs Befunde:

| Konto | Verletzte Regel |
|---|---|
| `ebaumann` | `kontoAktiv` (514) |
| `iweber` | `mailGesetzt` |
| `lsteiner` | `abteilungBekannt` (Marketing) |
| `obucher` | `lizenzEindeutig` (keine Lizenzgruppe) |
| `sroth` | `lizenzEindeutig` (zwei Lizenzgruppen) |
| `vlehmann` | `kontoAktiv` und `mailGesetzt` (546) |

Genau dieser Lauf ist als Test hinterlegt:
`go test ./worker -run TestTheADUserCheckExampleRunsAgainstTheMockDirectory`.

**50 000 Konten:** `go run ./examples/testdaten/generieren -n 50000 -out ./testdaten` schreibt
`benutzer.ldif` mit 50 000 erfundenen Konten, davon 3 000 mit Befund (sechs je hundert, siehe
[`testdaten`](../testdaten/)). Die Konsole nimmt einen Startbestand standardmässig bis 256 KB an;
für diese Grösse den Server mit `ATLAS_LIMIT_SETTINGS=33554432` starten, oder einen selbst
gestarteten Worker mit `ATLAS_AD_MOCK=1 ATLAS_AD_MOCK_SEED=./testdaten/benutzer.ldif` auf die Datei
zeigen lassen.

## Gegen ein echtes Verzeichnis

Am Prozess ändern sich nur Server-URL, Bind-DN und Base-DN im Task *Konten lesen*; das
Secret `AD_BIND` trägt dann das Passwort des Dienstkontos. Drei Punkte sind dabei zu prüfen,
bevor ein Ergebnis als Aussage über das Verzeichnis gilt:

- **Leserechte des Dienstkontos.** Ein Attribut, das das Konto nicht lesen darf, kommt als
  leere Zelle an. Die Regel meldet dann einen Datenmangel, wo ein Rechteproblem vorliegt.
- **Grösse.** `maxEntries` ist in der Dokumentform standardmässig 100 000; die Dokumentgrenze
  des Servers (`ATLAS_LIMIT_DOCUMENT`) gilt immer. Beides wird beim Schreiben geprüft: Eine
  Suche darüber scheitert, statt eine abgeschnittene Datei abzulegen.
- **Personendaten.** Die Datei nennt jede Person des Verzeichnisses. Sie gehört zur Instanz;
  wer die Instanz lesen darf, darf sie lesen, und sie wird mit der Instanz entfernt
  (Entscheid D5 im Plan). Eine Korrektur im Verzeichnis ist ein eigener Prozess mit eigener
  Freigabe und nicht Teil dieser Prüfung.

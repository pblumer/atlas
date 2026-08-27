# Compliance

Unterlagen für den Einsatz von Atlas in regulierten Umgebungen. Aktuell ist das
die **schweizerische Bundesverwaltung** (ISG/ISV, DSG, HERMES, BIT-Vorgaben).

| Dokument | Inhalt |
|----------|--------|
| [`isds-konzept.md`](isds-konzept.md) | Antworten auf die Vorlage **P042-Hi01 — ISDS-Konzept** (BIT-Template V120 / NCSC V4.4): Systembeschreibung, Datenbeschreibung, Kommunikationsmatrix, Risiken und Schutzmassnahmen, Wiederherstellung, Ausserbetriebnahme |
| [`isds-offene-punkte.md`](isds-offene-punkte.md) | Was **am Produkt** fehlt, bevor eine Einführung im Bund vertretbar ist — priorisiert, mit Bezug auf die Restrisiken des Konzepts |
| [`zugriffsschutz-konzept.md`](zugriffsschutz-konzept.md) | **Konzept**: jede Schnittstelle hinter einen authentisierten Prinzipal — Schnittstelleninventar, der `/mcp`-Befund im Detail, acht Massnahmen und ein Stufenplan bis zur Tauglichkeit für einen produktiven PoC (schliesst R-08 / O-07) |

## Gebrauchsanweisung

- Die Dokumente sind auf Deutsch, weil sie in dieser Sprache eingereicht und
  geprüft werden. Der Rest des Repositories bleibt Englisch.
- Das ISDS-Konzept ist **keine fertige Einreichung**. Produktseitige Aussagen
  sind belegt und übernehmbar; alles mit **⟨…⟩** muss die einführende
  Verwaltungseinheit ausfüllen (Namen, Schutzbedarf, Zonen, SLA, CRQ-Nummern).
- Zu jeder Einführung gehören zusätzlich P041-Hi01 (Schutzbedarfsanalyse),
  P042-Hi02 (Risikoanalyse als Excel mit Restrisikomatrix) und — bei kritischen
  Geschäftsprozessen — P042-Hi03 (Notfallkonzept).

## Pflege

Diese Dokumente veralten still, deshalb: **wer eine sicherheitsrelevante
Eigenschaft ändert, ändert sie hier mit.** Das betrifft insbesondere

- Authentisierung und Autorisierung (`api/auth.go`, ADR-0044/0049/0071/0129),
- den Vault und die Secret-Auflösung (ADR-0041/0069/0070),
- Aufbewahrung und Löschung (ADR-0115/0144),
- Backup, Checkpoints und WAL-Kompaktierung (ADR-0107/0109/0131),
- neue Connector-Kinds oder Endpunkte, die die Kommunikationsmatrix (Kap. 5.4)
  oder die Liste der unauthentisierten Routen (Kap. 6.1, R-08) verändern,
- Flags und Umgebungsvariablen in `docs/install.md`.

Unabhängig davon: jährlicher Review, spätestens mit jedem Minor-Release
(Kapitel 1.3 des Konzepts).

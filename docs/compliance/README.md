# Compliance

**Produktseitig geprüft: 24. Oktober 2026** · Basis: `main` mit Änderungen
nach Release 0.19.0. **Nicht** als formell abgenommenes ISDS verwenden.

Unterlagen für den Einsatz von Atlas in regulierten Umgebungen. Aktuell ist das
die **schweizerische Bundesverwaltung** (ISG/ISV, DSG, HERMES, BIT-Vorgaben).

| Dokument | Inhalt |
|----------|--------|
| [`isds-konzept.md`](isds-konzept.md) | Antworten auf die Vorlage **P042-Hi01 — ISDS-Konzept** (BIT-Template V120 / NCSC V4.4): Systembeschreibung, Datenbeschreibung, aktualisierte Kommunikationsmatrix (inkl. Microsoft/Google/Dokumente), Restrisiken, neueste Schutzmassnahmen, Wiederherstellung, Ausserbetriebnahme |
| [`isds-offene-punkte.md`](isds-offene-punkte.md) | Was **am Produkt** bzw. am Sicherheitsnachweis noch fehlt — aktualisierte Prioritäten, inklusive Identity Policy, Dokument-Datenminimierung und Architektursnapshots |
| [`zugriffsschutz-konzept.md`](zugriffsschutz-konzept.md) | **Fortgeschriebenes Konzept**: historischer `/mcp`-Befund und implementierte Authentisierungsgrenze, OAuth/OIDC, Rollen und Worker-Zugriff. Neu M13: policygesteuerte, identitätsbezogene SharePoint-Ausführung mit klaren Grenzen |

## Stand und Geltungsgrenzen

- **Transportverschlüsselung:** atlas kann direkt TLS 1.3 terminieren
  (`--tls-cert`/`--tls-key`, ADR-0191) oder hinter einem TLS-Reverse-Proxy
  betrieben werden. Der öffentliche Listener ist ohne TLS-Konfiguration HTTP;
  die konkrete Ingress-Topologie und Access-Protokollierung sind abzunehmen.
- **Implementiert** bedeutet im Repository vorhanden, nicht durch eIAM, BIT,
  Provider oder eine unabhängige Prüfstelle abgenommen.
- OIDC-Föderation, Rollen und Worker-Freigaben sind implementiert. Die neue
  **Identitätsdelegation nach ADR-0475 ist ausdrücklich teilweise**: die
  Durchsetzung gilt für den beaufsichtigten SharePoint-Worker; Service-Modi,
  externe Worker und weitere Provider haben andere oder noch keine Garantien.
- Architekturskizzen pro Release (ADR-0471) lassen sich als SVG und ArchiMate
  exportieren. Sie sind Snapshots, keine aktive Prüfung der Firewall- oder
  Provider-Konfiguration; die Confluence-Veröffentlichung ist noch offen.
- Remote-Formularabfragen minimieren WAL-Daten (ADR-0473), wohingegen bewusstes
  `get-attachment` aus dem Mail-Worker binäre Daten als Base64-Variable
  persistieren kann (ADR-0478). Paperless-ngx und NATEL go sind **nicht**
  mit produktiv nutzbaren Worker-Typen gleichzusetzen.
- Das Produkt bleibt gemäss [SECURITY.md](../../SECURITY.md) ein
  `0.x`-Developer-Preview **ohne Produktionsfreigabe**.

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
- TLS-Listener, Zertifikatswechsel, Proxy-/Ingress-Topologie (ADR-0191/0448),
- den Vault und die Secret-Auflösung (ADR-0041/0069/0070),
- Aufbewahrung und Löschung (ADR-0115/0144),
- Backup, Checkpoints und WAL-Kompaktierung (ADR-0107/0109/0131),
- neue Worker-Typen oder Endpunkte, die die Kommunikationsmatrix (Kap. 5.4)
  oder die Liste der bewusst öffentlichen Routen (Kap. 6.1, R-08) verändern,
- Flags und Umgebungsvariablen in `docs/install.md`,
- verknüpfte Identitäten, Policy-Enforcement und Broker-Tokens (ADR-0475),
- neue nicht-durable Datenabfragen, Dokumentexporte und Event-Log-Aufbewahrung
  (ADR-0473/0478/0472),
- Release-Snapshots mit physischen Endpunkten und deren Redaktionsrechten
  (ADR-0471).

Unabhängig davon: jährlicher Review, spätestens mit jedem Minor-Release
(Kapitel 1.3 des Konzepts).

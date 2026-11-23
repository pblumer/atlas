# SPoP: Lebenszyklus einer SharePoint-OnPrem-TeamSite

Proof of Concept für drei Geschäftsfälle aus dem Projekt Provisioning vNext (PvN), Bereich
SharePoint OnPrem, abgebildet als **ein Lebenszyklus-Prozess** mit Formularen, drei
Entscheidungstabellen und einem Katalogprodukt.

| Geschäftsfall | Inhalt | Im Prozess |
|---|---|---|
| PvN-GF-001 | TeamSite bestellen | Bereitstellung (flach im Strang) |
| PvN-GF-002 | Quota-Erhöhung | Subprozess «Quota erhöhen» |
| PvN-GF-003 | Externen Benutzer einladen | Subprozess «Externen Benutzer einladen» |
| Ergänzung | Rückgabe | Catch-Event, Boundary-Events, Nachrichtenstart als Rückfallebene |

Die Rückgabe steht in keinem der drei Detailblätter. Sie ist dabei, weil ein Katalogprodukt
mit Lebenszyklus eine Rückgabe verlangt.

## Modellierung

Katalogform `per-position`, nach dem Muster von `lc_proc_m365_funktionspostfach`: Jede
Bestellposition hat genau einen Strang.

1. `spop.teamsite.bestellen` startet die Bereitstellung:
   - Richtlinie prüfen, bei Bedarf Vier-Augen-Freigabe.
   - Site erstellen, dann parallel Gruppen und Berechtigungen, Quota und Features.
   - Die Position als erledigt melden.
2. Danach wartet der Strang am ereignisbasierten Gateway «Aktiv: auf Auftrag warten» auf:
   - `spop.teamsite.quota`: Subprozess Quota-Erhöhung, danach zurück zum Warten.
   - `spop.teamsite.extern`: Subprozess Einladung. Die Berechtigung wird erst nach der
     Annahme (`spop.extern.angenommen`, Schlüssel `einladung.einladungId`) gesetzt; nach
     Ablauf der Gültigkeit wird die Einladung widerrufen.
   - `spop.teamsite.rueckgabe`: Rückgabe, terminierendes Ende.
3. Eine Rückgabe bricht einen laufenden Subprozess über ein unterbrechendes Boundary-Event ab.
4. Alle späteren Aktionen sind an `orderId + "/" + positionId` korreliert.

Der Strang trägt den Stand der Site in `site`, `quota` und `externe`. Aktionen müssen die
Site deshalb nicht nennen.

Ergebnisse innerer Aufgaben bleiben im Subprozess (ADR-0074). `quota` und `externe` gelangen
nur über Output-Mappings am Subprozess in den Strang.

## Artefakte

| Datei | Zweck |
|---|---|
| `spop-teamsite.bpmn` | Prozess `proc_spop_teamsite` mit von Hand berechnetem BPMN-DI |
| `SpopTeamsiteRichtlinie.dmn` | Namensregel, Quota je Site-Typ, Vier-Augen-Prinzip (anstelle von `amt_naming.rego`, `quota_limits.rego`, `approval.rego`) |
| `SpopQuotaRichtlinie.dmn` | Quota-Erhöhung: Begründung, Maximum, Schwellenwert der Freigabe |
| `SpopExternRichtlinie.dmn` | Externe Benutzer: Mail-Domains, Gültigkeit, MFA, Vier-Augen-Prinzip (anstelle von `external_user.rego`, `mfa_required.rego`) |
| `spop-teamsite-bestellung.form.json` | Bestellung (Konfigurationsformular des Produkts) |
| `spop-quota-erhoehung.form.json` | Aktion Quota erhöhen |
| `spop-extern-einladen.form.json` | Aktion externen Benutzer einladen |
| `spop-teamsite-rueckgabe.form.json` | Rückgabe |
| `spop-freigabe.form.json` | Vier-Augen-Freigabe (Gruppe `SPoP-Freigabe`) |
| `produkt.json` | Katalogprodukt `spop-teamsite` |
| `tools/` | Generatoren für BPMN, DMN und Formulare |

## Neu erzeugen

Die Modelle sind generiert. Änderungen gehören in die Generatoren, nicht in die
erzeugten Dateien. Aus `examples/spop` ausführen:

```bash
python3 tools/gen_bpmn.py spop-teamsite.bpmn
```

```bash
python3 tools/gen_dmn.py .
```

```bash
python3 tools/gen_forms.py .
```

Danach `go test ./examples -update` (Katalog des Handbuchs) und
`go test ./examples/... ./api/layout/...` laufen lassen.

## Installation

Das Beispiel wird nicht aus dem Handbuch installiert, weil seine Starts die Aktionen eines
Katalogprodukts sind. Die Schritte:

1. Eine Applikation anlegen, darin die fünf Formulare speichern.
2. Die drei DMN-Modelle hochladen (Handles `spop-teamsite-richtlinie`,
   `spop-quota-richtlinie`, `spop-extern-richtlinie`) und als Regeln
   `SpopTeamsiteRichtlinie`, `SpopQuotaRichtlinie`, `SpopExternRichtlinie` registrieren.
3. Den Prozess als Entwurf speichern und die Applikation deployen.
4. Das Produkt aus `produkt.json` mit einem Heimkatalog anlegen, in den Katalog aufnehmen
   und den Katalog veröffentlichen.
5. Die Rückmeldungen an den Katalog brauchen:
   - das Secret `atlas` mit einem API-Token der Rolle operator;
   - die Applikationskonstante `ATLAS_API_BASE`, falls der Server nicht unter
     `http://127.0.0.1:8080` lauscht.

## Annahmen und offene Punkte

- **Simulierte Ausführung:** Die Schritte gegen APS-BW (`atlas-provider-apsbw`) sind mit
  `atlas:mockupConnector` simuliert. Jede Aufgabe nennt in ihrer Dokumentation die Ressource,
  die sie ersetzt. Der Worker Type `sharepointserver` deckt Site, Quota, Owner, Gruppen und
  Löschen ab, aber keine Einladung externer Benutzer.
- **Grenzwerte:** Alle Grenzwerte sind Annahmen und vom Service Owner zu bestätigen:
  - Standardquota je Site-Typ: Standard 5120 MB, Project 10240 MB, Department 20480 MB.
  - Maximum je Site-Typ: Standard 10240 MB, Project 51200 MB, Department 102400 MB.
  - Vier-Augen-Prinzip bei einer Quota-Erhöhung um mehr als 5120 MB.
  - Gültigkeit einer Einladung 1 bis 90 Tage.
  - Liste der gesperrten privaten Mail-Domains.
- **Vier-Augen-Prinzip:** Getrennt wird nur über die Gruppe `SPoP-Freigabe`. Der Prozess
  verhindert nicht, dass die bestellende Person selbst freigibt.
- **Eine Aktion zur Zeit:** Während ein Subprozess läuft, nimmt der Strang keine weitere
  Aktion an. Eine offene Einladung blockiert eine Quota-Erhöhung bis zur Annahme oder zum
  Ablauf.
- **Rückgabe beantwortet keine offene Aktion:** Bricht eine Rückgabe eine laufende Aktion
  ab, bleibt deren Befehl ohne Antwort.
- **Personendaten:** `ownerUpn`, `memberUpns` und `email` sind nicht als Personendaten
  deklariert (`atlas:personal`); der Katalog meldet das beim Veröffentlichen als Warnung.
- **Feature-Zuordnung:** Welche Features je Site-Typ aktiviert werden, ist offen; aktiviert
  wird nur TeamCollab.

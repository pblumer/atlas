# Zugriffsschutz-Konzept — jede Schnittstelle hinter einem Prinzipal

- **Status:** Entwurf zur Diskussion
- **Datum:** 2026-08-26
- **Zielbild:** Atlas ist für einen produktiven PoC vertretbar, **ohne** dass die
  Absicherung allein an der Konfiguration eines vorgelagerten Proxys hängt.

---

## 0 Auftrag und Abgrenzung

Der Prüfbefund lautet: nicht alle Schnittstellen sind durch einen Benutzer-Login
geschützt, und der MCP-Zugang ist so nicht akzeptabel. **Der Befund stimmt.**
Kapitel 1 belegt ihn aus dem Code und zeigt, dass er für `/mcp` sogar noch
schärfer ist als formuliert.

Dieses Dokument beschreibt den **kleinsten Umbau am Produkt**, nach dem die
Aussage «keine Schnittstelle ohne authentisierten Prinzipal» wahr *und* durch
einen Test belegbar ist. Es ist bewusst auf ein PoC-Zielbild zugeschnitten: keine
Föderation, kein vollständiges Rollenmodell, keine Mandantenfähigkeit.

Verhältnis zu den bestehenden Unterlagen:

| Dokument | Bezug |
|----------|-------|
| [`isds-konzept.md`](isds-konzept.md) | Restrisiko **R-08** (unauthentisierte Endpunkte) ist heute **rot**, solange `/mcp` erreichbar ist. Dieses Konzept bringt R-08 auf grün und verbessert R-03, R-04, R-12 und R-13, ohne sie zu schliessen. |
| [`isds-offene-punkte.md`](isds-offene-punkte.md) | Schliesst **O-07** vollständig, **O-03** und **O-04** weitgehend. Legt die Grundlage, auf der **O-01** (Föderation) und **O-02** (feingranulare Autorisierung) später aufsetzen — beide bleiben ausdrücklich draussen. |

### Umsetzungsstand

| Massnahme | Stand |
|-----------|-------|
| **M1** — Zugriffsklassen je Route + Inventar-Test | ✅ umgesetzt — [`ADR-0199`](../adr/0199-route-access-classes.md), `api/access.go` |
| **M2** — `/mcp` hinter dieselbe Grenze, Identität durchreichen | ✅ umgesetzt — [`ADR-0196`](../adr/0196-authenticated-mcp-transport.md), `api.WithMCP` + `mcp/client.go` |
| **M4** — `atlas mcp --token` / `ATLAS_TOKEN` | ✅ umgesetzt — im selben Entscheid, `cmd/atlas/main.go` |
| **M5** — `--auth` standardmässig an | ✅ umgesetzt — [`ADR-0195`](../adr/0195-auth-on-by-default.md) |
| **M7** — Anmelde-Härtung | ✅ umgesetzt — [`ADR-0197`](../adr/0197-login-throttle-and-audit-log.md), `api/loginguard.go` |
| **M8** — Sicherheits-Audit-Log | ✅ umgesetzt — im selben Entscheid, `api/audit.go` |
| **M3** — API-Tokens als erste Klasse | ✅ umgesetzt — [`ADR-0194`](../adr/0194-api-tokens.md), `api/apitokenstore.go` |
| **M6** — `/metrics` hinter die Schranke | ✅ umgesetzt — [`ADR-0198`](../adr/0198-metrics-behind-the-boundary.md) |
| **M10** — OAuth für gehostete MCP-Clients | ✅ umgesetzt — [`ADR-0200`](../adr/0200-mcp-oauth-resource-server.md): Ressourcenserver (`api/oauthmeta.go`), Autorisierungsserver (`api/oauthserver.go`) und dynamische Client-Registrierung (`api/oauthregister.go`, standardmässig aus) |
| **M11** — Berechtigungen auf Worker-Ebene | ✅ umgesetzt — [`ADR-0205`](../adr/0205-connector-ownership-and-event-delivery.md): Eigentümer und Freigabe am Worker (`api/connectorscope.go`) und der Anspruch auf den Nachrichtennamen an beiden Türen (`api/messageclaim.go`) |

**Die acht Massnahmen der Stufe 1 sind umgesetzt. R-08 ist grün.**

M10 und M11 kamen später dazu, und **keines ist eine Lücke in R-08**: Jede
Schnittstelle verlangt einen Prinzipal. M10 ist die Antwort auf einen Fall, den M2
und M4 nicht bedacht haben — einen Client, den niemand konfigurieren kann, weil er
bei einem Dritten läuft. M11 ist die Antwort auf eine Frage *hinter* der Haustür:
Ein Konto ist angemeldet, aber alle Konten dürfen dasselbe. Das gehört zu O-02
(feingranulare Autorisierung), nicht zu R-08.

Kapitel 1 beschreibt durchgehend den **Befund**, also den Zustand vor diesen
Massnahmen — auch 1.5, das zuletzt dazukam und inzwischen ebenfalls behoben ist.
Das ist Absicht: Kapitel 1 ist der Beleg dafür, was behoben wurde. Was heute gilt,
steht in Kapitel 6.

---

## 1 Befund

### 1.1 Wie die Authentisierung heute greift

Die Grenze in `api/auth.go` ist gut gebaut: Handler hängen an einem aufgelösten
`*httpapi.Principal`, nie an «einem Cookie». Der Mechanismus dahinter ist
austauschbar (ADR-0044). Erzwungen wird über die Middleware `withAuth`, und
*welche* Route sie schützt, entscheidet `requiresAuth()` — **nach Pfadpräfix**:

```go
// api/auth.go — requiresAuth
if path != "/api/v1" && !strings.HasPrefix(path, "/api/v1/") {
    return false          // alles ausserhalb von /api/v1 ist offen
}
```

Damit ist eine Route nicht deshalb offen, weil jemand sie öffnen wollte, sondern
weil sie ausserhalb eines Präfixes gemountet wurde. **Das ist die strukturelle
Ursache aller folgenden Befunde.**

### 1.2 Schnittstelleninventar

| Endpunkt | Erreichbar für | Was dort möglich ist | Bewertung |
|----------|----------------|----------------------|-----------|
| `/api/v1/**` (178 Routen) | mit `--auth`: angemeldeter Benutzer<br>ohne `--auth`: **jeden** | Alles ausser den 52 mit `requireAdmin` geschützten Stellen | ok, **sofern** `--auth` an ist (Standard: **aus**) |
| `/api/v1/auth/login`, `/info`, `/settings/{theme,logo,registration}` | jeden | Login, Produktinfo, Branding vor dem Login | ok — die Login-Maske braucht sie |
| `/api/v1/openapi.json` | jeden | vollständige Beschreibung der API-Oberfläche | unnötig offen; vor dem Login wird sie nicht gebraucht |
| `/api/docs` (`--docs`, Standard **an**) | jeden | API-Explorer inkl. «Try it out» gegen dieselbe API | auf einer Produktion abzuschalten (ADR-0043) |
| `/metrics` (`--metrics`, Standard **an**) | jeden | Betriebs- und Lastkennzahlen, Instanz- und Auftragszahlen | Informationsabfluss; hängt allein am Proxy |
| `/healthz`, `/readyz` | jeden | Lebens-/Bereitschaftszustand, kein Inhalt | ok — bewusst offen (ADR-0142) |
| `/public/forms/{token}`, `/public/documentation/{token}` | Token-Inhaber | genau ein Startformular bzw. eine Doku-Version, ratenbegrenzt | ok — der Token *ist* die Autorisierung (ADR-0029/0143) |
| **`/mcp`** | **jeden, der den Port erreicht** | **71 Werkzeuge: deployen, Instanzen starten, abbrechen, terminieren, Aufgaben abschliessen, Entwürfe und Projekte ändern und löschen, Laufzeit- und Variablendaten lesen** | **nicht vertretbar** |

### 1.3 `/mcp` im Detail — warum «nicht akzeptabel» richtig ist

Zwei Tatsachen, und erst ihre Kombination ergibt die Schwere:

1. **`/mcp` liegt ausserhalb der Grenze.** Der Adapter wird in
   `cmd/atlas/main.go` auf einen *eigenen* Root-Mux gehängt, **neben**
   `srv.Handler()`. Die Middleware `withAuth` sieht diese Anfragen nie. `--auth`
   ändert daran nichts.
2. **Der Adapter hängt ein privilegiertes Credential an.** Jeder Loopback-Aufruf
   trägt den internen Service-Token des Servers (`mcp.WithBearer`, ADR-0049).

Daraus folgt der eigentliche Punkt:

> **`--auth` schliesst `/mcp` nicht — es versorgt `/mcp` mit einem
> funktionierenden Credential.** Ein anonymer Aufrufer am Port handelt als
> `system:mcp`, ein Prinzipal, der die Session-Schranke passiert und alles darf,
> was kein `requireAdmin` trägt.

Und weiter über `atlas_deploy`: wer deployen darf, führt Code aus — Skript-Tasks
laufen im Kontext des Dienstbenutzers, alle drei Sprachen sind standardmässig an
(ADR-0047; im ISDS-Konzept als **R-09** festgehalten). **R-08 und R-09 zusammen
ergeben an einem erreichbaren `/mcp` Codeausführung ohne jede Authentisierung.**
Das Produkt sagt das heute nur als Kommentar im Code und als Zeile im
Betriebshandbuch — nicht als Verhalten.

Zwei Nebenbefunde derselben Ursache:

- **Der entfernte MCP-Adapter kann sich gar nicht authentisieren.**
  `atlas mcp --server …` baut seinen Client ohne Bearer (`runMCP` in
  `cmd/atlas/main.go`); eine `--token`-Option existiert nicht. Gegen einen Server
  mit `--auth` ist er unbrauchbar. `atlas worker` hat `--token`/`ATLAS_TOKEN` —
  der MCP-Adapter hat es nie bekommen.
- **Ein Ambient-Secret für alles.** Derselbe interne Token ist auch das
  Worker-Credential (`api/superviseenv.go`, `ATLAS_TOKEN`). Er hat keine
  Gültigkeitsdauer, keinen Geltungsbereich, keinen Widerruf und keine Identität je
  Nutzer; jede Handlung erscheint als `system:mcp`.

### 1.4 Was das Repository an einer Stelle schon richtig macht

`deployAgentAllowed` in `api/auth.go` ist eine **fail-closed-Allowlist**, über
einen `http.ServeMux` aufgelöst, mit genau der richtigen Begründung im Kommentar:
die Reichweite eines Credentials müsse *durch Lesen einer kurzen Liste beweisbar*
sein statt durch Audit jedes Handlers. Dieses Prinzip fehlt eine Ebene höher — für
die Frage, welche Route überhaupt ohne Prinzipal erreichbar ist. Massnahme M1
trägt es genau dorthin.

### 1.5 Neubefund: Ein Worker gehört niemandem

Dieser Punkt kam nach der ersten Fassung dazu, aus einer Frage aus dem Betrieb:
«Wenn ich einen Inbound-E-Mail-Worker habe, darf ausser mir niemand diese
Ereignisse nutzen.» Nachgemessen an einem Server mit `--auth`, mit einem
gewöhnlichen Konto, das nur die Rolle `user` trägt:

| Es kann | Weil |
|---|---|
| einen Worker anlegen | `handleCreateConnector` hat keine Rollenprüfung |
| **alle** Worker auflisten, mit Endpunkt und Absenderpostfach | `handleListConnectors` ebenso wenig |
| **fremde** Worker ändern und löschen | dito; `DELETE …?force=true` antwortete `204` |
| **alle** Inbound-Abonnements lesen — also jeden Nachrichtennamen | `handleListInboundSubscriptions` ebenso wenig |
| ein Abonnement auf einem **fremden** Worker anlegen, unter einem selbst gewählten Nachrichtennamen | `handleCreateInboundSubscription` ebenso wenig |

`requireAdmin` steht auf genau einem dieser Endpunkte:
`POST /api/v1/connectors/{id}/provision-clio-key`. Der Rest lautet «irgendein
authentisierter Prinzipal», was seit M5 heisst: irgendein Konto der Installation.

Hinter der Konfiguration liegt die Zustellung, und dort ist es grundsätzlicher.
`Processor.PublishInbound` trägt **einen Nachrichtennamen und einen
Korrelationsschlüssel** — sonst nichts. Jede deployte Definition mit passendem
Nachrichten-Startereignis startet. **Der Nachrichtenname ist die ganze
Autorisierung**, und er ist aus der Liste oben für jeden lesbar.

Das ist keine Lücke in R-08 — jede dieser Routen verlangt einen Prinzipal, die
Haustür ist zu. Es ist eine Lücke *dahinter*, also in O-02 (feingranulare
Autorisierung), und sie fällt erst auf, seit ein Worker nicht mehr nur
hinausgreift, sondern hereinlässt (ADR-0075). Massnahme **M11** beantwortet sie und
ist umgesetzt; dieser Abschnitt beschreibt den Zustand davor.

---

## 2 Zielbild — fünf Grundsätze

| | Grundsatz |
|---|---|
| **G1** | **Kein Zugang ohne Prinzipal.** Jede nicht ausdrücklich öffentliche Route wird zu einem `*Principal` aufgelöst oder mit `401` abgewiesen. Es gibt keinen Weg an `withAuth` vorbei — auch nicht durch Verdrahtung im `cmd`-Paket. |
| **G2** | **Fail closed by construction.** Öffentlich ist eine Route nur, wenn sie es *erklärt*. Eine neue Route ohne Angabe ist geschützt, und ein Test weist das nach — nicht ein Review. |
| **G3** | **Ein Credential-Modell.** Maschinen authentisieren sich mit benannten, widerrufbaren, ablaufenden Tokens, die auf einen Prinzipal zeigen — nicht mit einem prozessweiten Ambient-Secret. |
| **G4** | **MCP ist Transport, keine Identität.** Ein MCP-Aufruf handelt als *der Aufrufer*, nicht als der Adapter. Damit erbt MCP jede Autorisierungsregel der API automatisch — heute `requireAdmin`, später Rollen. |
| **G5** | **Sicher als Standard.** Der Auslieferungszustand ist der geschützte. Das Öffnen ist die bewusste Handlung und wird beim Start sichtbar protokolliert. |

---

## 3 Massnahmen

Aufwand als Grössenordnung, nicht als Schätzung: **XS** ≈ Stunden, **S** ≈ ein
Tag, **M** ≈ zwei bis vier Tage, **L** ≈ mehr als eine Woche.

| Nr. | Massnahme | Aufwand | Schliesst | Grundsatz |
|-----|-----------|---------|-----------|-----------|
| M1 ✅ | Zugriffsklassen je Route + Inventar-Test | S | Ursache aus 1.1/1.4 | G2 |
| M2 ✅ | `/mcp` hinter dieselbe Grenze, Identität durchreichen | M | R-08, O-07 | G1, G4 |
| M3 ✅ | API-Tokens als erste Klasse | M | O-07, Teil R-04 | G3 |
| M4 ✅ | `atlas mcp --token` / `ATLAS_TOKEN` | XS | Nebenbefund 1.3 | G3 |
| M5 ✅ | `--auth` standardmässig an | S | R-08, R-03 | G5 |
| M6 ✅ | `/metrics` hinter die Schranke, Geltungsbereich `metrics` | S | R-08, O-07 | G1 |
| M7 ✅ | Anmelde-Härtung: Drosselung je Adresse *und* je Konto | S | O-04, R-12 | — |
| M8 ✅ | Sicherheits-Audit-Log | S | O-03, R-13 | — |
| M9 ✅ | Rollen je Endpunktgruppe | L | O-02, R-04, R-09 | G2, G4 |
| M10 ✅ | OAuth für gehostete MCP-Clients | M–L | Folgelücke aus M2/M4; bereitet O-01 | G1, G3, G4 |
| M11 ✅ | Berechtigungen auf Worker-Ebene | M–L | Neubefund 1.5; Teil O-02, R-04 | G1, G4 |
| M12 ✅ | Föderierte Authentisierung (OIDC) | L | O-01, R-03 | G1, G5 |

### M1 — Zugriffsklassen je Route, mit Inventar-Test

**Problem.** Die Schranke entscheidet nach Präfix; alles ausserhalb `/api/v1` ist
stillschweigend offen. So sind `/mcp` und `/metrics` offen geworden, und so wird
es die nächste Route auch.

**Lösung.** Jede gemountete Route deklariert ihre Zugriffsklasse — `authenticated`
(Vorgabe), `public` (mit Begründung im Code) oder `operator`. Die Routentabelle in
`api/openapi.go` ist bereits die einzige Wahrheit für die `/api/v1`-Oberfläche und
hat schon einen Drift-Test gegen die OpenAPI-Beschreibung; sie bekommt ein Feld
mehr. Die Auflösung läuft über einen `http.ServeMux`, genau wie
`deployAgentMayReach` — handgeschriebener Pfadvergleich ist die Stelle, an der
eine Allowlist leckt.

Ein Test zählt jede Route auf, die der Server mountet, und schlägt fehl, sobald
eine ohne Klasse dabei ist. **Danach ist «offen durch Vergessen» kein möglicher
Zustand mehr** — und die Antwort an eine Prüfstelle ist eine Liste, keine Zusage.

**Berührt:** `api/openapi.go`, `api/auth.go`, ein neuer Test in `api/`.

### M2 — `/mcp` hinter dieselbe Grenze

Zwei Hälften; nur beide zusammen wirken.

**(a) Transport authentisieren.** Der MCP-Handler wird innerhalb von
`srv.Handler()` gemountet (etwa über eine Option `api.WithMCP(handler)`), damit er
`withAuth` durchläuft. Ohne Credential: `401` mit `WWW-Authenticate: Bearer`. Die
Montage wandert damit aus `cmd/atlas` heraus — die Grenze darf nicht durch
Verdrahtung umgehbar sein (G1).

**(b) Identität durchreichen statt injizieren.** Der Adapter hört auf, den
internen Token anzuhängen, und reicht **das Credential des Aufrufers** an die
Loopback-API weiter — Bearer oder Session-Cookie. `mcp.Client` kennt bereits einen
Bearer je Instanz (`WithBearer`); nötig ist eine Variante je Anfrage, also ein
Client mit Anfragekontext statt eines prozessweiten.

Das ist die eigentlich tragende Änderung: ein MCP-Aufruf ist danach **exakt so
privilegiert wie die Person, die ihn ausgelöst hat**, erscheint unter deren Namen
in jeder Spur, und MCP erbt jede künftige Autorisierungsregel der API, ohne dass
das MCP-Paket sie kennen muss. Als Nebeneffekt kann eine in der UI eingebettete
Agentenfunktion `/mcp` mit dem Session-Cookie des angemeldeten Benutzers rufen.

**Berührt:** `mcp/http.go`, `mcp/client.go`, `mcp/server.go`, `api/server.go`,
`cmd/atlas/main.go`.

**Hinweis zur Protokollkonformität.** Für entfernte MCP-Server sieht die
Spezifikation OAuth 2.1 mit Metadaten der geschützten Ressource vor. Für einen PoC
genügt der statische Bearer samt korrektem `401`/`WWW-Authenticate`; die
OAuth-Metadaten sind Stufe 2 und erst nötig, wenn ein Client (z. B. ein
claude.ai-Connector) sich selbst ein Token holen soll.

### M3 — API-Tokens als erste Klasse ✅

**Problem — und es war grösser als hier ursprünglich beschrieben.** Unter `--auth`
akzeptierte der Server neben dem Session-Cookie nur den internen Token: beim Start
erzeugt, über keinen Endpunkt ausgeliefert, also nur für den Prozess erreichbar,
der ihn erzeugt hat. Damit konnte sich **keine Maschine anmelden, die nicht ein
Kind dieses Servers ist** — kein Worker auf einem anderen Host, kein entfernter
MCP-Adapter, kein CI-Lauf. M5 hat das vom Randfall zum Normalfall gemacht.

Der Ausweg, den der Code zu versprechen schien, existierte ausserdem nicht:
`workerTokenEnv` respektiert ein vom Betrieb gesetztes `ATLAS_TOKEN` und spritzt
dann seinen eigenen nicht mehr ein — während `principalFor` einen Bearer nur gegen
den internen Token verglich. Der Wert wurde also nach aussen geehrt und nach innen
abgelehnt: `ATLAS_TOKEN` zu setzen legte die beaufsichtigten Worker lahm. Am
gebauten Binary verifiziert.

**Lösung.** Das Deploy-Token-Muster verallgemeinern — es ist im Repository schon
vorbildlich umgesetzt: 32 Byte CSPRNG, **nur der SHA-256 liegt auf Platte**, das
Geheimnis wird genau einmal ausgeliefert, admin-vergeben, benannt, widerrufbar. Ein
`apiToken` ergänzt es um `subject` (Benutzer oder Dienst), `scopes`, `expiresAt`
und `lastUsedAt`. Der Speicher ist ein Aufruf von `sidecar.NewStore`.

**Zwei** Geltungsbereiche, nicht fünf: `worker` erreicht genau die vier
Operationen, die `atlas worker` tatsächlich ausführt, und `full` das, was ein
angemeldeter Nicht-Admin erreicht — für CI und MCP-Adapter, deren Aufrufe sich
nicht im Voraus aufzählen lassen. Ein Token ist **nie** admin, also bleiben
Benutzerverwaltung, Secrets und Backups auch für `full` verschlossen. Feinere
Schnitte wären ein Berechtigungssystem, und das ist M9.

Durchgesetzt mit demselben `ServeMux`-Mechanismus wie `deployAgentAllowed` — und
zwar **nicht daneben**: die Deploy-Allowlist ist zu einem Geltungsbereich unter
den anderen geworden, sodass es eine Stelle gibt, an der die Reichweite jedes
Maschinen-Credentials steht. Ein zweiter paralleler Mechanismus, eine Änderung
nach dem ersten eingeführt, wäre genau die Drift, die ein Review findet.

Damit hat der Worker ein eigenes Credential, ein CI-Lauf ein eigenes, und der
entfernte MCP-Adapter überhaupt eines. Der interne Token bleibt für die Kinder
dieses Servers — er ist flüchtig und verlässt den Host nie, was für diesen Fall
besser ist als ein dauerhaftes Geheimnis.

**Berührt:** neue Datei nach dem Vorbild von `api/deploytokenstore.go`,
`api/auth.go`, Console-UI (Minimalansicht: anlegen, auflisten, widerrufen).

### M4 — `atlas mcp --token` ✅

`runMCP` hat die Option bekommen, die `atlas worker` längst hatte, samt Rückfall
auf `ATLAS_TOKEN` und Trimmen des Werts (ein aus einem Shell-Profil exportierter
Token trägt regelmässig ein Zeilenende, und ein damit gesendeter Bearer wird aus
einem Grund abgewiesen, den die `401` nicht nennt). Der Start protokolliert, *ob*
ein Credential konfiguriert ist — nie das Credential —, weil «jedes Werkzeug
antwortet 401» und «es wurde kein Token gesetzt» derselbe Vorfall sind. `runMCP`
ist so aufgeteilt, dass seine Ströme übergeben werden können; damit steht der
ganze Weg des Credentials unter Test.

### M5 — `--auth` standardmässig an ✅

`--auth` ist `true`. `--auth=false` bleibt möglich (Entwicklung, Demo) und
schreibt beim Start eine deutliche Warnzeile unter dem stabilen Ereignisnamen
`auth.disabled`, die benennt, was offen ist. `/api/v1/openapi.json` **und** der
API-Explorer `/api/docs` liegen hinter der Schranke — die Login-Maske liest
beides nicht, und «Try it out» fährt dieselbe verändernde API. Beide zusammen,
weil ein Explorer, der lädt und dann sein eigenes Dokument nicht holen kann,
schlechter ist als einer, der sagt, dass er eine Anmeldung braucht.

Der erste Start legt weiter genau einen Administrator an
(`ATLAS_ADMIN_USERNAME`/`ATLAS_ADMIN_PASSWORD`, sonst generiert und **einmalig**
protokolliert). Das Helm-Chart zieht nach: `atlas.auth.enabled` steht auf `true`,
und die Render-Verweigerung ohne Passwortquelle ist weg — sie stammte aus der
Zeit, als Auth opt-in war, und hätte als Standard genau den Standardweg brechen
lassen.

ADR-0044 hat die Erzwingung bewusst als opt-in gewählt, um bestehende
Installationen nicht zu brechen. Die Umkehr ist der Unterschied zwischen «kann
sicher betrieben werden» und «ist sicher, wenn man alles richtig macht» — und
M-02 im ISDS-Konzept prüft jetzt, dass `--auth=false` *nicht* gesetzt ist, was am
Startlog ablesbar ist statt an einer Argumentliste.

**Reihenfolge war wichtig:** M5 kam nach M2 und M4. Andernfalls hätte der
Standard auf «geschützt» gestanden, während die MCP-Pfade noch kein Credential
halten konnten.

### M6 — `/metrics` hinter die Schranke ✅

Kein eigener Listener. Nach M3 ist es ein Geltungsbereich: `/metrics` wird wie
jede andere Route erzwungen, und ein neuer Token-Geltungsbereich `metrics`
erlaubt genau ein Muster — `GET /metrics`. Das ist der engste Geltungsbereich im
System, denn ein Scraper braucht genau ein GET, für immer.

`--metrics-addr` war nur die Antwort auf eine Frage, die sich nicht mehr stellt:
Er existierte als Idee, weil ein Scraper sich nicht authentisieren konnte. Ein
zweiter `http.Server` mit eigenem Lebenszyklus tauscht ausserdem nur eine
Betriebsverantwortung gegen eine andere und lässt den Port selbst weiterhin offen.

`/healthz` und `/readyz` bleiben offen und inhaltslos — eine Bereitschaftssonde,
die ein Credential braucht, ist eine Sonde, die im Ernstfall nicht funktioniert.

**Ehrlich gesagt:** der Gewinn ist strukturell, nicht vertraulich. Die Exposition
trägt Instanzzahlen, Batch-Latenzen und Queue-Tiefe — keine Prozessvariablen,
keine Geschäftsdaten. Was sie bringt: die Aussage «keine Schnittstelle ohne
Credential» gilt jetzt ohne Fussnote, und die Liste der offenen Routen ist kurz
genug, um sie auf einen Blick zu lesen. **Breaking:** jede bestehende
Scrape-Konfiguration braucht zwei Zeilen (`authorization: credentials:`), und ein
fehlschlagender Scrape sieht aus wie ein gesunder Server — also vor dem Upgrade
lesen, nicht danach.

### M7 — Anmelde-Härtung ✅

Zwei Schlüssel in denselben Token-Bucket: **je Absenderadresse** 20 Versuche am
Stück, danach einer alle zwei Sekunden (grosszügig, weil ein ganzes Büro hinter
einer NAT-Adresse ein normaler Betriebsfall ist); **je Konto** 5 Versuche, danach
Auffüllung über 15 Minuten.

Drei Details tragen den Entwurf. Gezählt wird **vor** dem Kontolookup und
unabhängig davon, ob das Konto existiert — sonst wäre die Drosselung genau das
Enumerationsorakel, das die einheitliche Fehlermeldung vermeidet, und ein Flood
würde weiterhin bcrypt-Zeit kosten statt einen Map-Lookup. **Beide** Budgets
werden belastet, auch wenn eines schon abgewiesen hat, sodass Adresswechsel kein
Kontobudget schont und umgekehrt. Und eine **erfolgreiche Anmeldung setzt das
Kontobudget zurück** — wer sich zweimal vertippt und dann richtig liegt, steht
nicht am Rand einer Sperre.

Der Preis dieses Entwurfs ist ausdrücklich: wer an einem fremden Namen rät, kann
dessen Inhaber sperren. Genau deshalb läuft die Sperre von selbst ab und braucht
keinen Administrator.

### M8 — Sicherheits-Audit-Log ✅

Elf stabile `auth.*`-Ereignisse auf dem bestehenden Log-Strom: `login`,
`login_failed` (mit Grund), `login_throttled`, `logout`, `denied`,
`user_created`/`updated`/`deleted`, `password_set`, `token_minted`,
`token_revoked`. Jede Zeile trägt den handelnden Prinzipal und die
Absenderadresse; keine trägt ein Secret.

Zwei bewusste Entscheide darin. **Anonyme `401` werden nicht protokolliert** —
sie würden die seltene, aussagekräftige Zeile unter jedem Scan begraben, der den
Port findet; protokolliert wird die *Autorisierungs*verweigerung (`auth.denied`),
also der angemeldete Benutzer, der nach etwas greift, das ihm nicht zusteht. Und
eine fehlgeschlagene Anmeldung nennt den **Grund im Log, nicht auf der Leitung**:
das Log ist für den Betrieb, die Antwort ist das, was ein Angreifer lesen darf.

Kein eigener Sink: ein zweiter wäre eine zweite Sache zum Konfigurieren, Sichern
und Vergessen — `--log-format=json` liefert den bestehenden Strom SIEM-fähig.
`TestAuditTrailNeverCarriesASecret` treibt echte Passwörter und einen frisch
ausgestellten Deploy-Token durch die Handler und prüft, dass keiner davon — und
kein bcrypt-Präfix — im Log landet.

Diese Massnahme ist nicht nur O-03 — sie ist **der Nachweis, dass M1 bis M7
wirken**. Ohne sie bleibt die Antwort an eine Prüfstelle eine Behauptung.

### M9 — Rollen je Endpunktgruppe ✅

**Problem, gemessen.** M5 und M1 beantworten, *ob* jemand angemeldet ist. Was diese
Person dann darf, beantwortet Atlas mit genau einer Rolle: `admin`.

| | |
|---|---|
| Routen unter `/api/v1` | 199 |
| auf `admin` geprüft | 53 |
| **für jedes angemeldete Konto erreichbar** | **146** |

Unter diesen 146: `POST /api/v1/deployments`, `POST /api/v1/scripts/run`,
`DELETE /api/v1/instances/{key}`, `POST /api/v1/instances/terminate`,
`POST /api/v1/processes/{key}/cancel-instances` und die Variablen jeder Instanz.
Deployen ist Codeausführung (R-09) — «jedes angemeldete Konto darf deployen» ist
für einen Produktivbetrieb die falsche Vorgabe.

Die Freigabe-Arbeit (M11, ADR-0071/0205) deckt das **nicht** ab: Sie beantwortet,
*welches Objekt* jemand anfassen darf, nicht, *welche Art von Operation* jemand
überhaupt ausführen darf. Deshalb kann ein Konto ohne ein einziges eigenes Projekt
weiterhin ein Modell deployen und fremde Instanzen abbrechen.

**Ein zweiter Befund, der den Umfang bestimmt:** Ein **API-Token trägt gar keine
Rolle** (`api/auth.go`). Heute liest sich das als «kein Admin, sonst alles» — und
genau deshalb funktionieren Tokens. Unter einer Regel, die jede Route nach einer
Rolle fragt, erreicht ein rollenloser Prinzipal **nichts**: Jeder Worker, jeder
CI-Job und jeder stdio-MCP-Adapter stünde am Tag der Einführung still. Der Entwurf
muss also entscheiden, welche Rollen ein Token trägt.

**Vorschlag** (Entwurf:
[`ADR-0209`](../adr/0209-roles-per-endpoint-group.md)):

1. **Die Rolle steht in der Routentabelle**, neben Zusammenfassung und Tag — in
   derselben einzigen Quelle, die schon die OpenAPI-Beschreibung speist (ADR-0043).
   Die Schranke liest sie; kein Handler fragt noch einmal. Eine Route ohne Rolle
   fällt durch einen Test, der die Tabelle abläuft. Das ist dieselbe Eigenschaft
   wie bei M1: Die Reichweite ist durch **Lesen einer Liste** beweisbar, nicht
   durch Audit von 199 Handlern. Nach Tags zu gruppieren wäre billiger und geht
   nicht — `System` enthält `GET /api/v1/info` neben `POST /api/v1/restore/full`.
2. **Vier Rollen**: `admin`, `modeler` (Autorenschaft *und* Deploy), `operator`
   (Laufzeitsteuerung), `user` (Aufgaben und Lesen). Eine Liste, kein Verband: Ein
   Konto trägt mehrere. Deploy sitzt in `modeler`, weil der Schnitt, der zuerst
   zählt, «nicht jede Person darf deployen» ist; ein eigenes `deployer` später
   kostet eine Konstante, weil die Rolle je Route steht.
3. **Beim Aktualisieren behalten bestehende Konten, was sie heute können**
   (`modeler` + `operator` + `user`); neue Konten bekommen `user`. Ein API-Token
   trägt die Rollen des Kontos, das es ausgestellt hat, geschnitten mit seinem
   Geltungsbereich — ein Credential ist nie mächtiger als seine Ausstellerin.

**Warum hier anders entschieden wird als bei M11.** Dort wurden Alt-Worker
administrativ, weil der Ist-Zustand ein **Loch** war und eine Massnahme, die jede
bestehende Installation ausnimmt, nichts schliesst. Hier ist der Ist-Zustand ein
**dokumentiertes, akzeptiertes Restrisiko** (R-04, gelb) in Installationen, auf
denen heute gearbeitet wird. Aus jedem Konto beim Update eine reine
Aufgabenbearbeiterin zu machen, hielte diese Arbeit an — und ein Update, das die
Arbeit anhält, wird nicht eingespielt und schützt damit niemanden.

**Ehrlich dazugesagt.** Nach dem Update ist zunächst **nichts sicherer**: Jedes
bestehende Konto trägt drei Rollen, bis eine Betreiberin sie bewusst enger stellt.
Was die Massnahme liefert, ist die Möglichkeit, es enger zu stellen — und die
Gewissheit, dass keine Route stillschweigend offen bleibt. Ausserdem sind 199
Entscheidungen einmal von Hand zu treffen; der Inventar-Test macht die Menge
prüfbar, nicht jede einzelne Entscheidung richtig.

Wegen G4 gilt jede Regel automatisch auch für MCP: Ein Werkzeugaufruf handelt seit
M2 unter der Identität des Aufrufers. Das wird per Test belegt, nicht angenommen.

**Stand: umgesetzt** (`api/routeroles.go`, `api/rolesupgrade.go`). Jede der 199
Routen nennt ihre Rolle in `api/openapi.go`, jede daneben gemountete Route an ihrer
eigenen Montagestelle; `withAuth` liest sie an genau einer Stelle, nach dem
Geltungsbereich und vor dem Handler. Zwei Inventar-Tests halten das Ergebnis fest:
einer, dass **jede** gemountete Route eine bekannte Rolle nennt, und einer, dass die
51 rein administrativen Routen genau die ausgeschriebene Liste sind — eine Route
enger oder weiter zu stellen ist damit eine Änderung, die jemand liest.

**Die 51 `requireAdmin`-Aufrufe in den Handlern sind mit derselben Änderung
verschwunden.** Die Schranke weist ab, bevor ein Handler betreten wird; diese
Prüfungen konnten also gar nicht mehr auslösen. Eine Prüfung, die nicht auslösen
kann, ist keine Prüfung, sondern Dekoration, die wie eine aussieht. `requireAdmin`
bleibt für die eine Frage, die eine Rolle je Route nicht ausdrücken kann: Ein
Repository-Paket zu installieren verlangt `admin` nur, wenn das Paket Code
mitbringt.

Beim Bauen kam viererlei dazu, das im Entwurf nicht stand:

- **Der interne Token ist ein Credential wie die anderen.** Er identifiziert die
  Kinder dieses Servers (`superviseenv.go`) und trug bisher gar keine Rolle. Unter
  der neuen Regel hätte er nichts mehr erreicht — der beaufsichtigte Worker wäre am
  Tag der Aktualisierung stehengeblieben. Er trägt jetzt denselben Altbestand wie
  ein bestehendes Konto: alles ausser `admin`.
- **Ein Deploy-Token ist eine Veröffentlichende**, trägt also `modeler`. Seine
  Allowlist aus ADR-0129 — zwei Routen — bleibt die engere der beiden Antworten.
- **Ein API-Token ist nie `admin`.** Der Entwurf sagt «die Rollen des ausstellenden
  Kontos», und ausstellen darf nur eine Administratorin — wörtlich genommen hätte
  also jede Maschine die ganze Instanz bekommen. Ein Token trägt deshalb die
  Nicht-Admin-Rollen seiner Ausstellerin, und bei einer Administratorin den ganzen
  Nicht-Admin-Satz: genau das, was ein API-Token am Tag davor erreichte.
- **Die Aktualisierung braucht eine Markierung.** Ohne sie wäre aus der einmaligen
  Migration eine stehende Regel geworden: Ein Konto, das eine Betreiberin bewusst
  enger stellt, wäre beim nächsten Start wieder breit. Jeder Datensatz trägt jetzt
  `rolesUpgradedAt`; die Migration läuft genau einmal je Konto und schreibt zugleich
  die Rollen-Momentaufnahme in bestehenden OAuth-Freigaben nach — sonst könnte der
  Connector einer Person weniger als die Person.

**Eine Route bleibt bewusst offen für jedes angemeldete Konto:** das Lesen der
Variablen *einer* Instanz. Ein Aufgabenformular wird aus den Variablen der Instanz
vorbefüllt, zu der die Aufgabe gehört — die Rolle `operator`, die der Rest dieser
Gruppe trägt, hätte einer Aufgabenbearbeiterin ein leeres Formular hingelegt. Was
dort hingehört, ist die andere Achse («darf ich *diese* Instanz sehen»), und die
steht in O-02 als offener Punkt; eine Rolle je Endpunktgruppe kann sie nicht
ausdrücken.

**In der Console** vergibt der Konto-Dialog die vier Rollen namentlich, jede mit
dem, was sie erlaubt; die Navigation zeigt nur, was die angemeldete Person mit ihren
Rollen erreicht. Dasselbe im Aufnahmeprozess (ADR-0122): Das Freigabeformular bot
bisher «user» oder «admin» und bietet jetzt alle vier — die Rolle wird dort vergeben,
wo die Administratorin ohnehin schon entscheidet. Beides ist Bequemlichkeit, keine Schranke — der Server weist
ohnehin ab —, aber ohne beides wäre die Massnahme eine reine API-Fähigkeit, und M10
hat gezeigt, dass das keine Fähigkeit ist.

**Abnahme** — der Nachweis ist Code:

1. ✅ Jede Route der Tabelle nennt eine Rolle; eine ohne fällt durch den
   Inventar-Test (`TestEveryRouteDeclaresAKnownRole`, `TestEveryMountedPatternDeclaresARole`).
2. ✅ Ein Konto mit nur `user` erreicht `POST /api/v1/deployments` nicht, ein Konto
   mit `modeler` schon (`TestOnlyAModelerMayDeploy`).
3. ✅ Ein API-Token erreicht nicht mehr als das Konto, das es ausgestellt hat, und
   ist nie `admin` (`TestAnAPITokenIsNeverAnAdministrator`, `TestTokenRolesNeverIncludeAdmin`).
4. ✅ Ein MCP-Werkzeugaufruf unterliegt derselben Regel wie derselbe Aufruf über
   HTTP (`TestMCPToolActsAsTheCallingPrincipal`).
5. ✅ Mit `--auth=false` ist alles davon wirkungslos (`TestAuthOffEnforcesNoRole`).
6. ✅ Nach einer Aktualisierung kann jedes bestehende Konto genau das, was es vorher
   konnte — und was danach enger gestellt wird, bleibt enger
   (`TestAnAccountFromBeforeRolesKeepsWhatItCouldDo`, `TestNarrowingAnAccountSurvivesARestart`).

### M10 — OAuth, damit ein gehosteter MCP-Client anschliessen kann ✅

M2 hat `/mcp` hinter die Grenze gebracht und M4 dem stdio-Adapter ein Credential
gegeben. Der Fall, den beide nicht abdecken: ein **gehosteter** Client. Ein
Connector auf claude.ai — oder irgendeine Agentenplattform, die den Server aus
ihrer eigenen Infrastruktur heraus im Auftrag einer Person erreicht, die im
Browser sitzt — hat keinen Ort für einen Bearer-Token. Sein Dialog bietet eine URL
und, unter «Erweitert», optional eine OAuth-Client-ID und ein Client-Geheimnis.
Sonst nichts.

Das ist kein Gedankenspiel: Ein laufender Connector gegen eine Atlas-Instanz hörte
in dem Moment auf zu funktionieren, als die Anmeldung kam. Der Client bekam `401`,
las `WWW-Authenticate: Bearer realm="atlas"`, fand keinen Verweis auf irgendetwas,
riet `/authorize` — und bekam `404`, was richtig ist, weil Atlas diese Route nicht
bedient.

Der Kern der Lücke ist schärfer als «MCP über HTTP braucht ein Credential», was M2
gelöst hat: **Ein API-Token ist ein Credential für eine Maschine, die ein Mensch
konfiguriert. Ein gehosteter Client ist eine Maschine, die niemand konfigurieren
kann.** Die Person kann nur «Verbinden» drücken. Ihr stattdessen ein Token in die
Hand zu geben hiesse, einen langlebigen Bearer auszustellen, der danach in der
Konfiguration eines Dritten liegt und für jeden lesbar ist, der diese
Konfiguration lesen darf — das Gegenteil dessen, wofür M3 gebaut wurde.

**Was die Spezifikation verlangt** (MCP-Autorisierung, Revisionen 2025-06-18 und
2025-11-25): Der MCP-Server ist ein OAuth-**Ressourcenserver**; er nimmt Tokens
entgegen, er muss sie nicht ausstellen. Verbindlich sind vor allem drei Dinge —
Metadaten des geschützten Ressourcenservers nach RFC 9728, per
`WWW-Authenticate`-Verweis auf dem `401` oder als Well-Known-URI; Prüfung der
Token-Zielgruppe nach RFC 8707 (ein Token für eine andere Ressource wird
abgewiesen, und Weiterreichen empfangener Tokens ist ausdrücklich verboten); und
PKCE mit `S256`, wobei der Client `code_challenge_methods_supported` prüfen
**muss** und abbrechen muss, wenn das Feld fehlt.

**Was den Umfang entscheidet:** Dynamische Client-Registrierung (RFC 7591) ist
*optional* — in der November-Revision von SHOULD auf MAY abgeschwächt —, und die
Reihenfolge, die ein Client einhalten soll, beginnt mit **vorregistrierten
Zugangsdaten**. Genau dafür hat der Connector-Dialog seine beiden Felder. Atlas
kann also anschliussfähig werden, **ohne RFC 7591 und ohne CIMD zu bauen**: Ein
Betreiber legt in der Console einen OAuth-Client an und trägt ID und Geheimnis im
Dialog ein.

Was dazukommt: zwei Well-Known-Dokumente und der `resource_metadata`-Verweis auf
dem `401` (die Ressourcenserver-Hälfte, klein und ohnehin geschuldet), sowie
`/authorize` mit Zustimmungsbildschirm und `/token` (die Autorisierungsserver-
Hälfte). Der ausgestellte Token trägt eine **Person**, nicht eine Rolle — nur so
überlebt die Eigenschaft aus M2, dass ein Werkzeugaufruf genau die Rechte dessen
hat, der ihn ausgelöst hat.

**Ehrlich dazugesagt:** Atlas wird damit zum Autorisierungsserver. Das ist eine
Schwelle: zwei neue öffentliche Routen, ein Zustimmungsbildschirm, den man richtig
bauen muss, und Anmelde-Drosselung (M7) und Audit-Log (M8) müssen sie vom ersten
Commit an abdecken. Ein halb gebauter Autorisierungsserver ist schlechter als
keiner — heute scheitert ein Client schnell und sichtbar, ein entdeckbarer, aber
kaputter Ablauf scheitert langsam. Deshalb: die Metadaten-Dokumente erst
ausliefern, wenn die Endpunkte dahinter funktionieren.

**Warum es Föderation (O-01) nicht erschwert, sondern vorbereitet:** Wenn Atlas
später an eIAM oder einen anderen OIDC-Anbieter delegiert, zeigen die
Ressourcenserver-Metadaten woandershin und die Autorisierungsserver-Hälfte wird
gelöscht. Nichts anderes bewegt sich. Genau deshalb wird die
Ressourcenserver-Hälfte zuerst gebaut.

**Stand: die Ressourcenserver-Hälfte ist umgesetzt** (`api/oauthmeta.go`). Zwei
öffentliche Dokumente unter `/.well-known/oauth-protected-resource` — eines für
den Server, eines für `/mcp` —, der `resource_metadata`-Verweis auf jedem `401`,
und `--external-url` für die absolute Origin, weil Atlas kein TLS terminiert und
die aus einer Anfrage abgeleitete Origin hinter einem Proxy `http://…` lautet.

Was das ändert und was nicht: Eine Abweisung ist jetzt lesbar statt stumm — der
Client findet ein Dokument, das die Ressource benennt, die ihn abgewiesen hat,
statt `/authorize` zu raten. **Ein gehosteter Connector funktioniert damit noch
nicht**, und soll es auch nicht: Das Dokument nennt bewusst keinen
Autorisierungsserver, weil einen zu nennen, dessen Token Atlas nicht prüfen kann,
den Client durch einen ganzen Ablauf schickte, um ihn am Ende mit dem Token in der
Hand abzuweisen.

**Der Entscheid ist angenommen und beide Hälften sind umgesetzt**, in der
festgelegten Reihenfolge: erst die gewählte Variante (Ressourcenserver plus
kleinster Autorisierungsserver), dann die dynamische Client-Registrierung, und
Föderation, wenn sie sich lohnt.

Damit ist der Ablauf vollständig: `/oauth/authorize` mit Zustimmungsbildschirm,
`/oauth/token` mit Authorization Code, PKCE (`S256`) und rotierenden
Refresh-Tokens, Client-Registrierung über `POST /api/v1/oauth-clients`, und die
Prüfung der Token-Zielgruppe bei der Ausstellung. Kein impliziter Ablauf, kein
Passwort-Grant, kein Client-Credentials-Grant — was nicht angeboten wird, gibt es
nicht. Atlas ist damit ein Autorisierungsserver; die neuen öffentlichen Routen
sind von M7 (Drosselung) und M8 (Audit, acht `auth.oauth_*`-Ereignisse) vom
ersten Commit an abgedeckt.

Die entscheidende Eigenschaft: **Der ausgestellte Token trägt eine Person.** Damit
überlebt die Zusage aus M2 einen Client, den niemand konfigurieren kann — ein
Werkzeugaufruf hat genau die Rechte dessen, der zugestimmt hat. Was der Token
erreicht, folgt dem, was zugestimmt wurde: Eine Zustimmung für `/mcp` bedient den
Transport und wird auf `/api/v1` abgewiesen.

Eine Zustimmung ist widerrufbar, und zwar von der Person selbst. Ein deaktiviertes
oder gelöschtes Konto verliert seine Zustimmungen — ein Connector darf das Konto
dahinter nicht überleben —, und eine Rollen- oder Gruppenänderung schreibt sie um,
statt sie fallen zu lassen.

**Schritt zwei ist ebenfalls umgesetzt: die dynamische Client-Registrierung**
(RFC 7591, `POST /oauth/register`, `api/oauthregister.go`). Sie entfernt den
Betreiberschritt vor der Person — ein Connector braucht nur noch die URL dieses
Servers.

Sie ist **standardmässig aus** (`--oauth-dynamic-registration`), und «aus» heisst
abwesend: Die Route wird nicht eingehängt und `registration_endpoint` steht nicht
in den Metadaten. Ein Client erfährt die Politik, statt auf eine Abweisung zu
laufen. Das ist der einzige unauthentisierte Endpunkt in Atlas, der dauerhaften
Zustand schreibt, und vier Eigenschaften tragen diesen Entscheid:

1. **Ein selbstregistrierter Client ist auf dem Zustimmungsbildschirm als solcher
   ausgewiesen** — in klaren Worten, über der Frage. Das ist die Massnahme, von
   der die übrigen abhängen: Ist Registrierung offen, bedeutet «eine Anwendung
   fragt nach Zugriff» nicht mehr, dass jemand sie geprüft hat, und ohne diesen
   Hinweis würde jede spätere Zustimmungsentscheidung stillschweigend entwertet.
   Der Name auf dem Bildschirm ist einer, den die Anwendung sich vor dreissig
   Sekunden selbst gegeben hat.
2. **Die Zahl selbstregistrierter Clients ist begrenzt, und die Grenze verdrängt,
   statt abzuweisen.** Eine Grenze, die nur abweist, ist ihr eigener
   Denial-of-Service: Wer die Tabelle zuerst füllt, sperrt alle anderen dauerhaft
   und von aussen aus. Verdrängt wird der älteste selbstregistrierte Client, dem
   nie jemand zugestimmt hat.
3. **Ein zugestimmter Client wird nie verdrängt** — sonst könnte ein Fremder
   jemandem den Zugang entziehen, indem er genug Clients registriert. Das
   verbleibende Risiko wird benannt statt weggeredet: Ein Client, der registriert
   ist und noch auf die Zustimmung seiner Person wartet, kann von einer Flut
   verdrängt werden und müsste sich neu registrieren — ein Fenster von Sekunden.
4. **Gedrosselt auf eigenem Budget**, nicht auf dem gemeinsamen öffentlichen: Eine
   Registrierungsflut darf nicht den Token-Tausch der Clients drosseln, die bereits
   registriert sind — sonst wird der Missbrauch dieses Endpunkts zum Ausfall für
   alle anderen.

Registrieren allein erreicht weiterhin nichts: ID und Geheimnis erlauben zu
*fragen*; erreicht wird nur, was eine Person zustimmt, und begrenzt durch ihr
eigenes Konto. `auth.oauth_client_self_registered` hält jede Selbstregistrierung
fest, getrennt vom Administratorakt — «eine Administratorin hat eine Anwendung
hinzugefügt» und «ein Fremder hat eine hinzugefügt» sind derselbe Satz mit
verschiedenen Folgen.

Client ID Metadata Documents (CIMD), die andere Hälfte der Vollausbau-Variante,
sind **nicht** gebaut und nicht geplant.

**Bedienbar ist das Ganze über die Console:** `Console → AI access`
(`api/web/aiaccess.js`). Dort wird eine Anwendung angelegt und liefert die drei
Werte, nach denen der Connector-Dialog fragt — MCP-URL, Client-ID und das einmalig
angezeigte Geheimnis —, dort steht, was registriert ist und was sich selbst
registriert hat, und dort zieht eine Person ihre eigene Zustimmung zurück. Letzteres
ist der Grund, warum es diese Seite geben muss und nicht nur die Endpunkte: Die
Zustimmung ist das Einzige an diesem Entscheid, das der Person gehört und nicht der
Betreiberin, und was nur über einen API-Aufruf widerrufbar ist, ist für die meisten
Menschen nicht widerrufbar.

Föderation hängt nicht am Wollen, sondern an ihrer Voraussetzung: Sie ordnet
Claims Rollen zu und wartet damit auf **M9**. Was sie ersetzt, ist der in Schritt
eins gebaute Autorisierungsserver — die Ressourcenserver-Hälfte bleibt in jedem
Fall stehen. Deshalb gilt für alles, was jetzt gebaut wird: nichts darf
voraussetzen, dass Atlas der Aussteller bleibt.

Entscheid: [`ADR-0200`](../adr/0200-mcp-oauth-resource-server.md).

### M11 — Berechtigungen auf Worker-Ebene

**Problem.** Ein Worker gehört niemandem (1.5). Der Datensatz kennt Name, Art,
Endpunkt und Zugangsdaten-Verweis — aber kein Feld, das sagt, wessen er ist. Das
war eine zutreffende Beschreibung, solange ein Worker Infrastruktur war: eine
Jira-Instanz, ein SMTP-Relay, einmal für die Installation eingerichtet.

Seit ADR-0075 ist ein Worker auch ein Weg **herein**: Ein Inbound-Abonnement
beobachtet ein externes Subjekt und veröffentlicht, was ankommt, als
Atlas-Nachricht — die Prozesse startet.

Der auslösende Fall existiert noch nicht: Inbound ist heute nur clio, der
Mail-Worker ist ausgehend (ADR-0079/0093), ein *eingehender* ist weder gebaut
noch geplant. Er ist das, wonach gefragt wurde — und ein Postfach ist persönlich,
wie eine Jira-Instanz es nie war. Genau deshalb wird jetzt entschieden: Solange das
Postfach hypothetisch ist, kostet die Antwort nichts; sobald Menschen eines haben,
kostet jede Antwort eine Migration.

**Zwei Fragen, nicht eine.** Die Konfiguration zu schützen beantwortet die
gestellte Frage nicht. Selbst wenn niemand mehr ein Abonnement auf meinem
Worker anlegen kann, kann weiterhin jede Person einen Prozess deployen, dessen
Nachrichten-Startereignis `mail-eingegangen` heisst — und meine Ereignisse starten
ihn, weil der Name der ganze Schlüssel ist.

**Vorschlag** (Entwurf:
[`ADR-0205`](../adr/0205-connector-ownership-and-event-delivery.md)):

1. **Der Worker bekommt die drei Felder, die ein Projekt schon hat** —
   `ownerId`, `visibility`, `members[{ref, role}]` aus ADR-0071. Wörtlich
   dieselben, damit Gruppen (ADR-0180) ohne weiteres Zutun funktionieren: «teile
   es mit meiner Frau» und «teile es mit dem Team» sind derselbe Handgriff.
   Durchgesetzt wird es dort, wo ADR-0071 seine eigenen Regeln durchsetzt — in den
   HTTP-Handlern am aufgelösten Prinzipal, nicht in der Engine. Ein Abonnement
   erbt den Geltungsbereich seines Workers, wie ein Artefakt den seines
   Projekts erbt.
2. **Ein Abonnement beansprucht seinen Nachrichtennamen.** Solange der Anspruch
   steht, ist dieser Name nur an Definitionen im Geltungsbereich des Anspruchs
   zustellbar. Geprüft an **beiden** Türen, weil eine Prüfung zu einem Zeitpunkt
   keine Regel ist: beim Anlegen des Abonnements gegen bereits deployte
   Definitionen, und beim Deployen gegen bestehende Ansprüche. Beide Abweisungen
   nennen die Nachricht, nie die Gegenpartei — es geht darum, die Zustellung zu
   verhindern, nicht darum, fremde Postfächer offenzulegen.

**Ehrlich dazugesagt — was das ist und was nicht.** Es ist ein **Tor an zwei
Türen**, keine Isolation. Es stoppt den Unfall und den beiläufigen Zugriff, also
genau den gemeldeten Fall. Es überlebt keine Administratorin, und es hängt daran,
dass beide Prüfungen laufen; fehlt eine, ist es Dekoration. Die *echte* Isolation
wäre, dass die veröffentlichte Nachricht ihren Geltungsbereich mitträgt und die
Korrelation ihn vergleicht — das ändert den Nachrichtenwert und berührt
`applyToState`, ist also ein eigener Entscheid mit eigener Wiederanlauf-Geschichte.
Sie ist im Entwurf als Nachfolger benannt, nicht als Absicht verschwiegen.

**Der Preis, ebenfalls benannt.** Nachrichtennamen werden zu einem Namensraum mit
Ansprüchen: Ein Deploy kann künftig aus einem Grund abgewiesen werden, der in
einer Konfiguration liegt, die die deployende Person nicht sehen darf. Und
Worker aus der Zeit vor M11 tragen keinen Eigentümer; sie werden unter
`--auth` **administrativ**, bis eine Administratorin ihnen einen zuweist. Das
weicht von ADR-0071 ab, das altlastige Artefakte offen liess — dort wurde eine
Fähigkeit ergänzt, hier wird eine Lücke geschlossen, und eine Sicherheitsmassnahme,
die jede bestehende Installation ausnimmt, schliesst nichts.

**Reihenfolge.** Wann immer ein Inbound-E-Mail-Worker gebaut wird, gehört M11
davor, damit ein persönliches Postfach nie — auch nicht vorübergehend —
installationsweit ist. Zu M9 steht es quer: M9 ordnet Rollen Endpunktgruppen zu,
M11 ordnet einem Objekt einen Eigentümer zu. Beide zahlen auf O-02 ein, keines wartet auf das andere.

**Stand: Schritt 1 ist umgesetzt** (`api/connectorscope.go`). Der Worker trägt
Eigentümer, Sichtbarkeit und Mitgliederliste; die Handler für Worker *und*
Abonnements prüfen die Rolle; Freigeben, Zurückziehen, Versiegeln und Übergeben
gibt es als Endpunkte **und** in der Console neben jedem Worker. Zwei Dinge kamen
beim Bauen dazu, die im Entwurf nicht standen und dort jetzt nachgetragen sind:

- **Existenz ist nicht Konfiguration.** Der Modeler füllt seine Worker-Auswahl
  aus derselben Liste. Hätte man die Liste schlicht eingeschränkt, stünde jede
  Person ohne Eigentum vor einem leeren Auswahlfeld — eine Freigaberegel, die
  Menschen an der Arbeit hindert, ist keine Freigaberegel. Die Liste hat deshalb
  zwei Formen: ab *viewer* der Datensatz, darunter ein Katalogeintrag aus Name, Art
  und Zustand. Geschützt sind Endpunkt, Absender, Zugangsdaten-Verweis,
  Mitgliederliste und die Inbound-Abonnements.
- **Die Worker-Prüfung darf kein fremdes Geheimnis borgen.**
  `POST /api/v1/connectors/test` löst den Zugangsdaten-Verweis auf, den der Body
  nennt, und verschickt damit echte Mail. Das war bis jetzt für **jedes** Konto ein
  «Mail versenden als irgendwer, mit irgendwessen Zugangsdaten». Den Datensatz zu
  sperren und das stehen zu lassen wäre Theater gewesen. Die Regel: Ein Verweis darf
  nur nennen, wer einen Worker bearbeiten darf, der ihn schon benutzt.

**Schritt 2 ist ebenfalls umgesetzt** (`api/messageclaim.go`): Ein Abonnement
beansprucht seinen Nachrichtennamen, geprüft an beiden Türen — beim Deployen gegen
bestehende Ansprüche, beim Anlegen eines Abonnements gegen bereits deployte
Definitionen. Auch das Umbenennen eines Abonnements und das Wiedereinschalten eines
abgeschalteten gehen durch dieselbe Tür, sonst wäre der Änderungs-Endpunkt der Weg
um den Anlege-Endpunkt herum. Beide Abweisungen nennen die Nachricht und sonst
nichts.

**Beim Bauen kam heraus, dass die beiden Türen verschiedene Fragen stellen** — der
Entwurf hatte das nicht gesehen. Ein Worker hat einen Geltungsbereich, ein
*Deployment* hatte gar keine Identität: ADR-0071 hat die Laufzeit ausdrücklich
draussengelassen, und nirgends stand, wer eine Definition deployt hat. Die zweite
Tür brauchte genau diese eine Tatsache, und sie gibt es jetzt (`deployedBy`). Sie
ist bewusst *kein* Geltungsbereich, sondern das eine Feld, mit dem eine
projektlose Definition die Frage «gehört die dir» beantworten kann — geprüft mit
derselben Regel, die jedes andere projektlose Artefakt schon benutzt.

**Ehrlich dazugesagt, was weiterhin gilt:** Es bleibt ein **Tor an zwei Türen**,
keine Isolation. Eine Administratorin kommt durch beide. Eine Definition aus der
Zeit davor trägt weder Deployer noch Projekt, gilt damit als herrenlos und offen —
ihr Anspruch auf einen Namen steht, bis sie neu deployt wird; das Gegenteil würde
jede Aktualisierung brechen, bei der jemand einen Namen beansprucht, auf den sein
eigener alter Prozess hört. Und geprüft wird die Zustellung *an eine Definition*,
nie an eine laufende Instanz: Während eine Nachricht korreliert, wird nichts davon
befragt.

**Abnahme** — dieselbe Regel wie in Kapitel 6, der Nachweis ist Code:

1. ✅ Ein gewöhnliches Konto sieht einen fremden privaten Worker nicht, ändert
   ihn nicht und löscht ihn nicht; es legt darauf kein Abonnement an und liest seine
   Abonnements nicht. Genau die fünf Zeilen aus 1.5, jede als Test:
   `TestAConnectorBelongsToWhoeverMadeIt`.
2. ✅ Eine Freigabe an eine **Gruppe** wirkt für jedes Mitglied und endet mit der
   Mitgliedschaft — dieselbe Eigenschaft, die ADR-0180 für Projekte hat.
   `TestSharingAConnectorWithAGroup`, dazu `TestSharingAConnectorFollowsTheRoles`
   für viewer/editor/owner.
3. ✅ Der Modeler kann weiterhin gegen jeden Worker autorisieren, auch gegen
   einen fremden: `TestEveryoneCanStillAuthorAgainstAConnector`.
4. ✅ Die **Laufzeit** fragt den Geltungsbereich nicht: ein privater Worker steht
   in der Registry, sonst würde jedes Modell parken, das ihn nennt.
   `TestTheRuntimeResolvesAConnectorNobodyIsSignedInFor`.
5. ✅ Ein Worker ohne Eigentümer (aus der Zeit davor) ist administrativ, sein
   Katalogeintrag bleibt sichtbar: `TestAConnectorFromBeforeOwnershipIsAdminOnly`.
6. ✅ Kein fremdes Geheimnis über die Worker-Prüfung:
   `TestBorrowingAnotherConnectorsCredentialIsRefused`.
7. ✅ Mit `--auth=false` ist alles davon wirkungslos: der Server ist per Deklaration
   offen, und M11 fügt dort keine einzige Verweigerung hinzu.
   `TestAnOpenServerSharesEverything`.
8. ✅ Ein Deploy, dessen Definition einen beanspruchten Nachrichtennamen empfangen
   kann, wird abgewiesen; die Abweisung nennt die Nachricht und **nicht** die
   Gegenpartei. `TestAStrangerCannotDeployIntoSomebodyElsesMessage`.
9. ✅ Der Anspruch wird auch gegen **bereits deployte** Definitionen geprüft — sonst
   genügt es, zuerst zu deployen.
   `TestAClaimIsRefusedWhenSomebodyElseIsAlreadyListening`.
10. ✅ Der Änderungs-Endpunkt ist kein Weg um den Anlege-Endpunkt herum:
    `TestTheUpdateEndpointIsNotTheWayAround`.
11. ✅ Ein Projekt-Deploy bleibt Alles-oder-nichts: Wird der dritte Entwurf
    abgewiesen, ist auch der erste nicht registriert.
    `TestDeployingAProjectStopsBeforeItStarts`.

### M12 — Föderierte Authentisierung ✅

**Problem, gemessen.** Atlas kennt genau **einen** Weg, wie aus einem Menschen ein
Prinzipal wird: Benutzername und lokales Passwort.

| | |
|---|---|
| Wege, eine **Person** zu authentisieren | 1 (lokales Passwort) |
| Stellen, an denen eine Session entsteht | 1 (`handleLogin`, `api/users.go`) |
| Produktive Lesestellen von `User.Source` | 1 (die Console-Liste) |
| Produktive Lesestellen von `User.ExternalID` | **0** |
| Abhängigkeiten für OIDC, JWT oder JWKS | 0 |

[ADR-0044](../adr/0044-user-management-and-authentication-boundary.md) hat die
Haken bewusst gesetzt und schreibt sie auch hin: «morgen kann der Prinzipal aus
einem OIDC/JWT-Bearer kommen, indem nur die Middleware getauscht wird». Benutzt hat
sie bis heute nichts. Das ist zugleich die gute Nachricht — die Naht ist **eine**
Funktion — und das ganze Risiko: Alles, was eine fremde Identität echt macht, gibt
es noch nicht.

Was das kostet, steht schon in den Akten. **R-03** (gelb): lokale Passwörter statt
eIAM, kein MFA, keine zentrale Passwortrichtlinie und — der Punkt, nach dem eine
Prüfstelle zuerst fragt — **kein automatischer Entzug beim Austritt**. Ein Konto
hier überlebt die Anstellung seiner Inhaberin, bis eine Administratorin es von Hand
entfernt.

**Vorschlag** (Entwurf:
[`ADR-0210`](../adr/0210-federated-authentication.md)):

1. **Atlas wird OIDC-Relying-Party.** Authorization Code mit PKCE, Discovery,
   Prüfung des ID-Tokens gegen die JWKS des Anbieters. Drei Endpunkte und dieselbe
   Naht: Am Ende steht die Session, die auch ein lokales Login erzeugt, mit
   derselben Momentaufnahme aus Rollen und Gruppen. Damit gilt jede Regel seit M5
   unverändert weiter, statt für eine zweite Art Aufrufer neu bewiesen zu werden.
2. **Das Konto entsteht beim ersten Login** — `Source` wird `oidc`, `ExternalID`
   das `sub` des Anbieters. Die Identität ist das Subjekt, nicht die E-Mail-Adresse:
   Eine Adresse kann neu vergeben werden, ein `sub` nicht.
3. **Ein Claim vergibt zunächst keine Rolle.** Ein föderiertes Konto bekommt `user`,
   genau wie ein lokal angelegtes (M9), und Rollen vergibt eine Administratorin in
   Atlas. Die Abbildung von Claims auf Rollen und Gruppen ist der **zweite**
   Schritt, ausdrücklich konfiguriert, mit «leere Abbildung heisst: kein Claim
   vergibt etwas». *(Beide Schritte sind inzwischen gebaut; siehe «Stand» unten.)*
4. **Das lokale Login bleibt**, mindestens als Notfallzugang. Eine Installation,
   deren Anbieter nicht erreichbar ist, muss administrierbar bleiben; die
   Aussperr-Sperre aus ADR-0044 gilt unverändert.

**Warum nicht der billigere Weg.** Der naheliegende Kurzschluss ist ein
vertrauenswürdiger Header von einem authentisierenden Reverse Proxy. Das ist keine
Authentisierung, sondern der Entschluss, einem Header zu glauben — und in einem
Einzelbinary, das jemand direkt auf seinem Port erreicht, ist dieser Glaube genau
eine fehlgeleitete Anfrage von einem offenen Login entfernt. Er bleibt im Entscheid
als **Betriebsmuster** mit ausgeschriebenen Leitplanken stehen, weil es
Installationen mit einem solchen Proxy gibt und O-01 ausdrücklich Dokumentation
dafür verlangt. Die Antwort des Produkts ist er nicht.

SAML ist zurückgestellt, nicht verworfen: Spricht das Ziel eIAM SAML, ist das eine
zweite Fassade auf dieselbe Naht. Ein LDAP-Bind wäre am billigsten und schickt das
Passwort weiterhin durch Atlas — also genau die Eigenschaft, die Föderation
beseitigen soll.

**Ehrlich dazugesagt.** Atlas übernimmt damit die Prüfung fremder Tokens, also
sicherheitskritischen Code, den es bisher nicht hatte. Er ist begrenzt und prüfbar,
aber ihn falsch zu machen ist schlechter, als ihn nicht zu haben. Und ein Ausfall
des Anbieters wird zu einem Ausfall von Atlas für föderierte Konten; der lokale
Notfallzugang ist die Antwort, und er ist nur dann eine, wenn jemand dieses Passwort
aufbewahrt.

**Reihenfolge.** Schritt 1 ist der Anmeldeweg mit `user` als einziger Rolle.
Schritt 2 ist die Abbildung von Claims auf Rollen und Gruppen — bewusst danach,
denn ab diesem Tag verwaltet, wer die Gruppen des Anbieters pflegt, die Rollen von
Atlas. Das ist der Sinn der Föderation und zugleich ihre schärfste Kante, also soll
es eine Betreiberin einschalten, nachdem das Login selbst bewiesen ist. Die
Autorisierungsserver-Hälfte aus M10 bleibt vorerst stehen: Sie abzulösen setzt
voraus, dass der fremde Anbieter Tokens mit Atlas als Zielgruppe ausstellt, und das
ist eine Aussage über dessen Konfiguration, die ein selbstgehosteter PoC nicht
treffen kann.

**Stand: Schritt 1 ist umgesetzt** (`api/oidc.go`, `api/oidctoken.go`,
`api/oidclogin.go`). Zwei Endpunkte, eine Naht: `/auth/oidc/start` schickt die
Person zum Anbieter, `/auth/oidc/callback` prüft, was zurückkommt, und endet dort,
wo auch ein lokales Login endet. Konfiguriert wird über `--oidc-issuer`,
`--oidc-client-id` und `--oidc-client-secret` beziehungsweise die entsprechenden
`ATLAS_OIDC_*`-Variablen. Die Anmeldemaske bietet die Schaltfläche nur, wenn ein
Anbieter konfiguriert ist, und behält das Passwortformular in jedem Fall.

Drei Dinge kamen beim Bauen dazu:

- **Der erneute Abruf der Schlüssel darf nicht gedrosselt werden.** Der erste
  Entwurf erlaubte einen Abruf pro Minute, damit eine unbekannte Schlüssel-ID kein
  Hebel wird. Ein Test, der den Schlüssel des Anbieters rotieren lässt, hat gezeigt,
  was das kostet: eine Minute lang jede Anmeldung abgewiesen, mit «Signatur
  ungültig» und ohne erkennbare Ursache auf beiden Seiten. Den Hebel gibt es nicht —
  bis zur Prüfung kommt nur, wer einen von diesem Server ausgestellten `state`, das
  passende Cookie und einen vom **Anbieter** akzeptierten Code hat.
- **Eine Namenskollision ist weder eine Abweisung noch eine Übernahme.** Trägt ein
  lokales Konto den Namen schon, bekommt das föderierte die nächste freie Variante.
  Die beiden zusammenzuführen bleibt eine bewusste administrative Handlung.
- **Eine gescheiterte Anmeldung sagt nichts.** Sie landet auf der Anmeldemaske,
  ohne Grund; welcher Prüfschritt fehlschlug, steht im Audit-Log.

**Abnahme** — der Nachweis ist Code:

1. ✅ Ohne konfigurierten Anbieter verhält sich der Server exakt wie heute; die
   Route existiert nicht und die Anmeldemaske zeigt keinen zusätzlichen Weg
   (`TestWithoutAProviderNothingChanges`, `e2e/login-sso.spec.mjs`).
2. ✅ Ein ID-Token mit falschem Aussteller, falscher Zielgruppe, abgelaufener
   Gültigkeit, fehlendem `nonce`, unbekanntem Schlüssel oder «alg: none» führt zu
   **keiner** Session (`TestAnIDTokenIsRefused`, `TestAFederatedLoginIsRefused`).
3. ✅ Der erste Login einer unbekannten Person erzeugt ein Konto mit `Source=oidc`,
   gesetztem `ExternalID` und genau der Rolle `user`
   (`TestAFederatedLoginCreatesAnAccountAndASession`).
4. ✅ Ein zweiter Login derselben Person erzeugt **kein** zweites Konto, auch dann
   nicht, wenn sich Name oder E-Mail-Adresse geändert haben
   (`TestASecondFederatedLoginReusesTheAccount`).
5. ✅ Ein deaktiviertes Konto bekommt auch über den Anbieter keine Session
   (`TestADisabledAccountCannotFederate`).
6. ✅ Das lokale Login funktioniert unverändert weiter; das Passwortformular bleibt
   auch bei konfiguriertem Anbieter stehen.

**Stand: Schritt 2 ist ebenfalls umgesetzt** (`api/oidcmapping.go`). Eine
Administratorin benennt **einen** Claim und eine Liste **exakter Werte**, und jeder
Wert benennt die Rollen, die er vergibt, und die Atlas-Gruppen, in die er die Person
setzt. Bearbeitet wird das unter Console → Organization → Single sign-on
(`GET`/`PUT /api/v1/settings/oidc-mapping`, in beide Richtungen `admin` — die Regeln
nennen die Gruppenkennungen des Anbieters, und die Anmeldemaske braucht sie nicht).

Vier Eigenschaften machen daraus keine Falltür:

- **Ein Claim vergibt, er vergibt nie durch Abwesenheit.** Jede Regel ist ein
  exakter Wertevergleich. Wer beim Anbieter in keiner der genannten Gruppen ist,
  trifft auf nichts und bekommt nichts. Eine Regel «alle, die sich anmelden können,
  bekommen `modeler`» gibt es nicht — das wäre ein Konfigurationsschalter im Kostüm
  eines Claims.
- **`user` ist ein Boden, keine Vergabe.** Die Abbildung entscheidet über `admin`,
  `modeler` und `operator`. Wer sich überhaupt anmelden kann, behält `user`; eine
  verschwundene Gruppe beim Anbieter nimmt niemandem die eigene Aufgabenliste.
- **Sie besitzt die Gruppen, die sie nennt, und keine anderen.** Rollen sind ein
  geschlossener Satz von vier Namen, die Atlas selbst vergibt — «die Abbildung
  entscheidet die Rollen» ist deshalb ein vollständiger Satz, und eine von Hand
  vergebene Rolle überlebt die nächste Anmeldung nicht. Gruppen sind ein offener
  Satz, den Menschen aus eigenen Gründen anlegen; über eine Gruppe, die in keiner
  Regel vorkommt, hat die Abbildung nichts gesagt, und eine von Hand gesetzte
  Mitgliedschaft dort bleibt bestehen.
- **Eine Abbildung, die nichts bedeuten kann, wird dort abgewiesen, wo sie
  geschrieben wird.** Eine Regel, die eine Rolle nennt, die Atlas nicht durchsetzt,
  oder eine gelöschte Gruppe, vergibt nichts — still, bei jeder Anmeldung, bis
  jemand herausfindet, warum die neue Kollegin nicht deployen kann. Geprüft wird
  beim Schreiben, gegen die Gruppen im selben Lauf-Schleifen-Zug, der den Datensatz
  ablegt.

Und zwei Zustände bleiben unterscheidbar: Eine Abbildung, die niemand geschrieben
hat, vergibt *absichtlich* nichts; eine, die nicht gelesen werden kann, ist eine
kaputte Installation und weist die Anmeldung ab. Das Zweite als das Erste zu lesen
hiesse, dass ein Plattenfehler bei der nächsten Anmeldung stillschweigend allen die
Rollen nimmt.

**Eine Aussperr-Sperre beim Login gibt es bewusst nicht.** Eine Prüfung «nimm die
letzte `admin`-Rolle nicht weg» müsste ausgerechnet bei der Anmeldung laufen, die
am dringendsten gelingen soll, und machte die Wiederherstellbarkeit einer
Installation davon abhängig, in welcher Reihenfolge sich Menschen anmelden. Der
Notfallzugang ist der bereits dokumentierte: das lokale Administratorkonto und
`atlas reset-password` auf dem Host.

**Abnahme Schritt 2** — der Nachweis ist Code:

7. ✅ Ein Claim vergibt Rollen und Gruppenmitgliedschaft, eine nicht abgebildete
   Gruppe vergibt nichts (`TestAClaimDecidesRolesAndGroups`).
8. ✅ Verschwindet der Claim, verschwinden Rolle und Mitgliedschaft bei der nächsten
   Anmeldung — eine von Hand gesetzte Mitgliedschaft in einer nicht genannten Gruppe
   bleibt (`TestAClaimThatGoesAwayTakesItsGrantsWithIt`).
9. ✅ Eine ausgeschaltete Abbildung entscheidet nichts, auch mit passenden Claims
   (`TestAMappingThatIsOffDecidesNothing`).
10. ✅ Eine vergebene Rolle öffnet genau die Routen, die sie benennt, und keine
    darüber hinaus (`TestAMappedRoleReachesTheRouteItNames`).
11. ✅ Eine Abbildung mit unbekannter Rolle oder nicht existierender Gruppe wird
    beim Schreiben abgewiesen und nennt die Stelle; lesen und schreiben darf nur
    `admin` (`TestAMappingIsRefusedWhenItCannotMeanAnything`,
    `TestTheMappingEndpointIsAdminOnlyAndChecked`).
12. ✅ Eine unlesbare Abbildung ist keine ausgeschaltete: Die Anmeldung wird
    abgewiesen, statt still alle Rollen zu entziehen
    (`TestAMappingThatCannotBeReadIsNotAMappingThatIsOff`).

**Offen bleibt** die vom Anbieter ausgelöste Abmeldung (RP-initiated logout) und
SAML, falls eIAM es verlangt.

---

## 4 Stufenplan

### Stufe 0 — der Zustand vor Stufe 1

*Überholt, hier als Beleg behalten:* Seit Stufe 1 ist `--auth` der Standard und
sind `/mcp` und `/metrics` vom Produkt selbst geschützt — die Proxy-Regeln unten
sind Verteidigung in der Tiefe, nicht mehr das, was trägt.

Was ein Betrieb sofort tun musste, wenn damals ein PoC laufen sollte. Die Massnahmen
stehen ausformuliert im ISDS-Konzept, Kapitel 6.4 (M-01, M-02, M-09, M-13); in
Kurzform:

- `--auth` aktiv, Bootstrap-Passwort geändert, `ATLAS_ADMIN_PASSWORD` aus
  Umgebung und Log entfernt.
- Reverse Proxy mit TLS; **`/mcp` und `/metrics` dort sperren.**
- `--docs=false`, `--user-provisioning=false`, nicht benötigte Skriptsprachen aus
  (`--powershell=false --python=false --javascript=false`).
- Eigener Dienstbenutzer, Dateirechte 0750/0600, Datenträgerverschlüsselung.

**Ehrlich dazugesagt:** das ist Kompensation, nicht Lösung. Sie hängt vollständig
an einer Proxy-Konfiguration, die das Produkt nirgends erzwingt und deren Fehlen es
nicht bemerkt. Genau das ist der Grund für Stufe 1.

### Stufe 1 — PoC-produktivtauglich

~~M1 → (M2 + M3 + M4) → M5 → M7 + M8 → M6~~. **Alle acht Massnahmen sind
umgesetzt.**

Damit gilt: keine Schnittstelle ohne authentisierten Prinzipal, im
Auslieferungszustand, per Test belegbar — und ohne Fussnote. Offen bleiben genau
die Routen auf der ausgeschriebenen Liste: die zwei Sonden, was die Anmeldemaske
selbst liest, die tokenbasierten öffentlichen Links und die statische Oberfläche.
**R-08 ist grün**, O-07 erledigt, O-03 und O-04 weitgehend.

Danach gilt: **keine Schnittstelle ohne Login**, per Test belegbar, und die
Absicherung überlebt eine vergessene Proxy-Regel. R-08 geht von rot auf grün, O-07
ist erledigt, O-03 und O-04 sind weitgehend erledigt.

### Stufe 2 — vor breiterem Einsatz

~~M9 (Rollen, O-02)~~ ✅ → ~~M12 (Föderation OIDC/eIAM, O-01)~~ ✅ → dauerhafte
Sessions und Sitzungsverwaltung (O-14) → Verschlüsselung ruhender Daten (O-06).

**M9 ist umgesetzt.** Damit gibt es die vier Namen, auf die eine Föderation ihre
Claims abbildet — und sie sind eine öffentliche Zusage, kein Implementierungsdetail:
`admin`, `modeler`, `operator`, `user`.

**M12 ist umgesetzt**, beide Schritte: der Anmeldeweg über OpenID Connect und die
Abbildung der Claims auf genau diese vier Namen und auf die Gruppen. Damit ist der
automatische Rollenentzug beim Austritt gebaut — für Installationen, die einen
Anbieter konfigurieren und die Abbildung einschalten. Offen bleibt daraus die vom
Anbieter ausgelöste Abmeldung und SAML, falls eIAM es verlangt.

**M10 stand quer dazu** und ist umgesetzt — beide Hälften und die dynamische
Client-Registrierung, in der Reihenfolge des Entscheids. Offen bleibt daraus nur
noch die Föderation.

**M11 steht ebenfalls quer dazu** und wartet auf nichts. Es ordnet einem Objekt
einen Eigentümer zu, wo M9 einer Rolle eine Endpunktgruppe zuordnet; beide zahlen
auf O-02 ein, keines setzt auf dem anderen auf. Seine eigene Reihenfolge ist die
einzige, die zählt: vor einem Inbound-E-Mail-Worker, falls und sobald einer
gebaut wird.
Wer zuerst föderiert (O-01), löscht die Autorisierungsserver-Hälfte wieder: Dann
zeigen die Ressourcenserver-Metadaten auf den fremden Anbieter, und alles andere
bleibt stehen. Deshalb setzt nichts im heutigen Code voraus, dass Atlas der
Aussteller bleibt.

---

## 5 Bewusst nicht im Umfang

| Punkt | Warum nicht |
|-------|-------------|
| OIDC/SAML/eIAM (O-01) | Braucht zuerst die Rollen, denen Claims zugewiesen werden — sonst entsteht ein Mapping ins Leere. Stufe 2. |
| MFA | Folgt sinnvollerweise der Föderation, nicht dem lokalen Passwort. |
| Vollständiges RBAC (O-02) | Stufe 2. M1 und M2 sind so gebaut, dass es später an genau einer Stelle andockt. M9 und M11 sind zwei Scheiben davon, kein Ersatz dafür. |
| Isolation der Zustellung in der Engine | Der echte Nachfolger von M11: Die veröffentlichte Nachricht trüge ihren Geltungsbereich mit, und die Korrelation verglicht ihn. Das ändert den Nachrichtenwert und berührt `applyToState` — ein eigener Entscheid mit eigener Wiederanlauf-Geschichte, nicht ein Detail von M11. Bis dahin ist M11 ausdrücklich ein Tor und keine Isolation. |
| Mandantenfähigkeit (O-09) | Betriebsmuster «eine Installation je Schutzbedarfsklasse» bleibt die Aussage. |
| TLS im Produkt (R-02) | Bleibt Aufgabe des vorgelagerten Proxys. |
| Verschlüsselung ruhender Daten (O-06) | Unabhängige Achse; ändert nichts an der Zugriffsfrage. |
| CIMD (Client ID Metadata Documents) | Die andere Hälfte der Vollausbau-Variante der MCP-Spezifikation. Sie löst dasselbe Problem wie RFC 7591 eine Stufe weiter draussen, und nichts, worauf Atlas getroffen ist, verlangt danach. Dynamische Client-Registrierung selbst ist inzwischen umgesetzt (M10, Schritt zwei) — standardmässig aus. |

Begründung für den Schnitt: jeder dieser Punkte ist gross, und **keiner ist nötig,
damit die Aussage «jede Schnittstelle verlangt einen Login» wahr wird.**

---

## 6 Abnahme — woran man es prüft

Der Nachweis ist Code, nicht Prosa. Nach Stufe 1 muss gelten:

1. ✅ Die öffentliche Routenmenge wird gegen eine ausgeschriebene Liste gehalten;
   eine nicht deklarierte Route ist geschützt.
   `TestPublicRoutesAreExactlyTheAllowlist`, `TestUndeclaredRouteIsGated`,
   `TestEveryPublicAPIRouteEntryIsRegistered`, `TestAccessClassification`. *(M1)*
2. ✅ `POST /mcp` ohne Credential → `401` mit `WWW-Authenticate: Bearer`; mit
   gültiger Sitzung → `200`. `TestMCPTransportRequiresAuthentication`,
   `TestMCPTransportAdmitsASignedInCaller`. *(M2a)*
3. ✅ Ein MCP-Werkzeugaufruf handelt unter der Identität des Aufrufers: ein Admin
   erreicht ein admin-geschütztes Werkzeug, ein angemeldeter Nicht-Admin nicht.
   `TestMCPToolActsAsTheCallingPrincipal`, dazu
   `TestHTTPForwardsTheCallersCredential` und
   `TestHTTPCallerCredentialBeatsTheAdapters` im `mcp`-Paket. *(M2b)*
4. ✅ Ein widerrufenes und ein abgelaufenes API-Token werden abgewiesen; das
   Geheimnis ist über keinen Endpunkt erneut abrufbar; ein `worker`-Token erreicht
   nur die vier Worker-Operationen; ein unbekannter Geltungsbereich erreicht nichts.
   `TestAPITokenRevocationIsImmediate`, `TestAPITokenIndexRefusesAnExpiredToken`,
   `TestAPITokenSecretIsReturnedOnce`, `TestWorkerScopeReachesOnlyAWorkersOperations`,
   `TestUnknownScopeReachesNothing`. Am Binary verifiziert: ein entfernter
   `atlas worker` arbeitet mit dem Token und scheitert ohne es an «authentication
   required». *(M3)*
5. ✅ Ein Server ohne Flags verlangt einen Login; `--auth=false` erzeugt eine
   Warnzeile unter `auth.disabled`. Am gebauten Binary geprüft: anonym `401` auf
   `/api/v1/processes`, `/api/v1/users`, `/api/v1/openapi.json`, `/api/docs` und
   `POST /mcp`, `200` auf `/healthz`, `/readyz`, `/metrics` und `/api/v1/info`;
   nach Anmeldung `200` auf allen. *(M5)*
6. ✅ Nach fünf Fehlanmeldungen je Konto antwortet der Login mit `429`, und ein
   reales und ein erfundenes Konto werden gleich behandelt.
   `TestLoginThrottleRefusesAFlood`,
   `TestLoginThrottleDoesNotRevealWhetherAnAccountExists`,
   `TestSuccessfulLoginClearsTheFailureBudget`, dazu die Einheitentests in
   `api/loginguard_internal_test.go`. *(M7)*
7. ✅ An-/Abmeldung, Fehlanmeldung, Drosselung, Autorisierungsverweigerung sowie
   Konto- und Deploy-Token-Lebenszyklus erscheinen als maschinenlesbare
   Log-Ereignisse mit Akteur — und **kein** Secret im Log.
   `TestAuditTrailRecordsTheAccountLifecycle`,
   `TestAuditTrailNeverCarriesASecret`. *(M8)*
8. ✅ `/metrics` verlangt ein Credential; ein `metrics`-Token erreicht genau diese
   eine Route, ein `worker`-Token nicht; `/healthz` und `/readyz` bleiben offen.
   `TestMetricsRequiresACredential`, `TestMetricsScopeReachesOnlyTheExposition`.
   Am Binary verifiziert. *(M6)*
9. `docs/compliance/isds-konzept.md` (Kap. 5.2.2, 5.2.3, 5.4.2, 6.1) und
   `isds-offene-punkte.md` (O-03, O-04, O-07) sind in derselben Änderung
   nachgeführt — so verlangt es die Pflegeregel in [`README.md`](README.md).
   Für M1 und M2 ✅ erfolgt.

Ergänzend, nicht Teil der Abnahme: ein `atlas check`-Lauf, der die
sicherheitsrelevante Konfiguration bewertet, macht die jährliche Prüfung
reproduzierbar (O-15).

---

## 7 Auswirkungen auf bestehende Entscheide

| ADR | Heutige Aussage | Änderung |
|-----|-----------------|----------|
| [ADR-0016](../adr/0016-mcp-server-over-http-api.md) | «`/mcp` ist unauthentisiert — Proxy davorstellen» | Der Transport authentisiert selbst. **Neuer Entscheid nötig.** |
| [ADR-0049](../adr/0049-internal-service-auth-for-mcp.md) | Interner Token als MCP-Credential; die Folgearbeit «ein auth-fähiger Transport für `/mcp`» ist dort ausdrücklich benannt | Genau diese Folgearbeit. Der interne Token verliert die MCP-Rolle. |
| [ADR-0044](../adr/0044-user-management-and-authentication-boundary.md) | Erzwingung opt-in | Standard umgekehrt (M5). Die `*Principal`-Grenze selbst bleibt unverändert — sie ist der Grund, warum das alles klein bleibt. |
| [ADR-0129](../adr/0129-remote-deployment-targets.md) | Deploy-Tokens als Sonderfall | Speicher und Muster werden zu API-Tokens verallgemeinert (M3); Deploy-Tokens werden ein Geltungsbereich davon. |
| [ADR-0043](../adr/0043-openapi-spec-and-embedded-api-explorer.md) | `openapi.json` vor dem Login lesbar | Hinter die Schranke, sobald Auth aktiv ist (M5). |
| [ADR-0142](../adr/0142-prometheus-metrics.md) | `/metrics` ungated wie `/healthz` | Hinter der Schranke, mit Geltungsbereich `metrics` (M6). Was ADR-0142 über *Inhalt* und Kosten der Exposition sagt, bleibt unverändert. |
| [ADR-0041](../adr/0041-connector-management-and-secret-store.md) | Ein Worker ist Betriebskonfiguration ohne Eigentümer | Bekommt Eigentümer und Freigabeliste (M11). Was ADR-0041 über den Geheimnisverweis sagt (nie der Wert, nur die Referenz), bleibt unverändert. |
| [ADR-0071](../adr/0071-sharing-scopes.md) | Freigabe gilt für **Entwurfszeit-Inhalte**; Laufzeit ausdrücklich draussen | M11 trägt dieselben drei Felder über diese Linie — auf den Worker und, per Anspruch auf den Nachrichtennamen, auf die Zustellung. Die Linie selbst fällt nicht: die Isolation *in* der Engine bleibt draussen. |
| [ADR-0075](../adr/0075-clio-inbound-event-bridge.md) | Ein Abonnement veröffentlicht unter einem frei gewählten Nachrichtennamen | Der Name wird beansprucht und an zwei Türen geprüft (M11). **Neuer Entscheid nötig.** |
| [ADR-0043](../adr/0043-openapi-spec-and-embedded-api-explorer.md) | Die Routentabelle ist die einzige Quelle für die bediente Fläche und ihre Spezifikation | Sie trägt zusätzlich die geforderte Rolle je Route (M9). Was ADR-0043 über Nichtabdriften sagt, gilt damit auch für die Berechtigung. |
| [ADR-0194](../adr/0194-api-tokens.md) | Ein API-Token trägt einen Geltungsbereich und keine Rolle | Es trägt die Rollen seiner Ausstellerin, geschnitten mit dem Geltungsbereich (M9) — sonst erreichte es nach M9 nichts mehr. |

Als ADR-Entwürfe ohne Nummer (Nummernvergabe beim Merge, ADR-0170):

- ✅ [`0199-route-access-classes.md`](../adr/0199-route-access-classes.md) — M1
- ✅ [`0196-authenticated-mcp-transport.md`](../adr/0196-authenticated-mcp-transport.md)
  — M2, ersetzt die `/mcp`-Aussage aus ADR-0016 und erledigt die Folgearbeit aus
  ADR-0049
- ✅ [`0194-api-tokens.md`](../adr/0194-api-tokens.md) — M3
- ✅ [`0198-metrics-behind-the-boundary.md`](../adr/0198-metrics-behind-the-boundary.md) — M6
- ✅ [`0195-auth-on-by-default.md`](../adr/0195-auth-on-by-default.md) — M5
- ✅ [`0197-login-throttle-and-audit-log.md`](../adr/0197-login-throttle-and-audit-log.md)
  — M7 und M8 zusammen: eine Drosselung, deren Verweigerungen unsichtbar sind,
  lässt sich weder abstimmen noch belegen, und eine Spur ohne Fehlanmeldungen
  lässt genau den Eintrag aus, mit dem jede Prüfung beginnt

M4 hat keinen eigenen Entscheid: es vervollständigt
`draft-authenticated-mcp-transport` und steht dort.

Neu und noch offen:

- 🔲 [`0200-mcp-oauth-resource-server.md`](../adr/0200-mcp-oauth-resource-server.md)
  — M10. Erweitert ADR-0196 um den Fall, den dieser nicht bedacht hat: einen
  Client, dem niemand ein Credential in die Hand geben kann. Er nimmt ADR-0194 das
  Credential *nicht* weg — ein OAuth-Token ist bewusst kein API-Token — und stützt
  sich für die neuen öffentlichen Routen auf die Zugriffsklassen aus ADR-0199 sowie
  für deren Drosselung und Protokollierung auf ADR-0197.
- 🔲 [`0209-roles-per-endpoint-group.md`](../adr/0209-roles-per-endpoint-group.md)
  — M9. Füllt das Rollenfeld aus ADR-0044 endlich mit Bedeutung und annotiert die
  Routentabelle aus ADR-0043. Er hebt ADR-0071 nicht auf, sondern steht quer dazu:
  eine Rolle sagt, *welche Art* von Operation, ein Geltungsbereich, *welches
  Objekt*. Beide müssen passieren.
- 🔲 [`0205-connector-ownership-and-event-delivery.md`](../adr/0205-connector-ownership-and-event-delivery.md)
  — M11. Trägt die Freigabe-Sprache aus ADR-0071 und die Gruppen aus ADR-0180 auf
  den Worker und auf die Zustellung seiner Ereignisse. Er nimmt ADR-0071 seine
  Linie nicht weg: die Isolation *in* der Engine bleibt ausdrücklich ein eigener,
  späterer Entscheid.

---

## 8 Zusammenfassung in drei Sätzen

Der Prüfbefund trifft zu, und bei `/mcp` ist die Lage schärfer als beschrieben:
der Endpunkt ist nicht nur ungeschützt, sondern bringt unter `--auth` sein eigenes
gültiges Credential mit — 71 Werkzeuge inklusive Deploy, und damit Codeausführung,
für jeden, der den Port erreicht. Die Ursache ist strukturell und deshalb billig zu
beheben: die Schranke entscheidet nach Pfadpräfix statt nach einer erklärten
Zugriffsklasse, und der MCP-Adapter setzt eine eigene Identität ein, statt die des
Aufrufers durchzureichen. Acht kleine Massnahmen — zusammen deutlich unter dem
Aufwand einer Föderationsanbindung — machen aus «ist sicher, wenn der Betrieb alles
richtig konfiguriert» ein **«ist sicher, und ein Test beweist es»**, und das ist
genau die Schwelle, ab der ein produktiver PoC vertretbar wird.

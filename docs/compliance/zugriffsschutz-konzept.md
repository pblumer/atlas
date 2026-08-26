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
| **M1** — Zugriffsklassen je Route + Inventar-Test | ✅ umgesetzt — [`ADR-draft-route-access-classes`](../adr/draft-route-access-classes.md), `api/access.go` |
| **M2** — `/mcp` hinter dieselbe Grenze, Identität durchreichen | ✅ umgesetzt — [`ADR-draft-authenticated-mcp-transport`](../adr/draft-authenticated-mcp-transport.md), `api.WithMCP` + `mcp/client.go` |
| **M4** — `atlas mcp --token` / `ATLAS_TOKEN` | ✅ umgesetzt — im selben Entscheid, `cmd/atlas/main.go` |
| **M5** — `--auth` standardmässig an | ✅ umgesetzt — [`ADR-draft-auth-on-by-default`](../adr/draft-auth-on-by-default.md) |
| **M7** — Anmelde-Härtung | ✅ umgesetzt — [`ADR-draft-login-throttle-and-audit-log`](../adr/draft-login-throttle-and-audit-log.md), `api/loginguard.go` |
| **M8** — Sicherheits-Audit-Log | ✅ umgesetzt — im selben Entscheid, `api/audit.go` |
| M3, M6 | offen |

Kapitel 1 beschreibt weiterhin den **Befund**, also den Zustand vor diesen beiden
Massnahmen. Das ist Absicht: es ist der Beleg dafür, was behoben wurde, und die
Begründung für die restlichen Massnahmen. Was heute gilt, steht in Kapitel 6.

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
| M3 | API-Tokens als erste Klasse | M | O-07, Teil R-04 | G3 |
| M4 ✅ | `atlas mcp --token` / `ATLAS_TOKEN` | XS | Nebenbefund 1.3 | G3 |
| M5 ✅ | `--auth` standardmässig an | S | R-08, R-03 | G5 |
| M6 | `/metrics` auf eigenen Listener bzw. Zugriffsklasse | S | R-08, O-07 | G1 |
| M7 ✅ | Anmelde-Härtung: Drosselung je Adresse *und* je Konto | S | O-04, R-12 | — |
| M8 ✅ | Sicherheits-Audit-Log | S | O-03, R-13 | — |
| M9 | Rollen je Endpunktgruppe *(Stufe 2)* | M–L | O-02, R-04, R-09 | G4 |

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

### M3 — API-Tokens als erste Klasse

**Problem.** Es gibt drei uneinheitliche Credentials: den flüchtigen internen
Token (kein Ablauf, kein Geltungsbereich, kein Widerruf, geteilt mit den Workern),
Deploy-Tokens (sauber gemacht) und das Session-Cookie.

**Lösung.** Das Deploy-Token-Muster verallgemeinern — es ist im Repository schon
vorbildlich umgesetzt: 32 Byte CSPRNG, **nur der SHA-256 liegt auf Platte**, das
Geheimnis wird genau einmal ausgeliefert, admin-vergeben, benannt, widerrufbar. Ein
`apiToken` ergänzt es um `subject` (Benutzer oder Dienst), `scopes`, `expiresAt`
und `lastUsedAt`. Der Speicher ist ein Aufruf von `sidecar.NewStore`.

Wenige, grobe Geltungsbereiche genügen für einen PoC: `runtime:read`,
`runtime:write`, `design:write`, `worker`, `mcp` — durchgesetzt mit demselben
`ServeMux`-Mechanismus wie `deployAgentAllowed`.

Damit hat der Worker ein eigenes Credential, ein CI-Lauf ein eigenes, und der
entfernte MCP-Adapter überhaupt eines. Der interne Token verliert seine Rolle als
Universalschlüssel; falls er bleibt, dann nur noch für den prozessinternen
Bootstrap.

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

### M6 — `/metrics` trennen

`--metrics-addr` bindet die Prometheus-Exposition auf einen eigenen Listener
(z. B. `127.0.0.1:9090`); leer gelassen bleibt sie wie heute am Hauptport, dort
dann in der Zugriffsklasse `operator` (Token nötig). `/healthz` und `/readyz`
bleiben offen und inhaltslos — eine Bereitschaftssonde, die ein Credential
braucht, ist eine Sonde, die im Ernstfall nicht funktioniert.

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

### M9 — Rollen je Endpunktgruppe *(Stufe 2, hier nur eingeordnet)*

Vier Rollen statt einer: `admin`, `modeler` (Design-Time und Deploy), `operator`
(Laufzeitsteuerung), `user` (Aufgaben). Der wichtigste einzelne Schnitt ist
`POST /api/v1/deployments` — weil Deployen Codeausführung bedeutet (R-09), ist
«jeder Angemeldete darf deployen» auf einer Produktion die falsche Vorgabe. Details
in O-02; wegen G4 gilt jede Regel automatisch auch für MCP.

---

## 4 Stufenplan

### Stufe 0 — heute, ohne Codeänderung

Was ein Betrieb sofort tun muss, wenn heute ein PoC laufen soll. Die Massnahmen
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

~~M1~~ → (~~M2~~ + M3 + ~~M4~~) → ~~M5~~ → ~~M7 + M8~~ → M6. **M1, M2, M4, M5,
M7 und M8 sind umgesetzt.** Offen: **M3** (API-Tokens) und **M6** (`/metrics`) —
M6 ist das letzte Stück, das R-08 von gelb auf grün bringt.

Danach gilt: **keine Schnittstelle ohne Login**, per Test belegbar, und die
Absicherung überlebt eine vergessene Proxy-Regel. R-08 geht von rot auf grün, O-07
ist erledigt, O-03 und O-04 sind weitgehend erledigt.

### Stufe 2 — vor breiterem Einsatz

M9 (Rollen, O-02) → Föderation OIDC/eIAM (O-01, setzt auf den Rollen auf) →
dauerhafte Sessions und Sitzungsverwaltung (O-14) → Verschlüsselung ruhender Daten
(O-06).

---

## 5 Bewusst nicht im Umfang

| Punkt | Warum nicht |
|-------|-------------|
| OIDC/SAML/eIAM (O-01) | Braucht zuerst die Rollen, denen Claims zugewiesen werden — sonst entsteht ein Mapping ins Leere. Stufe 2. |
| MFA | Folgt sinnvollerweise der Föderation, nicht dem lokalen Passwort. |
| Vollständiges RBAC (O-02) | Stufe 2. M1 und M2 sind so gebaut, dass es später an genau einer Stelle andockt. |
| Mandantenfähigkeit (O-09) | Betriebsmuster «eine Installation je Schutzbedarfsklasse» bleibt die Aussage. |
| TLS im Produkt (R-02) | Bleibt Aufgabe des vorgelagerten Proxys. |
| Verschlüsselung ruhender Daten (O-06) | Unabhängige Achse; ändert nichts an der Zugriffsfrage. |

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
4. Ein widerrufenes und ein abgelaufenes API-Token werden abgewiesen; das
   Geheimnis ist über keinen Endpunkt erneut abrufbar. *(M3)*
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
8. `docs/compliance/isds-konzept.md` (Kap. 5.2.2, 5.2.3, 5.4.2, 6.1) und
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
| [ADR-0142](../adr/0142-prometheus-metrics.md) | `/metrics` ungated wie `/healthz` | Eigener Listener bzw. Zugriffsklasse `operator` (M6). |

Als ADR-Entwürfe ohne Nummer (Nummernvergabe beim Merge, ADR-0170):

- ✅ [`draft-route-access-classes.md`](../adr/draft-route-access-classes.md) — M1
- ✅ [`draft-authenticated-mcp-transport.md`](../adr/draft-authenticated-mcp-transport.md)
  — M2, ersetzt die `/mcp`-Aussage aus ADR-0016 und erledigt die Folgearbeit aus
  ADR-0049
- `draft-api-tokens.md` — M3, offen
- ✅ [`draft-auth-on-by-default.md`](../adr/draft-auth-on-by-default.md) — M5
- ✅ [`draft-login-throttle-and-audit-log.md`](../adr/draft-login-throttle-and-audit-log.md)
  — M7 und M8 zusammen: eine Drosselung, deren Verweigerungen unsichtbar sind,
  lässt sich weder abstimmen noch belegen, und eine Spur ohne Fehlanmeldungen
  lässt genau den Eintrag aus, mit dem jede Prüfung beginnt

M4 hat keinen eigenen Entscheid: es vervollständigt
`draft-authenticated-mcp-transport` und steht dort.

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

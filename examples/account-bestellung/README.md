# Account-Bestellung (Entra ID) 🧩

Eine Atlas-**Applikation mit öffentlichem Start-Formular**, **DMN-gesteuertem
Kontotyp-Mapping**, menschlicher Freigabe und **Entra-Provisionierung** — plus ein
**einbettbares HTML+JS-Widget**, das die Bestellung von einer beliebigen Website aus
startet. Schreibt scharf gegen **blumer.net**, aber fail-closed nur gegen klar
benannte **Test-Objekte**.

## Der Ablauf

```
Start (öffentliches Formular account-order: Vorname, Nachname, Kontotyp, Begründung)
  → [DMN] Profil bestimmen   KontotypMapping: A/E/T/S → { kuerzel, accountEnabled,
                             kategorie, usageLocation }
  → [Script+Output-Mappings] mailNick, upn = jml-test-<kuerzel>-<mailNick>@blumer.net,
                             displayName, kategorie
  → (X) Test-Objekt?   ── sonst ──▶ Ende "Kein Test-Objekt"  (kein Entra-Aufruf)
        │ jml-test-*@blumer.net
  → 🔑 Freigabe (account-freigabe) – Admin setzt Initialpasswort
  → (X) Freigegeben?   ── ablehnen ▶ Ende "Abgelehnt"
        │ anlegen
  → [entra create-user] accountEnabled & usageLocation aus dem DMN-Profil
  → Ende "Konto bereitgestellt"
```

## Kontotypen (DMN-Tabelle `KontotypMapping`)

| Typ | kürzel | accountEnabled | Kategorie | UPN-Beispiel |
|---|---|---|---|---|
| **A** Administrativ | `a` | **true** | Administrativ | `jml-test-a-anna.muster@blumer.net` |
| **E** Bildung/Extern | `e` | **true** | Bildung/Extern | `jml-test-e-…@blumer.net` |
| **T** Test | `t` | **false** | Test | `jml-test-t-…@blumer.net` |
| **S** Service/Dienst | `s` | **false** | Dienstkonto | `jml-test-s-…@blumer.net` |

Der Typ wird über eine **Entscheidungstabelle** abgebildet, nicht über if/else im
Prozess — wer die Regeln je Typ ändert, ändert die Tabelle. Das `jml-test-`-Präfix
baut der Prozess (nicht die Tabelle), damit die **Test-Objekt-Grenze eine
Prozess-Invariante** bleibt.

> **Eine Feinheit, die dieses Beispiel zeigt:** ein Script-Task trägt genau **ein**
> `<zeebe:script>`. Mehrere abgeleitete Werte entstehen deshalb über
> `zeebe:ioMapping`-**Output-Mappings** (`mailNick`, `upn`, `displayName`,
> `kategorie`) — jeweils direkt aus schon vorhandenen Variablen.

## Das einbettbare Widget

[`account-order-widget.html`](account-order-widget.html) ist eine **self-contained**
Datei (kein externes CSS/JS): ein hübsches Bestellformular mit **dynamischem Verhalten
je Kontotyp** und **Live-UPN-Vorschau**. Zwei Einbett-Wege:

1. **Per `<iframe>`** (kein Serverumbau nötig) — bettet die von Atlas gerenderte
   Public-Form-Seite ein:
   ```html
   <iframe src="https://atlas.blumer.cloud/public/forms/DEIN_TOKEN"
           style="width:100%;max-width:560px;height:760px;border:0"></iframe>
   ```
   Oder das Widget selbst per iframe (es liest `?atlas=…&token=…` aus der URL).

2. **Direkt auf einer fremden Seite** (das schöne Widget, cross-origin) — dafür muss
   der Atlas-Server die Origin deiner Seite per CORS erlauben (ADR-draft-embed-public-forms-cross-origin):
   ```
   atlas serve --public-forms-cors "https://deine-seite.example"
   ```
   Ohne Token läuft das Widget im **Demo-Modus** und zeigt nur, was gesendet würde.

Beide Wege posten an `POST /public/forms/{token}/start` (ADR-0029) —
token-basiert, rate-limited, ohne Login, ohne Cookie.

## Deployen (über die Atlas-MCP-Tools)

```
atlas_create_application     name="Account-Bestellung"                → appId
atlas_upload_decision_model  handle="kontotyp"  xml=<kontotyp.dmn>
atlas_register_decision      name="KontotypMapping" modelRef="kontotyp" projectId=appId
atlas_save_form              id=account-order     schema=…  projectId=appId
atlas_save_form              id=account-freigabe  schema=…  projectId=appId
atlas_save_draft             xml=<account-bestellung.bpmn>  projectId=appId
atlas_publish_application    id=appId                       → Definition-Key
```

**Öffentlichen Link erzeugen** (macht den Prozess öffentlich startbar — bewusst eine
eigene Handlung):
```
POST /api/v1/public-links   {"processId":"proc_account_bestellung"}   → { token, url }
```
Danach `token` ins Widget setzen (`data-token` / `?token=`).

## Artefakte

| Datei | Zweck |
|---|---|
| `account-bestellung.bpmn` | Prozess (`proc_account_bestellung`) |
| `kontotyp.dmn` | Entscheidung `KontotypMapping` (A/E/T/S → Profil) |
| `form-account-order.json` | Öffentliches Start-Formular |
| `form-account-freigabe.json` | Freigabe-Formular (Admin) |
| `account-order-widget.html` | Einbettbares HTML+JS-Bestell-Widget |

# Account-Bestellung (Entra ID) 🧩

Eine Atlas-**Applikation mit öffentlichem Start-Formular**, **DMN-gesteuertem
Kontotyp-Mapping**, menschlicher Freigabe und **Entra-Provisionierung** — plus ein
**einbettbares HTML+JS-Widget**, das die Bestellung von einer beliebigen Website aus
startet. Schreibt scharf gegen **contoso.com**, und nur gegen klar benannte
**Test-Objekte**: das `jml-test-`-Präfix steckt im `attributes`-Ausdruck des
create-user-Tasks, also im Modell.

Tenant und Worker-Name sind **Platzhalter**: `contoso.com` steht für den eigenen
Entra-Tenant, `contoso` für den Namen, unter dem der Entra-Worker konfiguriert ist.
Beides ist vor dem Deployen im Modell, in den Formularen, im Widget und in der
UPN-Prüfung auf die eigenen Werte zu setzen.

## Der Ablauf

```
Start (öffentliches Formular account-order: Vorname, Nachname, Kontotyp, Begründung)
  → [DMN] Profil bestimmen   KontotypMapping: A/E/T/S → { kuerzel, accountEnabled,
                             kategorie, usageLocation }
  → [Script] kategorie aus dem Profil übernehmen
  → 🔑 Freigabe (account-freigabe) – Admin setzt Initialpasswort
  → (X) Freigegeben?   ── ablehnen ▶ Ende "Abgelehnt"
        │ anlegen
  → [entra create-user] baut UPN, mailNickname und Anzeigenamen selbst aus Vorname,
                        Nachname und kuerzel; accountEnabled & usageLocation aus dem Profil
  → Ende "Konto bereitgestellt"
```

## Kontotypen (DMN-Tabelle `KontotypMapping`)

| Typ | kürzel | accountEnabled | Kategorie | UPN-Beispiel |
|---|---|---|---|---|
| **A** Administrativ | `a` | **true** | Administrativ | `jml-test-a-anna.muster@contoso.com` |
| **E** Bildung/Extern | `e` | **true** | Bildung/Extern | `jml-test-e-…@contoso.com` |
| **T** Test | `t` | **false** | Test | `jml-test-t-…@contoso.com` |
| **S** Service/Dienst | `s` | **false** | Dienstkonto | `jml-test-s-…@contoso.com` |

Der Typ wird über eine **Entscheidungstabelle** abgebildet, nicht über if/else im
Prozess — wer die Regeln je Typ ändert, ändert die Tabelle. Das `jml-test-`-Präfix
steht nicht in der Tabelle, damit die **Test-Objekt-Grenze im Modell sichtbar** bleibt.

> **Was dieses Beispiel über Personendaten zeigt.** Der Prozess deklariert
> `atlas:personal="vorname,nachname"` ([ADR-0314](../../docs/adr/0314-portal-personal-data.md)).
> Eine so deklarierte Variable wird verschlüsselt, bevor sie ein Kommando wird, und
> Chiffrat lässt sich nicht vergleichen oder verketten — der Compiler **verweigert**
> deshalb ein Deployment, in dem sie in einem Ausdruck steht, den die Engine auswertet.
>
> Früher rechnete hier ein Script-Task `mailNick`, `upn` und `displayName` aus Vorname
> und Nachname. Diese Rechnung steht jetzt im `attributes`-Ausdruck des
> create-user-Tasks: **Worker-Ausdrücke wertet der Worker aus**, und dort existiert der
> Klartext für die Dauer eines Aufrufs und wird nie im Klartext zurückgeschrieben.
>
> Das hat einen Preis, und er ist der ehrliche Teil: ein zweites fail-closed Gateway
> prüfte den gebauten UPN vor jedem Schreibzugriff gegen `jml-test-*@contoso.com`. Diese
> Prozessvariable gibt es nicht mehr, also gibt es das Gatter nicht mehr. Die Grenze
> trägt jetzt das Literal im `attributes`-Ausdruck — im Modell sichtbar und im Review
> prüfbar, aber **strukturell statt zur Laufzeit** geprüft.

## Das einbettbare Widget

[`account-order-widget.html`](account-order-widget.html) ist eine **self-contained**
Datei (kein externes CSS/JS): ein hübsches Bestellformular mit **dynamischem Verhalten
je Kontotyp** und **Live-UPN-Vorschau**. Zwei Einbett-Wege:

1. **Per `<iframe>`** (kein Serverumbau nötig) — bettet die von Atlas gerenderte
   Public-Form-Seite ein:
   ```html
   <iframe src="https://atlas.example.com/public/forms/DEIN_TOKEN"
           style="width:100%;max-width:560px;height:760px;border:0"></iframe>
   ```
   Oder das Widget selbst per iframe (es liest `?atlas=…&token=…` aus der URL).

2. **Direkt auf einer fremden Seite** (das schöne Widget, cross-origin) — dafür muss
   der Atlas-Server die Origin deiner Seite per CORS erlauben (ADR-0186):
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

# Onboarding-Self-Service (Entra ID) 🚀

Eine Atlas-**Applikation mit Formularen**, die einen neuen Arbeitsplatz im Tenant
**blumer.net** über den Entra-Worker (ADR-0172) anlegt — **scharf**, aber
ausschließlich gegen klar benannte **Test-Objekte**.

## Der Ablauf

```
Start (onb-start: Vorname, Nachname, UPN, Abteilung, Lizenz?, Gruppe?)
  → [Script] Vorschlag              displayName + mailNickname aus den Namen
  → (X) Test-Objekt?    ── sonst ──▶ Ende "Kein Test-Objekt"  (kein Entra-Aufruf)
        │ jml-test-*@blumer.net
  → 🔑 User-Task "Onboarding freigeben" (onb-freigabe) – Admin setzt Initialpasswort
  → (X) Freigegeben?    ── ablehnen ▶ Ende "Abgelehnt"
        │ anlegen
  → [entra create-user] Benutzer anlegen (Worker "blumer_net" → konto)
  → (X) Lizenz?         ── ohne ──▶┐  (übersprungen, wenn keine lizenzSku)
        │ lizenzSku gesetzt        │
  → [entra assign-license]         │
  → (X) Gruppe?         ── ohne ──▶┤  (übersprungen, wenn keine gruppeId)
        │ gruppeId gesetzt         │
  → [entra add-group-member]       │
  → Ende "Arbeitsplatz bereit" ◀───┘
```

Lizenz und Gruppe sind **optionale, fail-closed** Schritte: das jeweilige Gateway
nimmt den *Überspringen*-Pfad als Default, nur eine gesetzte `lizenzSku` bzw. `gruppeId`
löst den echten Entra-Aufruf aus. Die id-Kette trägt sie: `create-user` schreibt das
Konto nach `konto`, die Folgeschritte adressieren `=konto.id`.

## Zwei fail-closed Grenzen vor jedem Schreibzugriff

Beide Gateways nehmen den **ablehnenden** Pfad als Default. Nur eine ausdrückliche,
positive Bedingung führt zum Schreiben:

1. **Test-Objekt-Gate.** Der UPN muss mit `jml-test-` beginnen **und** auf
   `@blumer.net` enden (`= starts with(upn, "jml-test-") and ends with(upn,
   "@blumer.net")`). Jeder andere UPN endet **vor** jedem Entra-Aufruf bei „Kein
   Test-Objekt". Das ist die maßgebliche Grenze — die `pattern`-Validierung im
   Start-Formular ist nur Komfort und lässt sich (z. B. bei per MCP gestarteten
   Instanzen ohne Formular) umgehen, das BPMN-Gateway nicht.
2. **Freigabe-Gate.** Nur die ausdrückliche Entscheidung `anlegen` ruft
   `create-user`. Ein fehlender oder unerwarteter Wert landet nie beim Schreiben.

Das **Initialpasswort** ist ein Laufzeitwert: es wird erst im Freigabe-Formular
gesetzt und pro Feld als FEEL in den `passwordProfile`-Teil des `create-user`-Body
eingesetzt — es steht nie im Modell.

## Worker & Berechtigung

Der Worker-Typ `entra` ist **worker-only** (ADR-0164/0172): das Tenant-Credential
liegt nie in der Engine. Die Service-Tasks parken, bis eine Entra-Worker-Instanz sie
abholt; auf `atlas.blumer.cloud` beaufsichtigt die Engine diese Instanz standardmäßig,
sobald der Worker `blumer_net` unter *Console → Workers* konfiguriert ist. Benötigte
Anwendungsberechtigungen (mit Administratorzustimmung): **`User.ReadWrite.All`**; für die optionalen Schritte
zusätzlich **`Organization.Read.All`** (SKUs) und **`Group.ReadWrite.All`**.

## Deployen (über die Atlas-MCP-Tools)

```
atlas_create_application  name="Onboarding-Self-Service"        → appId
atlas_save_form           id=onb-start     schema=…  projectId=appId
atlas_save_form           id=onb-freigabe  schema=…  projectId=appId
atlas_save_draft          xml=<onboarding-selfservice.bpmn>     projectId=appId
atlas_publish_application  id=appId                              → Definition-Keys + Release
```

## Erfasste Prozessvariablen

- **onb-start:** `vorname`, `nachname`, `upn`, `abteilung`, `lizenzSku` (optional),
  `gruppeId` (optional)
- **Script:** `displayName`, `mailNick`
- **onb-freigabe:** `displayName` (editierbar), `entscheidung`, `initialpasswort`,
  `ablehnungsgrund`
- **create-user:** `konto` (das angelegte Konto samt `id`, trägt `=konto.id` in die
  optionalen Folgeschritte)

## Artefakte

| Datei | Zweck |
|---|---|
| `onboarding-selfservice.bpmn` | Prozess (`proc_entra_onboarding_selfservice`) |
| `form-onb-start.json` | Start-Formular (Antrag) |
| `form-onb-freigabe.json` | Freigabe-Formular (Admin) |

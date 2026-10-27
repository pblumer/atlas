// What it takes to make one Worker Type actually work, written down where the choice
// is made: in the service task's properties panel, beside the type an author just
// picked, and in the Console dialog where an operator fills the fields in.
//
// The catalog in editor.js says what a task *states* (a target, an operation, a result
// variable). It deliberately says nothing about what has to exist outside Atlas first —
// an app registration, a service account key, a shared spreadsheet — and that is the
// part a new installation stumbles over: the model is right, the task is right, and
// nothing happens because a checkbox at the provider is missing. That knowledge existed
// only in the handbook, which is another page, in another tab, that an author has to
// know to open.
//
// So each Worker Type carries it here: a one-line `needs` (always visible, it answers
// "does this need a Worker record and a credential at all?"), the ordered `steps` to
// get there, the one `trap` that catches people, and the `anchor` of the handbook
// card that says the same thing at length — the handbook is served by this same
// server (/handbuch.html), so the link works on an installation with no internet.
//
// One entry per catalog id, including the ids that need nothing: "nothing to configure"
// is an answer, and leaving it unsaid is what makes someone go looking for a dialog
// that does not exist. api/workertypedocs_internal_test.go holds catalog, docs and
// handbook to each other, so a new Worker Type cannot ship without its setup.
//
// The strings are markup and are rendered as such. They are static module content —
// never a value from a model, a record or a request — which is why this is the one
// place in the panel that does not escape what it renders.
//
// Every entry carries a `checked` date, and the panel prints it. These steps name menu
// paths in somebody else's product — *IAM & Admin → Service accounts* — which is what
// makes them worth writing and what will silently stop being true when that product is
// rearranged. Nothing here can detect that: the text goes on looking authoritative, and
// the person it misleads is the one least able to tell whether they or the instructions
// are wrong. The date does not fix that; it hands the reader the one fact that lets them
// judge it for themselves, and it gives the staleness a name a test can hold
// (TestSetupDocsAreRecentlyChecked).

// HANDBOOK_URL is the manual this server serves itself (api/web/handbuch.html). A
// relative URL on purpose: an air-gapped installation has the same handbook as a
// public one, and neither reaches an external docs site.
const HANDBOOK_URL = "/handbuch.html";

// The vault is the same sentence in a dozen entries, so it is written once. Console ›
// Secrets holds the value; the model and the Worker record hold only its name (I6).
const VAULT = `<i>Console &rsaquo; Secrets</i>`;
const WORKERS = `<i>Console &rsaquo; Workers</i>`;

// WORKER_TYPE_DOCS is keyed by the SERVICE_TASK_KINDS id, so the panel looks its entry
// up by exactly what the author picked.
//
// - `anchor`  the handbook section or card id this links to.
// - `title`   what the handbook calls it; the link says so rather than "Handbook".
// - `checked` YYYY-MM: when these steps were last read against the real thing.
// - `needs`   one line, always visible: what must exist before a task of this type runs.
// - `steps`   the ordered setup, provider first, Atlas second, proof last.
// - `trap`    the failure this type is actually reported with — omitted where there is none.
// - `reference` optional { anchor, title }: a handbook card on what the tasks *do* — every
//             operation, its result, the error codes a boundary can catch — linked beside
//             the setup card, for a type whose operations need more than the field hints.
export const WORKER_TYPE_DOCS = {
  worker: {
    anchor: "runbook-jobworker", title: "Job worker",
    checked: "2026-09",
    needs: "No Worker record and no credential — the job type is the whole contract, and a worker process you run leases it.",
    steps: [
      `Give the task a <b>job type</b>: a stable name your worker subscribes to, e.g. <code>payment</code>. It is the only thing the model says about the work.`,
      `Run a worker against this server: <code>atlas worker --server http://localhost:8080 --handle payment=/usr/local/bin/pay.sh</code>. With authentication on, add <code>--token</code> (or the <code>ATLAS_TOKEN</code> environment variable).`,
      `The job reaches the command on <b>stdin</b> as JSON. Whatever JSON object it prints on <b>stdout</b> becomes the variables the job completes with; a non-zero exit fails the job and carries stderr as the message.`,
      `Watch it under ${WORKERS}: the worker reports itself there with the job types it serves.`,
    ],
    trap: `A token sitting on the task with <b>no incident</b> means nobody is leasing this job type. The job is written and waiting — correctly; there is just no worker.`,
  },

  rest: {
    anchor: "runbook-rest", title: "HTTP REST",
    checked: "2026-09",
    needs: "Nothing to configure on the server: URL, method, headers and body live in this task. A credential is named here, never pasted.",
    steps: [
      `Put the credential in the vault first: ${VAULT} &rarr; new secret, e.g. <code>crm_token</code>, holding the token or password itself.`,
      `Pick the authentication type (Basic, Bearer, API key) and enter that key as the <b>Secret reference</b>. The model stores the reference only, so an export, a version and a diagram carry no secret.`,
      `Where the call runs on a worker rather than in the engine, the same reference travels as <code>ATLAS_CONNECTOR_&lt;REF&gt;_TOKEN</code>: a worker atlas supervises is handed the vault's value at spawn — and only for the references the deployed models actually name — while a worker you run yourself reads it from its own environment.`,
      `Name a <b>result variable</b>; the JSON answer lands there and is addressable as <code>=antwort.feld</code>.`,
    ],
    trap: `<code>401</code> is the credential, <code>403</code> the permission. Both park the token on an incident that names the status — read it before changing the URL.`,
  },

  scim: {
    anchor: "runbook-scim", title: "SCIM provisioning",
    checked: "2026-09",
    needs: "Nothing to configure on the server: the provider's base URL lives in this task, its credential is a vault reference.",
    steps: [
      `At the identity provider, create a <b>provisioning token</b> for its SCIM 2.0 endpoint (Okta, Entra ID and most IdPs issue one per application) and note the base URL, e.g. <code>https://idp.example.com/scim/v2</code>.`,
      `Store the token in the vault: ${VAULT}, e.g. <code>scim_idp</code>.`,
      `In this task: the base URL, the resource (<code>Users</code> or <code>Groups</code>), authentication <b>Bearer token</b>, and <code>scim_idp</code> as the secret reference.`,
      `Start with a <b>search</b> before a create: a filter such as <code>userName eq "arno"</code> proves the token and the base URL without writing anything.`,
    ],
    trap: `A search the token is not entitled for answers "no resources" rather than failing. An empty result is therefore not proof that the account does not exist.`,
  },

  ldap: {
    anchor: "runbook-ldap", title: "LDAP directory",
    checked: "2026-09",
    needs: "No Worker record: the server URL and the bind DN live in this task, and the bind password is a vault reference.",
    steps: [
      `Create a <b>service account in the directory</b> — never a personal one — and delegate exactly the rights on exactly the subtrees the processes touch.`,
      `Store its password alone in the vault: ${VAULT}, e.g. <code>ldap_bind</code>.`,
      `In this task: <code>ldaps://dc.example.com:636</code>, the bind DN, and <code>ldap_bind</code> as the bind password reference. A plain <code>ldap://</code> connection needs STARTTLS switched on.`,
      `Targeting Active Directory? Use the <b>Active Directory</b> type instead — it knows the quirks (password encoding, <code>userAccountControl</code>) this generic type leaves to you.`,
    ],
    trap: `A search without permission answers "nothing found" instead of failing, which is why a gateway's default branch must always be the harmless one.`,
  },

  soap: {
    anchor: "runbook-soap", title: "SOAP / web services",
    checked: "2026-09",
    needs: "Nothing to configure on the server: the endpoint and the request body live in this task, the credential is a vault reference.",
    steps: [
      `Take the endpoint out of the WSDL (<code>soap:address</code>) and the operation name out of its <code>&lt;operation&gt;</code>, e.g. <code>GetUser</code>.`,
      `Store the credential in the vault: ${VAULT}, e.g. <code>ws_password</code>.`,
      `In this task: the endpoint, the operation, the SOAP version (1.1 sends <code>text/xml</code> with a <code>SOAPAction</code> header, 1.2 sends <code>application/soap+xml</code>), and Basic authentication with <code>ws_password</code> as the secret reference.`,
      `The request body is the operation's request element only — atlas writes the envelope around it. Switch on <code>fx</code> to interpolate variables into the XML.`,
    ],
    trap: `A wrong or missing <code>SOAPAction</code> is answered by most stacks with a fault about the <i>body</i>, not about the header. When the body looks right, check the action first.`,
  },

  ad: {
    anchor: "runbook-ad", title: "Active Directory",
    checked: "2026-09",
    needs: `A configured Active Directory Worker in ${WORKERS}: an LDAPS URL plus a vault bundle holding the bind account.`,
    steps: [
      `Create a <b>directory service account</b> — not a personal account, not a domain admin — and delegate account and group rights on exactly the OUs the processes work in. Delegation at OU level is the advantage AD has over Entra ID; use it.`,
      `Store the bind bundle in the vault: ${VAULT}, e.g. <code>ad_prod_bind</code> holding <code>{"bindDN": "cn=svc-atlas,ou=service,dc=example,dc=com", "password": "…"}</code>.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>Active Directory</b>, a <b>name</b> (this task states exactly that name), endpoint <code>ldaps://dc.example.com:636</code>, credential reference <code>ad_prod_bind</code>. Saving is enough — atlas restarts the supervised worker with the new configuration itself.`,
      `Practise without a domain controller: a worker started with <code>ATLAS_AD_MOCK=1</code> serves the same tasks from a directory in its own memory, and <code>ATLAS_AD_MOCK_SEED</code> names an LDIF or DSML file it starts from.`,
    ],
    trap: `<code>ldaps://</code>, not <code>ldap://</code>: a domain controller refuses to set a password over an unencrypted channel, so a plain connection works for everything except the one thing a joiner process needs.`,
  },

  ldif: {
    // Short name in the label, like Google Sheets: "(LDIF/DSML)" is a second line in a
    // 270px panel, and the card behind the link names both formats in its first sentence.
    anchor: "runbook-ldif", title: "Directory file",
    checked: "2026-09",
    needs: "Nothing to configure: no Worker record, no credential, no endpoint. The task reads or writes text that is already a process variable.",
    steps: [
      `Pick the <b>format</b> explicitly — LDIF or DSML. There is deliberately no default: guessing a directory file's format from its bytes is how a malformed file becomes a plausible-looking empty result.`,
      `Reading: put the file's text into a variable first (a form upload, a REST call, a script task) and name it as the source. The entries land in the result variable as <code>{dn, attributes}</code> objects.`,
      `Writing: the source variable holds entries in that same shape — which is exactly what an LDAP search or an AD sync returns, so a directory read can be written straight to a file.`,
    ],
  },

  entra: {
    anchor: "runbook-entra", title: "Microsoft Entra ID",
    checked: "2026-09",
    needs: `A configured Microsoft Entra ID Worker in ${WORKERS}: a vault bundle with tenant, client and a secret, a certificate, or — on Windows — a certificate's thumbprint. No endpoint — Graph's address is the same for everyone.`,
    steps: [
      `Entra admin center &rarr; <b>App registrations</b> &rarr; new registration, <b>dedicated to this worker</b>. Note the <i>tenant id</i> and <i>client id</i>, then <i>Certificates &amp; secrets</i> &rarr; new client secret — its value is shown once. Or upload a certificate's public part under <i>Certificates</i> instead and keep the certificate with its private key for the vault.`,
      `<i>API permissions</i> &rarr; <b>Microsoft Graph</b> &rarr; <b>Application permissions</b>. For the usual joiner/mover/leaver case exactly two suffice: <code>User.ReadWrite.All</code> and <code>Group.ReadWrite.All</code> (or the narrower <code>GroupMember.ReadWrite.All</code> when no groups are created). Read-only processes take the <code>*.Read.All</code> variants. Remove <code>Directory.ReadWrite.All</code> if it is there.`,
      `<b>Grant admin consent.</b> Application permissions do nothing until this is done — and its absence shows up as "it does nothing, without an error".`,
      `Store the bundle in the vault: ${VAULT}, e.g. <code>entra_contoso</code> holding <code>{"tenantId": "…", "clientId": "…", "clientSecret": "…"}</code> — or, with a certificate, <code>"certificate"</code> in place of <code>"clientSecret"</code>: the certificate and its unencrypted private key as one PEM text. On a Windows server, <code>"certificateThumbprint"</code> may name a certificate already in the store instead (the service account's <code>CurrentUser\\My</code>, or <code>LocalMachine\\My</code>); its key signs there and may be non-exportable. Exactly one of the three.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>Microsoft Entra ID</b>, a name, credential reference <code>entra_contoso</code>, no endpoint.`,
    ],
    trap: `Resetting a password or touching a role-bearing account needs more than <code>User.ReadWrite.All</code>: the app must also hold a <b>directory role</b> such as <i>User Administrator</i> — and even then it cannot act on holders of higher-privileged roles.`,
  },

  exchange: {
    anchor: "runbook-exchange", title: "Microsoft Exchange",
    checked: "2026-10",
    needs: `A configured Microsoft Exchange Worker in ${WORKERS} per organization: a vault bundle that says which one and as whom. Exchange Online needs no endpoint; an Exchange Server's endpoint is its Remote PowerShell URI. <i>These steps are written from Microsoft's documentation and have not yet been walked at a tenant.</i>`,
    steps: [
      `<b>Exchange Online:</b> Entra admin center &rarr; <b>App registrations</b> &rarr; new registration, <b>dedicated to this worker</b>. Note the <i>client id</i>; the organization is the tenant's initial domain, <code>contoso.onmicrosoft.com</code>.`,
      `<i>API permissions</i> &rarr; <i>APIs my organization uses</i> &rarr; <b>Office 365 Exchange Online</b> &rarr; <b>Application permissions</b> &rarr; <code>Exchange.ManageAsApp</code>, then <b>Grant admin consent</b>.`,
      `<i>Certificates &amp; secrets</i> &rarr; upload the public half of a certificate. Keep the certificate and its private key together as one PEM text for the bundle; Microsoft documents certificates, not client secrets, for unattended Exchange administration.`,
      `Give the app an Exchange role: Entra &rarr; <b>Roles and administrators</b> &rarr; <i>Exchange Recipient Administrator</i> (recipients and permissions) or <i>Exchange Administrator</i> &rarr; add the app. Without a role every call is refused.`,
      `Store the bundle in the vault: ${VAULT}, e.g. <code>exo_contoso</code> holding <code>{"organization": "contoso.onmicrosoft.com", "clientId": "…", "certificate": "-----BEGIN CERTIFICATE-----…-----BEGIN PRIVATE KEY-----…"}</code>.`,
      `<b>Exchange Server:</b> an account in a role group that covers the tasks (<i>Recipient Management</i>, or <i>Organization Management</i> for everything), and a worker host that reaches <code>http://exch01.contoso.local/PowerShell/</code> — in practice a Windows domain member with PowerShell 7 or Windows PowerShell 5.1, running <code>atlas worker --connector exchange</code> as that account, so Kerberos needs no password. The bundle is then <code>{"target": "onprem"}</code>, or carries <code>username</code> and <code>password</code>.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>Microsoft Exchange</b>, a name, the credential reference, and for a server its Remote PowerShell URI as endpoint.`,
    ],
    trap: `An app with <code>Exchange.ManageAsApp</code> but no Exchange role in Entra — or a role assignment that has not yet taken effect, which can take a while — is refused on every call. The permission lets the app ask; the role is what it may do.`,
  },

  teamsphone: {
    anchor: "runbook-teamsphone", title: "Microsoft Teams Phone",
    checked: "2026-10",
    needs: `A configured Microsoft Teams Phone Worker in ${WORKERS}: a vault bundle with tenant, client and the app's certificate. No endpoint. The worker host needs PowerShell 7 and the MicrosoftTeams module.`,
    steps: [
      `On the worker host: PowerShell 7 and <code>Install-Module MicrosoftTeams -Scope AllUsers</code>. If <code>pwsh</code> is not on the PATH, <code>ATLAS_TEAMSPHONE_PWSH</code> names it. The host's own proxy is used; atlas sets none.`,
      `Entra admin center &rarr; <b>App registrations</b> &rarr; new registration with a <b>certificate</b> (the module signs apps in with a certificate only). Give the service principal the role <i>Teams Administrator</i> and the Graph application permission <code>User.ReadWrite.All</code>, then <b>grant admin consent</b>.`,
      `Store the bundle in the vault: ${VAULT}, e.g. <code>teams_contoso</code> holding <code>{"tenantId": "…", "clientId": "…", "certificate": "…"}</code> — the certificate and its unencrypted private key as one PEM text — or <code>"certificateThumbprint"</code> in its place for a certificate already in the worker account's store. Never both.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>Microsoft Teams Phone</b>, a name, credential reference <code>teams_contoso</code>, no endpoint.`,
      `For phone numbers: a person needs a Teams Phone licence, a resource account the <i>Microsoft Teams Phone Resource Account</i> licence, and both a usage location. Calling Plan and Operator Connect numbers must already be the tenant's; a Direct Routing number is your own carrier's.`,
      `Practise without a tenant: <code>ATLAS_TEAMSPHONE_MOCK=1</code> serves the same tasks from a tenant in the worker's memory, <code>ATLAS_TEAMSPHONE_MOCK_USERS</code> names the people Resolve users finds, and <code>ATLAS_TEAMSPHONE_MOCK_NUMBERS</code> the tenant's Calling Plan and Operator Connect numbers.`,
    ],
    trap: `Teams shows a new resource account only after a synchronization. A voice app waits up to two minutes for its account and is then refused before anything is created — give the model a pause between creating the accounts and the voice apps (waiting for licences is one), rather than reading the delay as a fault.`,
    reference: { anchor: "ref-teamsphone", title: "Teams Phone operations and error codes" },
  },

  // The three database entries quote the connection string exactly as the Console's own
  // field placeholder does, `PASSWORT` included (SQL_DSN_EXAMPLES in workerdialog.js):
  // an operator reads the example here and types into the field there, and two spellings
  // of the same example is one more thing to wonder about.
  mssql: {
    anchor: "runbook-mssql", title: "Microsoft SQL Server",
    checked: "2026-09",
    needs: `A configured Microsoft SQL Server Worker in ${WORKERS}. Its whole configuration is one connection string, which is the credential.`,
    steps: [
      `Create a <b>technical database user</b> with rights on exactly the tables, views and procedures the processes need. No <code>db_owner</code>. Granting on views and stored procedures rather than tables makes the interface a contract instead of your schema.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>Microsoft SQL Server</b>, a name, and the connection string <code>sqlserver://atlas:PASSWORT@sql.example.com:1433?database=hr</code>. Atlas seals it into the vault and keeps only a reference — the Console can never show it back.`,
      `Write the statement literally and pass every value as a <b>bound parameter</b> (<code>@p1</code>, or by name — SQL Server is the one of the three drivers that binds by name, so an object-shaped parameters variable is accepted here).`,
      `Practise without a database: switch the worker to mock mode and the same task answers from seeded rows in memory; what a run asked for is listed under <i>Operations &rsaquo; Diagnostics &rsaquo; Mock database</i>.`,
    ],
    trap: `The <b>statement</b> field has no <code>fx</code> toggle, on purpose: a statement assembled from process data would be an injection that needs no quoting bug to work.`,
  },

  mariadb: {
    anchor: "runbook-mariadb", title: "MariaDB",
    checked: "2026-09",
    needs: `A configured MariaDB Worker in ${WORKERS}. Its whole configuration is one connection string, which is the credential.`,
    steps: [
      `Create a <b>technical database user</b> with rights on exactly the tables and views the processes need. No <code>SUPER</code>, no <code>GRANT ALL</code>.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>MariaDB</b>, a name, and the connection string <code>atlas:PASSWORT@tcp(mariadb.example.com:3306)/hr?parseTime=true</code> — the MySQL driver's own form, which is not a URL.`,
      `Write the statement literally and pass every value as a <b>bound parameter</b>, marked <code>?</code> and bound in order. An object as the parameters variable is refused rather than put into an order nobody wrote.`,
      `Practise without a database: switch the worker to mock mode and the same task answers from seeded rows in memory (<i>Operations &rsaquo; Diagnostics &rsaquo; Mock database</i>).`,
    ],
    trap: `The <b>statement</b> field has no <code>fx</code> toggle, on purpose: a statement assembled from process data would be an injection that needs no quoting bug to work.`,
  },

  postgres: {
    anchor: "runbook-postgres", title: "PostgreSQL",
    checked: "2026-09",
    needs: `A configured PostgreSQL Worker in ${WORKERS}. Its whole configuration is one connection string, which is the credential.`,
    steps: [
      `Create a <b>technical database role</b> with rights on exactly the tables and views the processes need — not the owner role, and no <code>SUPERUSER</code>.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>PostgreSQL</b>, a name, and the connection string <code>postgres://atlas:PASSWORT@db.example.com:5432/hr?sslmode=require</code>. Keep <code>sslmode=require</code> unless the database is on this very host.`,
      `Write the statement literally and pass every value as a <b>bound parameter</b>, marked <code>$1</code>, <code>$2</code> and bound in order.`,
      `Practise without a database: switch the worker to mock mode and the same task answers from seeded rows in memory (<i>Operations &rsaquo; Diagnostics &rsaquo; Mock database</i>).`,
    ],
    trap: `The <b>statement</b> field has no <code>fx</code> toggle, on purpose: a statement assembled from process data would be an injection that needs no quoting bug to work.`,
  },

  clio: {
    anchor: "runbook-clio", title: "clio event store",
    checked: "2026-09",
    needs: `A configured clio Worker in ${WORKERS}: an endpoint, and a token reference only where the store requires one.`,
    steps: [
      `Have a clio event store reachable from this server, e.g. <code>https://clio.example.com</code>.`,
      `Where it is not open inside your own network, store its token in the vault: ${VAULT}, e.g. <code>clio_token</code>.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>clio</b>, a name (this task states exactly that name), the endpoint, and the token reference where there is one.`,
      `Prove it with a <b>write</b> of one event on a throwaway subject before wiring a query into a gateway — a query against a subject nothing has written answers "no state", which is not the same as "not reachable".`,
    ],
  },

  // temis is not a service-task kind — it is the business rule task's other decision
  // binding — but it is a Worker an operator configures in the same Console dialog as
  // the rest, so it carries the same entry. The panel that offers it renders this too.
  temis: {
    anchor: "runbook-temis", title: "temis",
    checked: "2026-09",
    needs: `A configured temis Worker in ${WORKERS}: an endpoint, and a token reference only where the service requires one.`,
    steps: [
      `Check whether you need it at all: a DMN decision deployed into atlas is evaluated by the built-in engine and needs no worker. temis is for keeping decisions <b>centrally</b>, outside this installation.`,
      `Where the service requires a token, store it in the vault: ${VAULT}, e.g. <code>temis_token</code>.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>temis</b>, a name, the endpoint <code>https://temis.example.com</code>, and the token reference where there is one.`,
      `On the business rule task, choose <b>External (temis worker)</b> and name that worker.`,
    ],
  },

  mail: {
    anchor: "runbook-mail", title: "Mail",
    checked: "2026-09",
    needs: `A configured mail Worker in ${WORKERS} — and one of its four transports needs no mail server at all. Reading the mailbox (a watch, or a task that lists, files or answers mail) needs read access as well.`,
    steps: [
      `<b>Start with Preview.</b> ${WORKERS} &rarr; <b>New worker</b>: type <b>Mail</b>, provider <b>Preview</b>, a name, a sender address. It frames the message exactly as it would be sent and puts it in <i>Operations &rsaquo; Diagnostics &rsaquo; Outbox</i> instead of delivering it — a mail task can be built and demonstrated with no credentials.`,
      `<b>SMTP:</b> provider <b>SMTP</b>, endpoint <code>smtp.example.com:587</code> (587 is assumed without a port); the sender is the login, and the token reference names its password in the vault. An internal relay that admits this server by its address (often <code>relay.example.com:25</code>) needs no login: leave the token reference empty and the worker sends without one. The only route that also works with a mail server in your own basement.`,
      `<b>Gmail:</b> provider <b>Gmail API</b>, no endpoint, and a vault bundle — a service account <code>{"method": "serviceAccount", "clientEmail": "…", "privateKey": "-----BEGIN PRIVATE KEY-----\\n…", "subject": "sender@your-domain"}</code> (Workspace, domain-wide delegation for the <code>gmail.send</code> scope), or <code>{"method": "refreshToken", "clientId": …, "clientSecret": …, "refreshToken": …}</code> for a single mailbox.`,
      `<b>Microsoft Graph:</b> provider <b>Microsoft Graph</b>, no endpoint, vault bundle <code>{"method": "clientCredentials", "tenantId": "…", "clientId": "…", "clientSecret": "…"}</code> from an app registration with the <code>Mail.Send</code> application permission and admin consent. This is the route when your organisation has switched off SMTP basic authentication.`,
      `Going live is a change of <b>worker</b>, not of model: the task keeps naming the same worker name.`,
      `<b>To read the mailbox</b> — an inbound watch, or a task that lists, reads, files, marks, deletes or answers mail: <b>SMTP</b> names an <b>IMAP endpoint</b> (<code>imaps://imap.example.com:993</code>; plaintext is refused) and logs in as the sender with the same password. <b>Microsoft Graph</b> needs the application permission <code>Mail.Read</code> (<code>Mail.ReadWrite</code> to change the mailbox) — and because an application permission reaches every mailbox in the tenant, an Exchange Online <b>RBAC for Applications</b> assignment that confines the app to this one. <b>Gmail</b>'s delegation needs <code>gmail.readonly</code>, and <code>gmail.modify</code> to change the mailbox. <i>This step is written from the providers' documentation and has not yet been walked at a tenant.</i>`,
      `<b>Decide who may use it.</b> A task that reads or changes a mailbox deploys only for people the Worker is shared with — viewer to read, editor to change (Workers &rsaquo; ⋯ &rsaquo; Share). Keep a personal mailbox in a Worker of its own, apart from a shared sender, ideally with its own read-only credential.`,
      `<b>Hand attachments on</b> — forward a mail, read an attachment into a variable, export one into an S3 Worker: name a <b>malware scanner</b> on the worker (a ClamAV daemon, <code>clamd://clamav.internal:3310</code>) so every document is scanned before it leaves; without one only the built-in inspection runs. Forwarding and exporting deploy only for editors of the mail Worker, and an export also for editors of the S3 Worker. <b>Microsoft Graph</b> forwards with <code>Mail.Send</code> and reads attachments with <code>Mail.Read</code>; <b>Gmail</b> forwards with <code>gmail.send</code> and reads with <code>gmail.readonly</code>. <i>The provider half of this step is written from the providers' documentation and has not yet been walked at a tenant.</i>`,
      `<b>Receive mail:</b> on the worker under <b>Events…</b> — a folder (empty is the inbox), a message name, and who may start a process: <b>Allowed senders</b> and <b>Require a DMARC pass</b>. The watch never changes the mailbox; the process receives the mail as one variable, <code>mail</code> (<code>mail.from</code>, <code>mail.subject</code>, <code>mail.auth.dmarc</code>, …), and files or marks it with a task addressing <code>mail.messageId</code>. A watch needs a mailbox: on an SMTP Worker without an IMAP endpoint, or a Preview Worker, it is refused. One that cannot read — a refused login, a missing permission — shows <b>not reading</b> in its row, with the reason, from its first poll on.`,
    ],
    trap: `Send the first real batch to yourself, and check that the sender address is allowed to send (SPF/DKIM). An automation sending two hundred wrong mails is faster than any correction. And a watch publishes mail from <b>anyone</b> who can write to the address: a sender address is a claim anybody can make, so restrict the watch to allowed senders <i>and</i> require a DMARC pass.`,
  },

  csv: {
    anchor: "runbook-csv", title: "Text file",
    checked: "2026-09",
    needs: "Nothing to configure: no Worker record, no credential, no endpoint. The layout is in the task, the file's text is a process variable.",
    steps: [
      `Reading: put the file's text into a variable first (a form upload, a REST call), name it as the source, and the rows land in the result variable as a JSON array of objects.`,
      `Say what the layout is: delimiter and header row for a delimited file, <code>name:width</code> pairs for a fixed-width one, field names for attribute-value pairs. A delimited file with a header row can derive its columns itself.`,
      `Writing: the source variable holds the rows as an array of objects and the rendered file lands in the result variable as text — pass it to a mail attachment, a REST call or a Directory file task.`,
    ],
    trap: `A fixed-width value wider than its column is cut on write, because the format has no way to hold it. Size the columns for the widest value you actually expect.`,
  },

  sharepoint: {
    anchor: "runbook-sharepoint", title: "SharePoint",
    checked: "2026-09",
    needs: `A configured SharePoint Worker in ${WORKERS}: a vault bundle for Microsoft Graph with tenant, client and a secret, a certificate, or — on Windows — a certificate's thumbprint. No endpoint — Graph's address is the same for everyone. Or the <b>Mockup</b> provider, which needs none of it.`,
    steps: [
      `<b>Start with the mockup.</b> ${WORKERS} &rarr; <b>New worker</b>: type <b>Microsoft SharePoint</b>, provider <b>Mockup</b>, a name. Every operation then runs against a site atlas keeps in its own memory — a list or library exists from the first task that writes to it — and <i>Operations &rsaquo; Diagnostics &rsaquo; Mockups</i> shows what a run wrote. A SharePoint process can be built and demonstrated with no tenant; going live is switching the same Worker to <b>Microsoft Graph</b>.`,
      `The groundwork is Entra ID's: an <b>app registration</b>, a client secret or — recommended for an app that runs unattended — a certificate uploaded under <i>Certificates &amp; secrets</i>, and admin consent. Note tenant id and client id.`,
      `Grant the <b>site-specific</b> permission <code>Sites.Selected</code> and then grant this app access to the individual sites it works on. <code>Sites.ReadWrite.All</code> works too and is the grant an audit writes up.`,
      `Store the bundle in the vault: ${VAULT}, e.g. <code>sharepoint_auth</code> holding <code>{"method": "clientCredentials", "tenantId": "…", "clientId": "…", "clientSecret": "…"}</code> — or <code>"certificate"</code>, the certificate and its unencrypted private key as one PEM text, in place of <code>clientSecret</code>. On a Windows server, <code>"certificateThumbprint"</code> may name a certificate already in the store instead (the service account's <code>CurrentUser\\My</code>, or <code>LocalMachine\\My</code>), as for Entra ID; its key signs there and may be non-exportable. One of the three.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>Microsoft SharePoint</b>, a name, credential reference <code>sharepoint_auth</code>, no endpoint.`,
      `In the task, name the <b>Site</b> as the browser shows it (<code>https://contoso.sharepoint.com/sites/hr</code>), as <code>contoso.sharepoint.com:/sites/hr</code>, or by Graph's site id; a list or library by its title, a column by its internal name.`,
    ],
    trap: `A <code>403</code> here usually means not "a permission is missing" but "this app has no access to <i>this</i> site" — which is why the same app works on one site and not on the next.`,
  },

  remedy: {
    anchor: "runbook-remedy", title: "BMC Remedy",
    checked: "2026-09",
    needs: `A configured BMC Remedy Worker in ${WORKERS}: the AR System endpoint plus a vault bundle with a technical user.`,
    steps: [
      `Have your ITSM team create a <b>technical AR System user</b> entitled on exactly the forms the processes work on.`,
      `Store its credential in the vault: ${VAULT}, e.g. <code>remedy_creds</code> holding <code>{"username": "…", "password": "…"}</code>.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>BMC Remedy</b>, a name, endpoint <code>https://helix.example.com:8008</code>, credential reference <code>remedy_creds</code>.`,
      `In the task, name the form (<code>HPD:IncidentInterface_Create</code>) and the field values by their Remedy field names.`,
    ],
    trap: `Which fields a ticket is <i>required</i> to carry is installation-specific. Clarify it with the ITSM team beforehand — it is the most common cause of a rejected call.`,
  },

  jira: {
    anchor: "runbook-jira", title: "Jira",
    checked: "2026-09",
    needs: `A configured Jira Worker in ${WORKERS}: the site URL plus a vault bundle with the API token.`,
    steps: [
      `<b>Create an API token:</b> Atlassian account &rarr; <i>Security</i> &rarr; <i>API tokens</i> &rarr; create token. For <b>Jira Data Center</b>, a personal access token instead. Use a technical account, not your own.`,
      `Store it in the vault: ${VAULT}, e.g. <code>jira_acme</code> holding <code>{"email": "you@example.com", "apiToken": "ATATT…"}</code> — or <code>{"token": "…"}</code> for Data Center. The worker tells the two shapes apart by their fields.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>Atlassian Jira</b>, a <b>name</b> (this task states exactly that name), endpoint <code>https://&lt;your-site&gt;.atlassian.net</code>, credential reference <code>jira_acme</code>.`,
      `<b>Entitle the account in the project:</b> create, comment and transition issues. Assigning additionally needs the <b>global</b> permission <i>Browse users and groups</i>, which is what <code>search-users</code> reads.`,
      `Prove it with a single <i>Create issue</i> against a scratch project before wiring anything else.`,
    ],
    trap: `Without <i>Browse users and groups</i>, a user search finds nobody — <b>with no error</b>. An assignment then quietly targets nothing.`,
  },

  googlesheets: {
    // The link label is the short name, not the handbook card's full title ("Google
    // Sheets & Drive"): at 270px that one wrapped onto a second line, and the card it
    // opens says the rest.
    anchor: "runbook-googlesheets", title: "Google Sheets",
    checked: "2026-09",
    needs: `A configured Google Sheets Worker in ${WORKERS}: a vault bundle with a service account key. No endpoint — Google's addresses are the same for everyone.`,
    steps: [
      `<b>Google Cloud Console</b> &rarr; pick a project &rarr; <i>APIs &amp; Services</i> &rarr; enable the <b>Google Sheets API</b> <i>and</i> the <b>Google Drive API</b>. Drive is not optional: creating a spreadsheet, filing it in a folder and deleting it are Drive operations.`,
      `<i>IAM &amp; Admin</i> &rarr; <b>Service accounts</b> &rarr; create one. <b>Skip</b> the "Permissions" and "Principals with access" steps — a project role does nothing for Sheets. Then <i>Keys</i> &rarr; <i>Add key</i> &rarr; <b>JSON</b>; the file downloads once and Google keeps no copy of the private half.`,
      `Store the bundle in the vault: ${VAULT}, e.g. <code>google_sheets_auth</code> holding <code>{"method": "serviceAccount", "clientEmail": "…@….iam.gserviceaccount.com", "privateKey": "-----BEGIN PRIVATE KEY-----\\n…"}</code>. <code>clientEmail</code> and <code>privateKey</code> are <code>client_email</code> and <code>private_key</code> from the key file, camel-cased; the <code>\\n</code> stay <b>two characters</b>.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>Google Sheets</b>, a name, credential reference <code>google_sheets_auth</code>, no endpoint.`,
      `<b>Share the spreadsheet with the service account.</b> It has an email address of its own and owns nothing: share every spreadsheet or folder with that address as an <b>editor</b>, exactly as you would with a colleague.`,
    ],
    trap: `The step everyone forgets is the last one: without the share, the worker answers <code>403</code> on a document you have open in front of you.`,
  },

  googlecontacts: {
    anchor: "runbook-googlecontacts", title: "Google Contacts",
    checked: "2026-10",
    needs: `A configured Google Contacts Worker in ${WORKERS} per address book: a vault bundle with a service account and the Workspace user it acts as, or a refresh token. No endpoint. <i>These steps are written from Google's documentation and have not yet been walked in a Google account.</i>`,
    steps: [
      `<b>Google Cloud Console</b> &rarr; pick a project &rarr; <i>APIs &amp; Services</i> &rarr; enable the <b>People API</b>.`,
      `<i>IAM &amp; Admin</i> &rarr; <b>Service accounts</b> &rarr; create one, then <i>Keys</i> &rarr; <i>Add key</i> &rarr; <b>JSON</b>. Note the account's <b>client ID</b> (the long number on its details page).`,
      `<b>Google Workspace Admin console</b> &rarr; <i>Security</i> &rarr; <i>Access and data control</i> &rarr; <i>API controls</i> &rarr; <b>Manage domain-wide delegation</b> &rarr; add that client ID with the scope <code>https://www.googleapis.com/auth/contacts</code> — and <code>https://www.googleapis.com/auth/directory.readonly</code> beside it if a process searches the directory.`,
      `Store the bundle in the vault: ${VAULT}, e.g. <code>google_contacts_verkauf</code> holding <code>{"method": "serviceAccount", "clientEmail": "…@….iam.gserviceaccount.com", "privateKey": "-----BEGIN PRIVATE KEY-----\\n…", "subject": "verkauf@firma.ch"}</code>. The <b>subject</b> is the person whose address book the Worker uses; for the directory, add <code>"scope"</code> naming both scopes, separated by a space.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>Google Contacts</b>, a name, credential reference <code>google_contacts_verkauf</code>, no endpoint. One Worker per address book.`,
      `<b>Without a Workspace</b> (a gmail.com account): an OAuth client of type <i>Desktop app</i>, one consent with the contacts scope, and the refresh token it yields — <code>{"method": "refreshToken", "clientId": "…", "clientSecret": "…", "refreshToken": "…"}</code>.`,
    ],
    trap: `Delegation grants exactly the scopes listed. A scope in the bundle that is not delegated fails the token request — for every operation, not only the one that needed it. And an OAuth client still in <i>Testing</i> expires its refresh token after 7 days.`,
  },

  carddav: {
    anchor: "runbook-carddav", title: "CardDAV contacts",
    checked: "2026-10",
    needs: `A configured CardDAV Worker in ${WORKERS} per address book: its endpoint, and a vault bundle with the account's username and password. <i>Walked against Radicale and Xandikos; the provider steps are written from the providers' documentation and have not yet been walked in an iCloud, Nextcloud or mail provider account.</i>`,
    steps: [
      `At the provider, create an <b>app-specific password</b> for the account where it issues one — <b>iCloud</b>: <i>account.apple.com</i> &rarr; <i>Sign-In and Security</i> &rarr; <b>App-Specific Passwords</b> (required); <b>Nextcloud</b>: <i>Settings</i> &rarr; <i>Security</i> &rarr; <b>Create new app password</b> (required with two-factor sign-in).`,
      `Note the <b>endpoint</b>: <code>https://contacts.icloud.com</code> for iCloud, <code>https://&lt;server&gt;/remote.php/dav</code> for Nextcloud and ownCloud, the server's address for Radicale (<code>…/dav.php</code> for Baïkal), otherwise the CardDAV address the provider's help names. The address book is found from it; when the account has several, the first task names them, and you set the endpoint to the one this Worker uses.`,
      `Store the bundle in the vault: ${VAULT}, e.g. <code>carddav_verkauf</code> holding <code>{"username": "verkauf@firma.ch", "password": "…", "groups": "categories"}</code> — <code>"groups": "group-vcards"</code> for <b>iCloud and Fastmail</b>, whose groups are cards of their own.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>CardDAV contacts</b>, a name, the endpoint, credential reference <code>carddav_verkauf</code>. One Worker per address book.`,
    ],
    trap: `The account's own password is refused where the provider requires an app-specific one — iCloud always, Nextcloud with two-factor sign-in — and the task fails with <code>HTTP 401</code>. A group form that does not match the server's is silent instead: an iCloud address book in the <code>categories</code> form keeps every group in a field Apple Contacts never shows.`,
  },

  wikiwikibangbang: {
    anchor: "runbook-wikiwikibangbang", title: "wikiwikibangbang",
    checked: "2026-10",
    needs: `A configured wikiwikibangbang Worker in ${WORKERS} per wiki: the wiki's base URL, and a vault bundle with the API token of a technical account of its own — an editor in the spaces processes write to, with no system role. <i>Walked against wikiwikibangbang run locally at commit 928fec0.</i>`,
    steps: [
      `In the wiki, an administrator creates a <b>technical account</b> for Atlas, e.g. <code>atlas-bot</code>, and grants it the role <b>editor</b> in each space processes write to — and nothing else: an account with the system role <b>admin</b> or <b>erasure</b> is refused. It is not the read-only account the catalogue reads the wiki with.`,
      `Signed in as that account, create an <b>API token</b> of scope <b>full</b> under <i>Account</i> &rarr; <b>API tokens</b> (at most 366 days), and note its expiry: the token stops working then, and the Worker with it. A Worker that only reads may use a token of scope <b>read</b>.`,
      `Store the bundle in the vault: ${VAULT}, e.g. <code>wiki_bot</code> holding <code>{"token": "wwbb_…"}</code>.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>wikiwikibangbang</b>, a name, the wiki's base URL as endpoint (<code>https://wiki.example.ch</code>), credential reference <code>wiki_bot</code>. One Worker per wiki and account.`,
      `<b>To wait for a task to be done:</b> create a second token, of scope <b>read</b>, for the same account or one that is a viewer of the space, store it as e.g. <code>wiki_leser</code>, and add a second wikiwikibangbang Worker with it — the engine reads the wiki's change feed itself, and a token that can write never reaches it. On that Worker under <b>Events…</b>: the space, a message name (<code>aufgabe.erledigt</code>) and the correlation key <code>= itemId</code>. In the model, the task that opens the item keeps its result in <code>aufgabe</code> and is followed by a message catch event of that name correlated on <code>= aufgabe.id</code>; the instance receives the finished task as <code>wikiEvent</code>.`,
    ],
    trap: `Everything the Worker writes is written as its account: the wiki's history and comments name <i>atlas-bot</i>, not the person behind the process. And a user property accepts any text — the wiki does not check that <code>assignee</code> names an account, so a misspelt username assigns the task to nobody, silently. A finished task is told only to an instance that is waiting for it at that moment: model the wait right after the task that opens it.`,
  },

  discord: {
    anchor: "runbook-discord", title: "Discord",
    checked: "2026-09",
    needs: `A configured Discord Worker in ${WORKERS}: a vault bundle with the bot token. No endpoint — Discord's API base is the same for everyone.`,
    steps: [
      `<b>Discord Developer Portal</b> &rarr; <i>New Application</i> &rarr; <i>Bot</i> &rarr; <b>Reset Token</b>, and copy the token. It is shown once.`,
      `Store it in the vault: ${VAULT}, e.g. <code>discord_team</code> holding <code>{"botToken": "…"}</code> — the token alone; atlas composes the <code>Bot </code> scheme itself.`,
      `<b>Invite the bot to your server</b> (Developer Portal &rarr; <i>OAuth2</i> &rarr; URL generator, scope <code>bot</code>) and make sure it holds <b>View Channel</b> and <b>Send Messages</b> in every channel a process writes to.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>Discord</b>, a name, credential reference <code>discord_team</code>, no endpoint.`,
      `Get the <b>channel id</b>: in Discord enable <i>Developer Mode</i> (User settings &rarr; Advanced), then right-click the channel &rarr; <b>Copy Channel ID</b>. A thread is itself a channel, so posting into one is a Send message naming the thread's id.`,
    ],
    trap: `A missing channel grant comes back as code <code>50001</code>, <i>Missing Access</i> — not as a bad token. Check the channel's permissions before the token.`,
  },

  cloudevents: {
    anchor: "runbook-cloudevents", title: "CloudEvents endpoint",
    checked: "2026-10",
    needs: `A receiving system that accepts CloudEvents batches over https, and a configured CloudEvents endpoint Worker in ${WORKERS}, subscribed to the event feed.`,
    steps: [
      `On the receiving side, offer an <b>https</b> address that accepts <code>POST</code> with <code>Content-Type: application/cloudevents-batch+json</code> — a JSON array of CloudEvents — and answers <b>2xx</b> once it has stored them.`,
      `If it wants a credential, store its token in the vault: ${VAULT}, e.g. <code>billing_feed_token</code>, holding the token itself. atlas sends it as <code>Authorization: Bearer</code>.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>CloudEvents endpoint</b>, a name, the address, and the token reference.`,
      `In the worker's menu, <b>Feed…</b>: subscribe it to the feed, optionally narrowed to some catalogues, starting from the oldest event the feed holds or from now.`,
    ],
    trap: `Delivery is at least once: a batch accepted just before a restart can arrive again. Deduplicate by each event's <code>id</code>. A receiver that refuses a batch holds the subscription — nothing is skipped — so a permanent refusal shows as a hold that does not lift.`,
  },

  s3: {
    anchor: "runbook-s3", title: "S3 object storage",
    checked: "2026-09",
    needs: `A configured S3 Worker in ${WORKERS}: a vault bundle with an access key and the region it signs for. The endpoint stays empty for AWS and names the host for anything else.`,
    steps: [
      `Create the <b>bucket</b> at your store first — AWS S3, MinIO, Ceph, Garage, Cloudflare R2 or whatever the installation runs — and note the <b>region</b> it is in. Self-hosted stores that have no regions commonly answer to <code>us-east-1</code>.`,
      `Issue an <b>access key</b> scoped to that bucket: <b>AWS console &rsaquo; IAM &rsaquo; Users &rsaquo; Security credentials &rsaquo; Create access key</b>, or <b>MinIO console &rsaquo; Access Keys &rsaquo; Create</b>. Give it only the actions the processes need — <code>s3:GetObject</code>, <code>s3:PutObject</code>, <code>s3:ListBucket</code>, and <code>s3:DeleteObject</code> where a process removes anything.`,
      `Store it in the vault: ${VAULT}, e.g. <code>s3_archiv</code> holding <code>{"accessKeyId": "…", "secretAccessKey": "…", "region": "eu-central-1"}</code>. Add <code>"sessionToken"</code> as well if the key came from STS.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>S3 object storage</b>, a name, credential reference <code>s3_archiv</code>. Leave the <b>endpoint empty for AWS</b>; for any other store enter its base URL (<code>https://minio.example:9000</code>), which is also what tells atlas to address buckets path-style.`,
      `Try it with a <b>Check object</b> task on a key you know: <code>=datei.exists</code> answers without needing the object to be readable in full, so a wrong permission shows up as an error rather than as an empty result.`,
    ],
    trap: `A document larger than a process variable's budget — <b>1 MiB</b> unless this installation raised it — cannot be read into one. Use <b>Link to download</b>, which hands out a signed URL and lets the browser fetch the bytes straight from the store. Keep the link's lifetime short: it opens that object for anyone who has it, and it is stored in the instance's variables like any other value.`,
  },

  aitask: {
    anchor: "runbook-ai", title: "AI worker",
    checked: "2026-09",
    needs: `A configured AI Worker in ${WORKERS}: a wire format, an API key reference, and the model it asks by default.`,
    steps: [
      `Get an <b>API key</b> from the provider — Anthropic Console, OpenAI platform, or whatever your gateway issues — for a key that is this server's, not a person's.`,
      `Store it in the vault: ${VAULT}, e.g. <code>anthropic_api_key</code>, holding the key itself.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>AI agent model</b>, a name, the <b>provider</b> (<i>Messages</i> is Anthropic's format, <i>Chat Completions</i> is OpenAI's and anything calling itself OpenAI-compatible), the API key reference, and the <b>default model</b>. Leave the endpoint empty unless you go through a gateway, a proxy or a self-hosted deployment.`,
      `In the task, leave <b>Model</b> empty to ask the worker's default, or name one per task — a cheap model for a classification and a strong one for advice, through the same worker and the same key.`,
    ],
    trap: `A model call takes seconds to minutes and can hang, so atlas never runs it in the engine: it supervises a worker for this type and picks up a changed model as soon as you save, with no restart.`,
  },

  webscrape: {
    anchor: "runbook-webscrape", title: "Web scraping",
    checked: "2026-09",
    needs: "Nothing to configure: no Worker record and no credential. The URL and the selectors live in this task.",
    steps: [
      `For a page, name the URL and the fields as <code>name</code> + <b>CSS selector</b> (plus an attribute where you want <code>href</code> rather than the text).`,
      `For a feed, choose format <b>RSS</b> or <b>Atom</b> — the entries come back as a structured list instead of selector matches.`,
      `Atlas offloads this type by default and starts the worker instance itself, so a slow or hanging page cannot stall the engine.`,
    ],
    trap: `A selector is a contract with somebody else's markup. Expect it to break without notice, and give the task an error path rather than assuming a result.`,
  },

  userconnector: {
    anchor: "runbook-userprov", title: "User provisioning",
    checked: "2026-09",
    needs: "No Worker record and no credential — it acts on this atlas's own login store, which is why it is fenced instead of configured.",
    steps: [
      `It runs <b>only for processes in the protected system project</b>. A copy of the same task in an ordinary project is refused at deploy — that fence is the whole security model of this type.`,
      `It is on by default. An installation that does not want atlas logins created by a process starts the server with <code>--user-provisioning=false</code>; the tasks then park instead of acting.`,
      `Put a <b>human approval step</b> in front of it and let an admin set the roles and the initial password there. Never let a requester choose roles on a public start form.`,
      `Pass the password as a FEEL reference to a variable (<code>=initialpasswort</code>, at least 8 characters), so no password is written into the model.`,
    ],
    trap: `This is the one Worker Type that reopens the boundary between "a process" and "who may use atlas". Treat every change to such a model as a change to access control.`,
  },

  mockup: {
    anchor: "runbook-mockup", title: "Mockup (simulation)",
    checked: "2026-09",
    needs: "Nothing to configure, and nothing outside atlas: the engine acts the foreign system itself.",
    steps: [
      `Give it a duration (or a minimum and a maximum) so the diagram behaves like the system it stands in for.`,
      `Write the answer as a FEEL <b>result expression</b> over the instance's variables and name a result variable — that is the input&rarr;output script of the system that does not exist yet.`,
      `Exercise the unhappy path: a <b>failure rate</b> between 0 and 1, plus either a BPMN error code (caught by an error boundary event) or an incident message.`,
      `Replace it with the real Worker Type later; the surrounding model, its gateways and its error handling stay as they are.`,
    ],
  },
};

// WORKER_KIND_TO_DOC maps a *configured Worker's* kind — the server's word, as the
// Console and the worker store use it — onto the catalog id whose setup it is. Almost
// all of them are the same string; the exceptions are the ones where the catalog is
// named after the task and the record after the capability.
const WORKER_KIND_TO_DOC = { agent: "aitask" };

// docForWorkerKind answers the Console's question ("how is a worker of this kind set
// up?") with the catalog's answer, so the dialog and the panel cannot drift apart.
export function docForWorkerKind(kind) {
  const id = WORKER_KIND_TO_DOC[kind] || kind;
  return WORKER_TYPE_DOCS[id] || null;
}

// handbookHref is the link to one Worker Type's handbook card, on this server.
export function handbookHref(doc) {
  return doc && doc.anchor ? `${HANDBOOK_URL}#${doc.anchor}` : HANDBOOK_URL;
}

// workerTypeDocHTML renders the setup block for one catalog kind: the always-visible
// line (what this needs, and the handbook link), then the steps folded away.
//
// The steps are collapsed because the panel is 270px wide and an author who has already
// configured this type wants the fields, not the recipe. The `needs` line and the link
// are not collapsed, because they are the two things someone who has *not* configured it
// has to see without knowing to look.
export function workerTypeDocHTML(id) {
  return renderDoc(WORKER_TYPE_DOCS[id]);
}

// workerKindDocHTML renders the same block for a *configured Worker's* kind, which is
// what the Console dialog has in its hand. Same text either way: an operator filling in
// the credential reference and an author picking the type are asking one question.
export function workerKindDocHTML(kind) {
  return renderDoc(docForWorkerKind(kind));
}

// monthName spells a YYYY-MM date out ("September 2026"). Written out rather than
// printed as 09/2026, which reads as a day and a month to half the world.
const MONTHS = ["January", "February", "March", "April", "May", "June",
  "July", "August", "September", "October", "November", "December"];

function monthName(yyyymm) {
  const [year, month] = String(yyyymm).split("-");
  const name = MONTHS[Number(month) - 1];
  return name ? `${name} ${year}` : String(yyyymm);
}

// renderDoc is the one markup for both surfaces; a Worker Type nobody documented
// renders nothing rather than an empty box (the Go guard makes sure there is none).
function renderDoc(doc) {
  if (!doc) return "";
  const steps = (doc.steps || []).map((s) => `<li>${s}</li>`).join("");
  const trap = doc.trap ? `<p class="wtdoc-trap"><b>Watch out:</b> ${doc.trap}</p>` : "";
  // Inside the fold on purpose: it qualifies the steps, and the reader deciding how far
  // to trust them is the reader who has opened them.
  const checked = doc.checked
    ? `<p class="wtdoc-checked">Steps last checked <time datetime="${doc.checked}">${monthName(doc.checked)}</time>. Menu paths and labels may have changed since; where they differ, the current interface takes precedence.</p>`
    : "";
  return `<div class="wtdoc">
    <p class="wtdoc-needs">${doc.needs}</p>
    <details class="wtdoc-more">
      <summary>How to set this up</summary>
      <ol class="wtdoc-steps">${steps}</ol>
      ${trap}
      ${checked}
    </details>
    <a class="wtdoc-link" href="${handbookHref(doc)}" target="_blank" rel="noopener"
       title="The handbook this server serves itself — no internet needed">Handbook: ${doc.title} <span class="ext" aria-hidden="true">&#8599;</span></a>
    ${doc.reference ? `<a class="wtdoc-link" href="${HANDBOOK_URL}#${doc.reference.anchor}" target="_blank" rel="noopener"
       title="What each operation takes and answers, in the handbook this server serves itself">Handbook: ${doc.reference.title} <span class="ext" aria-hidden="true">&#8599;</span></a>` : ""}
  </div>`;
}

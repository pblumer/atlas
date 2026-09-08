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
// - `needs`   one line, always visible: what must exist before a task of this type runs.
// - `steps`   the ordered setup, provider first, Atlas second, proof last.
// - `trap`    the failure this type is actually reported with — omitted where there is none.
export const WORKER_TYPE_DOCS = {
  worker: {
    anchor: "runbook-jobworker", title: "Job worker",
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
    needs: "Nothing to configure on the server: URL, method, headers and body live in this task. A credential is named here, never pasted.",
    steps: [
      `Put the credential in the vault first: ${VAULT} &rarr; new secret, e.g. <code>crm_token</code>, holding the token or password itself.`,
      `Pick the authentication type (Basic, Bearer, API key) and enter that key as the <b>Secret reference</b>. The model stores the reference only, so an export, a version and a diagram carry no secret.`,
      `Where the call runs on a worker rather than in the engine, the same reference travels as <code>ATLAS_CONNECTOR_&lt;REF&gt;_TOKEN</code>: a worker Atlas supervises is handed the vault's value at spawn — and only for the references the deployed models actually name — while a worker you run yourself reads it from its own environment.`,
      `Name a <b>result variable</b>; the JSON answer lands there and is addressable as <code>=antwort.feld</code>.`,
    ],
    trap: `<code>401</code> is the credential, <code>403</code> the permission. Both park the token on an incident that names the status — read it before changing the URL.`,
  },

  scim: {
    anchor: "runbook-scim", title: "SCIM provisioning",
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
    needs: "Nothing to configure on the server: the endpoint and the request body live in this task, the credential is a vault reference.",
    steps: [
      `Take the endpoint out of the WSDL (<code>soap:address</code>) and the operation name out of its <code>&lt;operation&gt;</code>, e.g. <code>GetUser</code>.`,
      `Store the credential in the vault: ${VAULT}, e.g. <code>ws_password</code>.`,
      `In this task: the endpoint, the operation, the SOAP version (1.1 sends <code>text/xml</code> with a <code>SOAPAction</code> header, 1.2 sends <code>application/soap+xml</code>), and Basic authentication with <code>ws_password</code> as the secret reference.`,
      `The request body is the operation's request element only — Atlas writes the envelope around it. Switch on <code>fx</code> to interpolate variables into the XML.`,
    ],
    trap: `A wrong or missing <code>SOAPAction</code> is answered by most stacks with a fault about the <i>body</i>, not about the header. When the body looks right, check the action first.`,
  },

  ad: {
    anchor: "runbook-ad", title: "Active Directory",
    needs: `A configured Active Directory Worker in ${WORKERS}: an LDAPS URL plus a vault bundle holding the bind account.`,
    steps: [
      `Create a <b>directory service account</b> — not a personal account, not a domain admin — and delegate account and group rights on exactly the OUs the processes work in. Delegation at OU level is the advantage AD has over Entra ID; use it.`,
      `Store the bind bundle in the vault: ${VAULT}, e.g. <code>ad_prod_bind</code> holding <code>{"bindDN": "cn=svc-atlas,ou=service,dc=example,dc=com", "password": "…"}</code>.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>Active Directory</b>, a <b>name</b> (this task states exactly that name), endpoint <code>ldaps://dc.example.com:636</code>, credential reference <code>ad_prod_bind</code>. Saving is enough — Atlas restarts the supervised worker with the new configuration itself.`,
      `Practise without a domain controller: a worker started with <code>ATLAS_AD_MOCK=1</code> serves the same tasks from a directory in its own memory, and <code>ATLAS_AD_MOCK_SEED</code> names an LDIF or DSML file it starts from.`,
    ],
    trap: `<code>ldaps://</code>, not <code>ldap://</code>: a domain controller refuses to set a password over an unencrypted channel, so a plain connection works for everything except the one thing a joiner process needs.`,
  },

  ldif: {
    anchor: "runbook-ldif", title: "Directory file (LDIF/DSML)",
    needs: "Nothing to configure: no Worker record, no credential, no endpoint. The task reads or writes text that is already a process variable.",
    steps: [
      `Pick the <b>format</b> explicitly — LDIF or DSML. There is deliberately no default: guessing a directory file's format from its bytes is how a malformed file becomes a plausible-looking empty result.`,
      `Reading: put the file's text into a variable first (a form upload, a REST call, a script task) and name it as the source. The entries land in the result variable as <code>{dn, attributes}</code> objects.`,
      `Writing: the source variable holds entries in that same shape — which is exactly what an LDAP search or an AD sync returns, so a directory read can be written straight to a file.`,
    ],
  },

  entra: {
    anchor: "runbook-entra", title: "Microsoft Entra ID",
    needs: `A configured Microsoft Entra ID Worker in ${WORKERS}: a vault bundle with tenant, client and secret. No endpoint — Graph's address is the same for everyone.`,
    steps: [
      `Entra admin center &rarr; <b>App registrations</b> &rarr; new registration, <b>dedicated to this worker</b>. Note the <i>tenant id</i> and <i>client id</i>, then <i>Certificates &amp; secrets</i> &rarr; new client secret — its value is shown once.`,
      `<i>API permissions</i> &rarr; <b>Microsoft Graph</b> &rarr; <b>Application permissions</b>. For the usual joiner/mover/leaver case exactly two suffice: <code>User.ReadWrite.All</code> and <code>Group.ReadWrite.All</code> (or the narrower <code>GroupMember.ReadWrite.All</code> when no groups are created). Read-only processes take the <code>*.Read.All</code> variants. Remove <code>Directory.ReadWrite.All</code> if it is there.`,
      `<b>Grant admin consent.</b> Application permissions do nothing until this is done — and its absence shows up as "it does nothing, without an error".`,
      `Store the bundle in the vault: ${VAULT}, e.g. <code>entra_contoso</code> holding <code>{"tenantId": "…", "clientId": "…", "clientSecret": "…"}</code>.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>Microsoft Entra ID</b>, a name, credential reference <code>entra_contoso</code>, no endpoint.`,
    ],
    trap: `Resetting a password or touching a role-bearing account needs more than <code>User.ReadWrite.All</code>: the app must also hold a <b>directory role</b> such as <i>User Administrator</i> — and even then it cannot act on holders of higher-privileged roles.`,
  },

  // The three database entries quote the connection string exactly as the Console's own
  // field placeholder does, `PASSWORT` included (SQL_DSN_EXAMPLES in workerdialog.js):
  // an operator reads the example here and types into the field there, and two spellings
  // of the same example is one more thing to wonder about.
  mssql: {
    anchor: "runbook-mssql", title: "Microsoft SQL Server",
    needs: `A configured Microsoft SQL Server Worker in ${WORKERS}. Its whole configuration is one connection string, which is the credential.`,
    steps: [
      `Create a <b>technical database user</b> with rights on exactly the tables, views and procedures the processes need. No <code>db_owner</code>. Granting on views and stored procedures rather than tables makes the interface a contract instead of your schema.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>Microsoft SQL Server</b>, a name, and the connection string <code>sqlserver://atlas:PASSWORT@sql.example.com:1433?database=hr</code>. Atlas seals it into the vault and keeps only a reference — the Console can never show it back.`,
      `Write the statement literally and pass every value as a <b>bound parameter</b> (<code>@p1</code>, or by name — SQL Server is the one of the three drivers that binds by name, so an object-shaped parameters variable is accepted here).`,
      `Practise without a database: switch the worker to mock mode and the same task answers from seeded rows in memory; what a run asked for is listed under <i>Operations &rsaquo; Mock database</i>.`,
    ],
    trap: `The <b>statement</b> field has no <code>fx</code> toggle, on purpose: a statement assembled from process data would be an injection that needs no quoting bug to work.`,
  },

  mariadb: {
    anchor: "runbook-mariadb", title: "MariaDB",
    needs: `A configured MariaDB Worker in ${WORKERS}. Its whole configuration is one connection string, which is the credential.`,
    steps: [
      `Create a <b>technical database user</b> with rights on exactly the tables and views the processes need. No <code>SUPER</code>, no <code>GRANT ALL</code>.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>MariaDB</b>, a name, and the connection string <code>atlas:PASSWORT@tcp(mariadb.example.com:3306)/hr?parseTime=true</code> — the MySQL driver's own form, which is not a URL.`,
      `Write the statement literally and pass every value as a <b>bound parameter</b>, marked <code>?</code> and bound in order. An object as the parameters variable is refused rather than put into an order nobody wrote.`,
      `Practise without a database: switch the worker to mock mode and the same task answers from seeded rows in memory (<i>Operations &rsaquo; Mock database</i>).`,
    ],
    trap: `The <b>statement</b> field has no <code>fx</code> toggle, on purpose: a statement assembled from process data would be an injection that needs no quoting bug to work.`,
  },

  postgres: {
    anchor: "runbook-postgres", title: "PostgreSQL",
    needs: `A configured PostgreSQL Worker in ${WORKERS}. Its whole configuration is one connection string, which is the credential.`,
    steps: [
      `Create a <b>technical database role</b> with rights on exactly the tables and views the processes need — not the owner role, and no <code>SUPERUSER</code>.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>PostgreSQL</b>, a name, and the connection string <code>postgres://atlas:PASSWORT@db.example.com:5432/hr?sslmode=require</code>. Keep <code>sslmode=require</code> unless the database is on this very host.`,
      `Write the statement literally and pass every value as a <b>bound parameter</b>, marked <code>$1</code>, <code>$2</code> and bound in order.`,
      `Practise without a database: switch the worker to mock mode and the same task answers from seeded rows in memory (<i>Operations &rsaquo; Mock database</i>).`,
    ],
    trap: `The <b>statement</b> field has no <code>fx</code> toggle, on purpose: a statement assembled from process data would be an injection that needs no quoting bug to work.`,
  },

  clio: {
    anchor: "runbook-clio", title: "clio event store",
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
    needs: `A configured temis Worker in ${WORKERS}: an endpoint, and a token reference only where the service requires one.`,
    steps: [
      `Check whether you need it at all: a DMN decision deployed into Atlas is evaluated by the built-in engine and needs no worker. temis is for keeping decisions <b>centrally</b>, outside this installation.`,
      `Where the service requires a token, store it in the vault: ${VAULT}, e.g. <code>temis_token</code>.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>temis</b>, a name, the endpoint <code>https://temis.example.com</code>, and the token reference where there is one.`,
      `On the business rule task, choose <b>External (temis worker)</b> and name that worker.`,
    ],
  },

  mail: {
    anchor: "runbook-mail", title: "Mail",
    needs: `A configured mail Worker in ${WORKERS} — and one of its four transports needs no mail server at all.`,
    steps: [
      `<b>Start with Preview.</b> ${WORKERS} &rarr; <b>New worker</b>: type <b>Mail</b>, provider <b>Preview</b>, a name, a sender address. It frames the message exactly as it would be sent and puts it in <i>Operations &rsaquo; Outbox</i> instead of delivering it — a mail task can be built and demonstrated with no credentials.`,
      `<b>SMTP:</b> provider <b>SMTP</b>, endpoint <code>smtp.example.com:587</code> (587 is assumed without a port), plus user and password. The only route that also works with a mail server in your own basement.`,
      `<b>Gmail:</b> provider <b>Gmail API</b>, no endpoint, and a vault bundle — a service account <code>{"method": "serviceAccount", "clientEmail": "…", "privateKey": "-----BEGIN PRIVATE KEY-----\\n…", "subject": "sender@your-domain"}</code> (Workspace, domain-wide delegation for the <code>gmail.send</code> scope), or <code>{"method": "refreshToken", "clientId": …, "clientSecret": …, "refreshToken": …}</code> for a single mailbox.`,
      `<b>Microsoft Graph:</b> provider <b>Microsoft Graph</b>, no endpoint, vault bundle <code>{"method": "clientCredentials", "tenantId": "…", "clientId": "…", "clientSecret": "…"}</code> from an app registration with the <code>Mail.Send</code> application permission and admin consent. This is the route when your organisation has switched off SMTP basic authentication.`,
      `Going live is a change of <b>worker</b>, not of model: the task keeps naming the same worker name.`,
    ],
    trap: `Send the first real batch to yourself, and check that the sender address is allowed to send (SPF/DKIM). An automation sending two hundred wrong mails is faster than any correction.`,
  },

  csv: {
    anchor: "runbook-csv", title: "Text file",
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
    needs: `A configured SharePoint Worker in ${WORKERS}: a vault bundle for Microsoft Graph. No endpoint — Graph's address is the same for everyone.`,
    steps: [
      `The groundwork is Entra ID's: an <b>app registration</b>, a client secret, and admin consent. Note tenant id, client id and secret.`,
      `Grant the <b>site-specific</b> permission <code>Sites.Selected</code> and then grant this app access to the individual sites it works on. <code>Sites.ReadWrite.All</code> works too and is the grant an audit writes up.`,
      `Store the bundle in the vault: ${VAULT}, e.g. <code>sharepoint_auth</code> holding <code>{"method": "clientCredentials", "tenantId": "…", "clientId": "…", "clientSecret": "…"}</code>.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>Microsoft SharePoint</b>, a name, credential reference <code>sharepoint_auth</code>, no endpoint.`,
      `In the task, the <b>Site</b> is Graph's site identifier <code>contoso.sharepoint.com,&lt;siteId&gt;,&lt;webId&gt;</code>, and the list is named by title or id.`,
    ],
    trap: `A <code>403</code> here usually means not "a permission is missing" but "this app has no access to <i>this</i> site" — which is why the same app works on one site and not on the next.`,
  },

  remedy: {
    anchor: "runbook-remedy", title: "BMC Remedy",
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
    anchor: "runbook-googlesheets", title: "Google Sheets &amp; Drive",
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

  discord: {
    anchor: "runbook-discord", title: "Discord",
    needs: `A configured Discord Worker in ${WORKERS}: a vault bundle with the bot token. No endpoint — Discord's API base is the same for everyone.`,
    steps: [
      `<b>Discord Developer Portal</b> &rarr; <i>New Application</i> &rarr; <i>Bot</i> &rarr; <b>Reset Token</b>, and copy the token. It is shown once.`,
      `Store it in the vault: ${VAULT}, e.g. <code>discord_team</code> holding <code>{"botToken": "…"}</code> — the token alone; Atlas composes the <code>Bot </code> scheme itself.`,
      `<b>Invite the bot to your server</b> (Developer Portal &rarr; <i>OAuth2</i> &rarr; URL generator, scope <code>bot</code>) and make sure it holds <b>View Channel</b> and <b>Send Messages</b> in every channel a process writes to.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>Discord</b>, a name, credential reference <code>discord_team</code>, no endpoint.`,
      `Get the <b>channel id</b>: in Discord enable <i>Developer Mode</i> (User settings &rarr; Advanced), then right-click the channel &rarr; <b>Copy Channel ID</b>. A thread is itself a channel, so posting into one is a Send message naming the thread's id.`,
    ],
    trap: `A missing channel grant comes back as code <code>50001</code>, <i>Missing Access</i> — not as a bad token. Check the channel's permissions before the token.`,
  },

  aitask: {
    anchor: "runbook-ai", title: "AI worker",
    needs: `A configured AI Worker in ${WORKERS}: a wire format, an API key reference, and the model it asks by default.`,
    steps: [
      `Get an <b>API key</b> from the provider — Anthropic Console, OpenAI platform, or whatever your gateway issues — for a key that is this server's, not a person's.`,
      `Store it in the vault: ${VAULT}, e.g. <code>anthropic_api_key</code>, holding the key itself.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>AI agent model</b>, a name, the <b>provider</b> (<i>Messages</i> is Anthropic's format, <i>Chat Completions</i> is OpenAI's and anything calling itself OpenAI-compatible), the API key reference, and the <b>default model</b>. Leave the endpoint empty unless you go through a gateway, a proxy or a self-hosted deployment.`,
      `In the task, leave <b>Model</b> empty to ask the worker's default, or name one per task — a cheap model for a classification and a strong one for advice, through the same worker and the same key.`,
    ],
    trap: `A model call takes seconds to minutes and can hang, so Atlas never runs it in the engine: it supervises a worker for this type and picks up a changed model as soon as you save, with no restart.`,
  },

  webscrape: {
    anchor: "runbook-webscrape", title: "Web scraping",
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
    needs: "No Worker record and no credential — it acts on this Atlas's own login store, which is why it is fenced instead of configured.",
    steps: [
      `It runs <b>only for processes in the protected system project</b>. A copy of the same task in an ordinary project is refused at deploy — that fence is the whole security model of this type.`,
      `It is on by default. An installation that does not want Atlas logins created by a process starts the server with <code>--user-provisioning=false</code>; the tasks then park instead of acting.`,
      `Put a <b>human approval step</b> in front of it and let an admin set the roles and the initial password there. Never let a requester choose roles on a public start form.`,
      `Pass the password as a FEEL reference to a variable (<code>=initialpasswort</code>, at least 8 characters), so no password is written into the model.`,
    ],
    trap: `This is the one Worker Type that reopens the boundary between "a process" and "who may use Atlas". Treat every change to such a model as a change to access control.`,
  },

  mockup: {
    anchor: "runbook-mockup", title: "Mockup (simulation)",
    needs: "Nothing to configure, and nothing outside Atlas: the engine acts the foreign system itself.",
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

// renderDoc is the one markup for both surfaces; a Worker Type nobody documented
// renders nothing rather than an empty box (the Go guard makes sure there is none).
function renderDoc(doc) {
  if (!doc) return "";
  const steps = (doc.steps || []).map((s) => `<li>${s}</li>`).join("");
  const trap = doc.trap ? `<p class="wtdoc-trap"><b>Watch out:</b> ${doc.trap}</p>` : "";
  return `<div class="wtdoc">
    <p class="wtdoc-needs">${doc.needs}</p>
    <details class="wtdoc-more">
      <summary>How to set this up</summary>
      <ol class="wtdoc-steps">${steps}</ol>
      ${trap}
    </details>
    <a class="wtdoc-link" href="${handbookHref(doc)}" target="_blank" rel="noopener"
       title="The handbook this server serves itself — no internet needed">Handbook: ${doc.title} <span class="ext" aria-hidden="true">&#8599;</span></a>
  </div>`;
}

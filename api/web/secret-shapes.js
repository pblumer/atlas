// What the value behind a worker's token reference has to look like — the
// knowledge the Secrets panel needs to say something useful about a field whose
// content it can never show (ADR-0155).
//
// It lives in its own module for two reasons: app.js boots the whole console on
// import and so cannot be exercised in isolation, and this is the part worth
// exercising — a pure mapping from "which worker resolves this name" to "what the
// value must be", plus the check that mirrors the server's own decoders.
//
// The shapes track the Go side: connector/mail/oauth.go's credentialBundle,
// connector/sharepoint/oauth.go's, api/connectors.go's remedyCredentials, and
// connector/jira/rest.go's, connector/googlesheets/oauth.go's,
// connector/discord/rest.go's and connector/s3/rest.go's credentialBundle, and
// connector/exchange/config.go's, connector/googlecontacts/config.go's,
// connector/carddav/config.go's and connector/wikiwikibangbang/config.go's Bundle. A field
// added there is a field added here.

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// SECRET_SHAPES says what the value behind a token reference has to look like, keyed
// by the referencing worker's Worker Type (and, for mail, its provider). It exists because
// a vault secret is a name and an opaque string: nothing about the field says whether
// it wants a password or a JSON credential bundle, and since the value is never read
// back, a wrong shape is invisible until a task parks behind an incident hours later
// (ADR-0155). The shapes mirror the Go decoders — connector/mail/oauth.go's
// credentialBundle, connector/sharepoint/oauth.go's, api/connectors.go's
// remedyCredentials, and the connector/jira, connector/googlesheets, connector/discord
// and connector/s3 bundles — so a change there is a change here.
export const SECRET_SHAPES = {
  "mail:gmail": {
    what: "a Google OAuth credential bundle (JSON)",
    oauth: "google",
    skeleton: {
      method: "refreshToken",
      clientId: "…apps.googleusercontent.com",
      clientSecret: "GOCSPX-…",
      refreshToken: "1//…",
    },
    note: "Atlas fills in <code>tokenUrl</code> and <code>scope</code> for Gmail. A service account uses <code>{\"method\":\"serviceAccount\",\"clientEmail\":…,\"privateKey\":…}</code> instead. Pasting the bare refresh token — it starts with <code>1//</code> — is the common mistake: that is one field of the bundle, not the bundle.",
  },
  "mail:microsoft": {
    what: "a Microsoft Graph OAuth credential bundle (JSON)",
    oauth: "microsoft",
    skeleton: { method: "clientCredentials", tenantId: "…", clientId: "…", clientSecret: "…" },
    note: "The tenant id is what atlas builds the token endpoint from.",
  },
  "sharepoint:": {
    what: "a Microsoft Graph OAuth credential bundle (JSON)",
    oauth: "microsoft",
    skeleton: { method: "clientCredentials", tenantId: "…", clientId: "…", clientSecret: "…" },
    note: "An app that runs unattended may prove itself with a certificate instead: <code>\"certificate\"</code> — the certificate and its unencrypted private key as one PEM text — in place of <code>clientSecret</code>. On a Windows server, <code>\"certificateThumbprint\"</code> may instead name a certificate already in the worker host's <code>CurrentUser\\My</code> or <code>LocalMachine\\My</code>, as for an Entra ID worker; its key signs there and may be non-exportable. Give one of the three. A <code>refreshToken</code> bundle keeps its client secret.",
    // connector/sharepoint/oauth.go reads a bundle without a method as clientCredentials,
    // and a clientCredentials bundle may carry a certificate in place of the secret —
    // as PEM, or by the thumbprint of one in the Windows certificate store.
    certificate: true,
    thumbprint: true,
  },
  "remedy:": {
    what: "the AR System username and password (JSON)",
    fields: ["username", "password"],
    skeleton: { username: "…", password: "…" },
  },
  "jira:": {
    what: "a Jira credential bundle (JSON): {email, apiToken} for Jira Cloud, or {token} for a Data Center personal access token",
    // anyOf, not fields: the two shapes are alternatives, and a bundle is valid when
    // it satisfies either — which the check below states as "one of these groups",
    // rather than demanding every field of both.
    anyOf: [["email", "apiToken"], ["token"]],
    skeleton: { email: "bot@acme.example", apiToken: "ATATT…" },
    note: "A Jira Cloud API token is created under <b>Atlassian account &rsaquo; Security &rsaquo; API tokens</b> and is used with the account's e-mail address, not a password. For Jira Data Center, store <code>{\"token\": \"…\"}</code> instead — a personal access token, sent as a bearer.",
  },
  "googlesheets:": {
    what: "a Google OAuth credential bundle (JSON): a service account's signing key, or a consumer account's refresh token",
    oauth: "google",
    // anyOf, not fields: the two grants are alternatives, and a bundle is valid when it
    // satisfies either — the same shape the Jira entry uses for Cloud and Data Center.
    anyOf: [["clientEmail", "privateKey"], ["clientId", "clientSecret", "refreshToken"]],
    skeleton: {
      method: "serviceAccount",
      clientEmail: "atlas@\u2026.iam.gserviceaccount.com",
      privateKey: "-----BEGIN PRIVATE KEY-----\u2026",
    },
    note: "Copied out of the JSON key file Google hands out for a service account (<code>client_email</code> and <code>private_key</code>, camel-cased here). Atlas fills in <code>tokenUrl</code> and the Sheets and Drive <code>scope</code>. Add <code>\"subject\"</code> to act as a Workspace user through domain-wide delegation. Pasting the whole key file is the common mistake: it is that file's two fields, not the file.",
  },
  "googlecontacts:": {
    what: "a Google OAuth credential bundle (JSON): a service account's signing key with the Workspace user whose address book it reads, or a consumer account's refresh token",
    oauth: "google",
    // anyOf for Google Sheets' reason — two grants — with the subject required beside the
    // key: a service account has no address book of its own, and the worker refuses one
    // without it.
    anyOf: [["clientEmail", "privateKey", "subject"], ["clientId", "clientSecret", "refreshToken"]],
    skeleton: {
      method: "serviceAccount",
      clientEmail: "atlas@\u2026.iam.gserviceaccount.com",
      privateKey: "-----BEGIN PRIVATE KEY-----\u2026",
      subject: "verkauf@firma.ch",
    },
    note: "<code>clientEmail</code> and <code>privateKey</code> are <code>client_email</code> and <code>private_key</code> from the service account's JSON key file. <code>subject</code> is the Workspace user whose contacts the Worker reads and changes; the service account needs <b>domain-wide delegation</b> for <code>https://www.googleapis.com/auth/contacts</code> (Admin console &rsaquo; Security &rsaquo; API controls). Search directory also needs <code>directory.readonly</code>: name both in <code>\"scope\"</code>, separated by a space, and delegate both. A gmail.com account has no delegation: store <code>{\"method\": \"refreshToken\", \"clientId\", \"clientSecret\", \"refreshToken\"}</code> instead.",
  },
  "carddav:": {
    what: "the CardDAV account's username and password (JSON), and which form its groups take",
    fields: ["username", "password"],
    skeleton: { username: "verkauf@firma.ch", password: "\u2026", groups: "categories" },
    note: "Use an <b>app-specific password</b> where the provider issues one: iCloud requires it, and Fastmail, mailbox.org and a Nextcloud with two-factor sign-in offer it. <code>\"groups\"</code> is <code>\"categories\"</code> (the default: Nextcloud, ownCloud, most servers) or <code>\"group-vcards\"</code> for iCloud and Fastmail, whose groups are cards of their own. The server's address is the Worker's endpoint, not part of this bundle.",
  },
  "wikiwikibangbang:": {
    what: "the API token of the wiki account the Worker writes as (JSON)",
    fields: ["token"],
    skeleton: { token: "wwbb_\u2026" },
    note: "Create the token signed in as a <b>technical account of its own</b> \u2014 an editor in the spaces processes write to, with no system role (an account with <code>admin</code> or <code>erasure</code> is refused) \u2014 under <i>Account</i> &rsaquo; <i>API tokens</i>, scope <code>full</code>. The wiki's address is the Worker's endpoint, not part of this bundle.",
  },
  "entra:": {
    what: "a Microsoft Entra ID app credential bundle (JSON): {tenantId, clientId} with a clientSecret, a certificate or a certificateThumbprint",
    // anyOf, not fields: the app proves itself with a secret, with a certificate as PEM,
    // or with one named by its thumbprint in the Windows certificate store, and a bundle
    // is valid when it carries one — the worker refuses one carrying more.
    anyOf: [["tenantId", "clientId", "clientSecret"], ["tenantId", "clientId", "certificate"], ["tenantId", "clientId", "certificateThumbprint"]],
    skeleton: { tenantId: "\u2026", clientId: "\u2026", clientSecret: "\u2026" },
    note: "Tenant and client id are on the app registration's <b>Overview</b>. A <b>client secret</b> comes from <b>Certificates &amp; secrets &rsaquo; Client secrets</b> and is shown once. A <b>certificate</b> is stored instead as <code>\"certificate\"</code>: the certificate and its <b>unencrypted</b> private key as one PEM text (<code>-----BEGIN CERTIFICATE-----</code> \u2026 <code>-----BEGIN PRIVATE KEY-----</code> \u2026), after uploading the certificate's public part under <b>Certificates &amp; secrets &rsaquo; Certificates</b>. On a Windows server, <code>\"certificateThumbprint\"</code> may instead name a certificate already in the worker host's <code>CurrentUser\\My</code> or <code>LocalMachine\\My</code>: its key signs there and may be non-exportable — elsewhere such a bundle is not used. Carry exactly one of the three.",
  },
  "teamsphone:": {
    what: "a Microsoft Teams Phone app credential bundle (JSON): {tenantId, clientId} with a certificate or a certificateThumbprint",
    // anyOf for Entra's reason: the certificate travels as PEM or is named by the
    // thumbprint of one already in the worker host's store, and never both.
    anyOf: [["tenantId", "clientId", "certificate"], ["tenantId", "clientId", "certificateThumbprint"]],
    skeleton: { tenantId: "\u2026", clientId: "\u2026", certificate: "-----BEGIN CERTIFICATE-----\u2026" },
    note: "Tenant and client id are on the app registration's <b>Overview</b>; the app needs the Teams administrator role and the Graph application permission <code>User.ReadWrite.All</code> (finding a resource account Teams does not show yet, and deleting one). <code>\"certificate\"</code> is the certificate and its <b>unencrypted</b> private key as one PEM text, after uploading the public part under <b>Certificates &amp; secrets &rsaquo; Certificates</b>. <code>\"certificateThumbprint\"</code> instead names a certificate already in the worker host's <code>CurrentUser\\My</code> or <code>LocalMachine\\My</code>. Carry one of the two, never both; there is no client secret, because the module signs in with a certificate.",
  },
  "discord:": {
    what: "a Discord bot credential bundle (JSON): {botToken}",
    fields: ["botToken"],
    skeleton: { botToken: "MTIz\u2026" },
    note: "The bot token from <b>Discord Developer Portal &rsaquo; your application &rsaquo; Bot &rsaquo; Reset Token</b>. Store the token alone: Atlas composes the <code>Bot </code> scheme itself, so a value pasted with the prefix has it stripped rather than sent twice. The bot must be invited to the server and have <b>View Channel</b> and <b>Send Messages</b> in each channel a process writes to \u2014 a missing grant is answered with code 50001, <i>Missing Access</i>.",
  },
  "s3:": {
    what: "an S3 access key (JSON): {accessKeyId, secretAccessKey, region}",
    fields: ["accessKeyId", "secretAccessKey", "region"],
    skeleton: { accessKeyId: "AKIA\u2026", secretAccessKey: "\u2026", region: "eu-central-1" },
    note: "From <b>AWS console &rsaquo; IAM &rsaquo; Users &rsaquo; Security credentials &rsaquo; Create access key</b>, or your store's own console (MinIO: <b>Access Keys &rsaquo; Create</b>). The <b>region</b> belongs in here rather than beside it because the signature is computed with it — a self-hosted store that has no regions commonly answers to <code>us-east-1</code>. Add <code>\"sessionToken\"</code> for a key issued by STS. The bucket is not part of the credential: a task names it, so one key can serve several.",
  },
  "exchange:": {
    what: "an Exchange organization (JSON): {organization, clientId, certificate} for Exchange Online, or {target: \"onprem\", …} for an Exchange Server",
    // Three shapes: Online with a certificate, Online with a secret, or a server, which
    // may carry nothing but its target (a Kerberos service account needs no password).
    anyOf: [["organization", "clientId", "certificate"], ["organization", "clientId", "clientSecret"], ["target"]],
    skeleton: { organization: "contoso.onmicrosoft.com", clientId: "\u2026", certificate: "-----BEGIN CERTIFICATE-----\u2026-----END CERTIFICATE-----\n-----BEGIN PRIVATE KEY-----\u2026-----END PRIVATE KEY-----" },
    note: "<b>Exchange Online:</b> the app registration's client id, the tenant's initial domain, and its certificate with the private key as one PEM text (<code>clientSecret</code> in place of <code>certificate</code> is accepted, but Microsoft documents certificates for unattended Exchange administration). The app needs <code>Exchange.ManageAsApp</code> with admin consent and an Exchange role in Entra. <b>Exchange Server:</b> <code>{\"target\": \"onprem\"}</code>, with <code>username</code> and <code>password</code> unless the worker runs as a Kerberos service account; the server's Remote PowerShell URI is the worker's endpoint, not part of this secret.",
  },
  "mail:smtp": { what: "the SMTP password for the worker's sender address (a plain string)" },
  "mail:preview": { what: "nothing — the preview provider needs no credential" },
  "clio:": { what: "a clio API key (a plain string, <code>kid.secret</code>)" },
  "temis:": { what: "the temis service token (a plain string)" },
};

// secretShapeFor resolves the expected shape from whichever worker references this
// secret name. Two workers of different Worker Types on one secret is a configuration
// nobody intends, so the first is enough; no reference at all means no expectation,
// and nothing is checked.
export function secretShapeFor(workers, name) {
  const user = (workers || []).find((k) => k.credentialsRef === name);
  if (!user) return null;
  const shape = SECRET_SHAPES[user.kind + ":" + (user.kind === "mail" ? (user.provider || "smtp") : "")];
  return shape ? { ...shape, worker: user } : null;
}

// checkSecretValue reports why a value cannot be what the referencing worker needs,
// or "" when it can. It mirrors the server's own decoders rather than guessing: the
// per-method required fields are newTokenSource's, and the token-URL rule is the one
// that turns a Microsoft bundle without a tenant into "credential has no token URL".
//
// It refuses rather than warns. The value is write-only — after the save nobody, user
// or server, can look at it and say what went wrong — so entry is the last moment a
// mistake can be named, and the worker that defines the expectation is named with
// it, so changing that worker remains the way out.
export function checkSecretValue(shape, value) {
  if (!shape || (!shape.oauth && !shape.fields && !shape.anyOf)) return "";
  let parsed;
  try {
    parsed = JSON.parse(value);
  } catch (e) {
    return `${shape.worker.name} (${shape.worker.kind}) needs ${shape.what}, and this is not valid JSON: ${e.message}`;
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    return `${shape.worker.name} (${shape.worker.kind}) needs ${shape.what} — a JSON object, not a ${Array.isArray(parsed) ? "list" : typeof parsed}.`;
  }
  const missing = (need) => need.filter((k) => !String(parsed[k] || "").trim());
  if (shape.anyOf) {
    // Satisfying any one group is enough. Reporting the *nearest* group — the one
    // with the fewest fields still missing — is what turns "this is not a Jira
    // bundle" into "your Cloud bundle is missing apiToken".
    const gaps = shape.anyOf.map(missing);
    if (gaps.some((g) => g.length === 0)) return "";
    const nearest = gaps.reduce((a, b) => (b.length < a.length ? b : a));
    return `Missing ${nearest.map((k) => `"${k}"`).join(" and ")}.`;
  }
  if (shape.fields) {
    const gone = missing(shape.fields);
    return gone.length ? `Missing ${gone.map((k) => `"${k}"`).join(" and ")}.` : "";
  }
  const method = parsed.method || (shape.certificate ? "clientCredentials" : "");
  // A worker that takes a certificate takes it in place of the client secret, never
  // beside it — the server refuses a bundle carrying two rather than pick one. Where the
  // shape allows it, the certificate may be named by its thumbprint instead of given as
  // PEM, and that counts as a second certificate, not as the same one.
  const given = (k) => String(parsed[k] || "").trim() !== "";
  const certFields = shape.certificate ? ["certificate", ...(shape.thumbprint ? ["certificateThumbprint"] : [])] : [];
  const quote = (ks) => ks.map((k) => `"${k}"`);
  if (method === "refreshToken" && certFields.some(given)) {
    return `A "refreshToken" bundle signs in with "clientSecret"; a certificate belongs to a "clientCredentials" bundle.`;
  }
  const certs = method === "clientCredentials" ? certFields.filter(given) : [];
  const credentials = [...(given("clientSecret") ? ["clientSecret"] : []), ...certs];
  if (credentials.length === 2) return `The bundle carries ${quote(credentials).join(" and ")}; give one, not both.`;
  if (credentials.length > 2) return `The bundle carries ${quote(credentials).join(", ")}; give one of them.`;
  const need = {
    refreshToken: ["clientId", "clientSecret", "refreshToken"],
    clientCredentials: certs.length ? ["clientId", certs[0]] : ["clientId", "clientSecret"],
    serviceAccount: ["clientEmail", "privateKey"],
  }[method];
  if (!need) {
    return `"method" is ${method ? `"${method}"` : "missing"} — it must be "refreshToken", "clientCredentials" or "serviceAccount".`;
  }
  // A missing secret is named with the certificates that could stand in for it.
  const gone = missing(need).map((k) =>
    k === "clientSecret" && method === "clientCredentials" && certFields.length
      ? `one of ${quote(["clientSecret", ...certFields.slice(0, -1)]).join(", ")} or "${certFields[certFields.length - 1]}"`
      : `"${k}"`);
  if (gone.length) return `A "${method}" bundle also needs ${gone.join(" and ")}.`;
  // Microsoft builds its token endpoint from the tenant; without either there is
  // nothing to call, which the server reports much later as "no token URL".
  if (shape.oauth === "microsoft" && method === "clientCredentials" && !parsed.tenantId && !parsed.tokenUrl) {
    return `A Microsoft bundle needs "tenantId" (or an explicit "tokenUrl").`;
  }
  return "";
}

// secretHintHTML renders what this secret is for, and — where the shape is a JSON
// bundle — the skeleton to fill in, so the form answers the question instead of
// leaving it to a later incident.
export function secretHintHTML(shape) {
  if (!shape) {
    return `<p class="secret-hint muted">Not referenced by any worker yet, so there is no expected shape to check against.</p>`;
  }
  const skeleton = shape.skeleton ? JSON.stringify(shape.skeleton, null, 2) : "";
  return `<div class="secret-hint">
    <p><b>${esc(shape.worker.name)}</b> <span class="muted">(${esc(shape.worker.kind)}${shape.worker.provider ? " · " + esc(shape.worker.provider) : ""})</span> resolves this reference, and needs ${shape.what}.</p>
    ${skeleton ? `<pre class="secret-skeleton">${esc(skeleton)}</pre>
      <button class="btn ghost sm" type="button" data-fill title="Fill the value box with a template you can edit">Insert this skeleton</button>` : ""}
    ${shape.note ? `<p class="muted">${shape.note}</p>` : ""}
  </div>`;
}

// secretValueFieldHTML is the input for a secret value. A JSON bundle gets a visible
// textarea, not a password box: it is several lines that have to be pasted and read
// back before saving, and hiding it is what made the shape unknowable in the first
// place. Anything else stays masked. Either way the value is never shown again after
// the save — the vault does not hand it back.
export function secretValueFieldHTML(shape) {
  return shape && shape.skeleton
    ? `<label class="field" style="margin:0"><span>Value (JSON)</span>
        <textarea name="value" rows="7" spellcheck="false" class="secret-json" placeholder="{ … }" required></textarea></label>`
    : `<label class="field" style="margin:0"><span>Value</span>
        <input name="value" type="password" placeholder="••••••••" required/></label>`;
}

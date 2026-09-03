// What the value behind a connector's token reference has to look like — the
// knowledge the Secrets panel needs to say something useful about a field whose
// content it can never show (ADR-0155).
//
// It lives in its own module for two reasons: app.js boots the whole console on
// import and so cannot be exercised in isolation, and this is the part worth
// exercising — a pure mapping from "which connector resolves this name" to "what the
// value must be", plus the check that mirrors the server's own decoders.
//
// The shapes track the Go side: connector/mail/oauth.go's credentialBundle,
// connector/sharepoint/oauth.go's, api/connectors.go's remedyCredentials, and
// connector/jira/rest.go's and connector/googlesheets/oauth.go's credentialBundle. A
// field added there is a field added here.

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// SECRET_SHAPES says what the value behind a token reference has to look like, keyed
// by the referencing connector's kind (and, for mail, its provider). It exists because
// a vault secret is a name and an opaque string: nothing about the field says whether
// it wants a password or a JSON credential bundle, and since the value is never read
// back, a wrong shape is invisible until a task parks behind an incident hours later
// (ADR-0155). The shapes mirror the Go decoders — connector/mail/oauth.go's
// credentialBundle, connector/sharepoint/oauth.go's, api/connectors.go's
// remedyCredentials, and the connector/jira and connector/googlesheets bundles — so a
// change there is a change here.
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
    note: "The tenant id is what Atlas builds the token endpoint from.",
  },
  "sharepoint:": {
    what: "a Microsoft Graph OAuth credential bundle (JSON)",
    oauth: "microsoft",
    skeleton: { method: "clientCredentials", tenantId: "…", clientId: "…", clientSecret: "…" },
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
  "mail:smtp": { what: "the SMTP password for the connector's sender address (a plain string)" },
  "mail:preview": { what: "nothing — the preview provider needs no credential" },
  "clio:": { what: "a clio API key (a plain string, <code>kid.secret</code>)" },
  "temis:": { what: "the temis service token (a plain string)" },
};

// secretShapeFor resolves the expected shape from whichever connector references this
// secret name. Two connectors of different kinds on one secret is a configuration
// nobody intends, so the first is enough; no reference at all means no expectation,
// and nothing is checked.
export function secretShapeFor(connectors, name) {
  const user = (connectors || []).find((k) => k.credentialsRef === name);
  if (!user) return null;
  const shape = SECRET_SHAPES[user.kind + ":" + (user.kind === "mail" ? (user.provider || "smtp") : "")];
  return shape ? { ...shape, connector: user } : null;
}

// checkSecretValue reports why a value cannot be what the referencing connector needs,
// or "" when it can. It mirrors the server's own decoders rather than guessing: the
// per-method required fields are newTokenSource's, and the token-URL rule is the one
// that turns a Microsoft bundle without a tenant into "credential has no token URL".
//
// It refuses rather than warns. The value is write-only — after the save nobody, user
// or server, can look at it and say what went wrong — so entry is the last moment a
// mistake can be named, and the connector that defines the expectation is named with
// it, so changing that connector remains the way out.
export function checkSecretValue(shape, value) {
  if (!shape || (!shape.oauth && !shape.fields && !shape.anyOf)) return "";
  let parsed;
  try {
    parsed = JSON.parse(value);
  } catch (e) {
    return `${shape.connector.name} (${shape.connector.kind}) needs ${shape.what}, and this is not valid JSON: ${e.message}`;
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    return `${shape.connector.name} (${shape.connector.kind}) needs ${shape.what} — a JSON object, not a ${Array.isArray(parsed) ? "list" : typeof parsed}.`;
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
  const need = {
    refreshToken: ["clientId", "clientSecret", "refreshToken"],
    clientCredentials: ["clientId", "clientSecret"],
    serviceAccount: ["clientEmail", "privateKey"],
  }[parsed.method];
  if (!need) {
    return `"method" is ${parsed.method ? `"${parsed.method}"` : "missing"} — it must be "refreshToken", "clientCredentials" or "serviceAccount".`;
  }
  const gone = missing(need);
  if (gone.length) return `A "${parsed.method}" bundle also needs ${gone.map((k) => `"${k}"`).join(" and ")}.`;
  // Microsoft builds its token endpoint from the tenant; without either there is
  // nothing to call, which the server reports much later as "no token URL".
  if (shape.oauth === "microsoft" && parsed.method === "clientCredentials" && !parsed.tenantId && !parsed.tokenUrl) {
    return `A Microsoft bundle needs "tenantId" (or an explicit "tokenUrl").`;
  }
  return "";
}

// secretHintHTML renders what this secret is for, and — where the shape is a JSON
// bundle — the skeleton to fill in, so the form answers the question instead of
// leaving it to a later incident.
export function secretHintHTML(shape) {
  if (!shape) {
    return `<p class="secret-hint muted">Not referenced by any connector yet, so there is no expected shape to check against.</p>`;
  }
  const skeleton = shape.skeleton ? JSON.stringify(shape.skeleton, null, 2) : "";
  return `<div class="secret-hint">
    <p><b>${esc(shape.connector.name)}</b> <span class="muted">(${esc(shape.connector.kind)}${shape.connector.provider ? " · " + esc(shape.connector.provider) : ""})</span> resolves this reference, and needs ${shape.what}.</p>
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

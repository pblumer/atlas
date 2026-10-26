// The content sources a product's description may come from, as an administrator
// configures them (ADR-draft-service-content-from-an-external-source, § 3 and § 4).
//
// A source is installation configuration beside the Workers and the vault: a base
// URL, the vault secret holding the technical account's read token, the picture
// field, the labels that are not keywords, and whether the shop may link to a page.
// The token itself is never here — only the name of the secret, which is what the
// server stores and what its audit line names.
//
// The check is the point of the screen. It asks the source for the account's
// standing — reachable, the token's scope, the spaces it holds viewer in — and lists
// every problem the server finds with it, because an account that is more than a
// read-only viewer is the one configuration that would let protected content reach
// the shop, and the administrator is the person who can fix it.
//
// Everything a source answers is drawn as text, through el().

import { t } from "./i18n.js";
import { el, fmtNs } from "./catalog-content.js";

const enc = encodeURIComponent;
const KINDS = ["wikiwikibangbang"];

export async function viewContentSources({ api, toast, view, isSuperseded }) {
  let sources, secrets;
  try {
    [sources, secrets] = await Promise.all([
      api("GET", "/api/v1/content-sources"),
      // The vault's secret names, offered as suggestions for credentialsRef. A vault
      // that is not configured or not readable leaves the field a plain text box:
      // ATLAS_CONNECTOR_<REF>_TOKEN is a valid answer too.
      api("GET", "/api/v1/secrets").catch(() => []),
    ]);
  } catch (err) {
    if (isSuperseded()) return;
    view.replaceChildren(el("div", { class: "card empty" },
      el("h2", {}, t("contentSources.title")),
      el("p", { class: "muted" }, t("contentSources.loadFailed", { error: err.message }))));
    return;
  }
  if (isSuperseded()) return;
  sources = Array.isArray(sources) ? sources : [];
  const secretNames = (Array.isArray(secrets) ? secrets : []).map((s) => s && s.name).filter(Boolean);
  const reload = () => viewContentSources({ api, toast, view, isSuperseded });

  const panel = el("div", { class: "content-source-panel" });
  const report = el("div", { class: "content-source-report", "aria-live": "polite" });

  const openForm = (src) => {
    report.replaceChildren();
    panel.replaceChildren(sourceForm(src, secretNames, async (body, editing) => {
      try {
        if (editing) await api("PUT", `/api/v1/content-sources/${enc(editing)}`, body);
        else await api("POST", "/api/v1/content-sources", body);
        toast(t("contentSources.saved"));
        reload();
      } catch (err) {
        return err.message;
      }
      return "";
    }, () => panel.replaceChildren()));
    panel.scrollIntoView({ block: "nearest" });
  };

  const check = async (src, button) => {
    button.disabled = true;
    report.replaceChildren(el("p", { class: "muted" }, t("contentSources.checking")));
    try {
      const res = await api("POST", `/api/v1/content-sources/${enc(src.id)}/check`);
      report.replaceChildren(checkCard(src, res || {}));
    } catch (err) {
      report.replaceChildren(el("div", { class: "card content-failure", role: "alert" },
        el("b", {}, t("contentSources.checkFailed")), el("p", {}, err.message)));
    } finally { button.disabled = false; }
  };

  const remove = async (src) => {
    if (!window.confirm(t("contentSources.confirmDelete", { id: src.id }))) return;
    try {
      await api("DELETE", `/api/v1/content-sources/${enc(src.id)}`);
      toast(t("contentSources.deleted"));
      reload();
    } catch (err) {
      // 409 names the products still bound to the source: the list is what the
      // administrator has to take to whoever maintains them.
      report.replaceChildren(el("div", { class: "card content-failure", role: "alert" },
        el("b", {}, t("contentSources.deleteFailed")), el("p", {}, err.message),
        err.status === 409 ? el("p", { class: "muted" }, t("contentSources.deleteBound")) : null));
    }
  };

  const rows = sources.map((s) => {
    const bCheck = el("button", { type: "button", class: "btn ghost" }, t("contentSources.check"));
    bCheck.addEventListener("click", () => check(s, bCheck));
    const bEdit = el("button", { type: "button", class: "btn ghost" }, t("common.edit"));
    bEdit.addEventListener("click", () => openForm(s));
    const bDel = el("button", { type: "button", class: "btn ghost danger" }, t("common.delete"));
    bDel.addEventListener("click", () => remove(s));
    return el("tr", {},
      el("td", {}, el("b", {}, s.name || s.id), el("div", { class: "muted" }, s.id)),
      el("td", {}, s.kind || ""),
      el("td", {}, el("code", {}, s.baseURL || "")),
      el("td", {}, el("code", {}, s.credentialsRef || "")),
      el("td", {}, s.pictureField || "—"),
      el("td", {}, (s.excludedLabels || []).join(", ") || "—"),
      el("td", {}, s.linkInShop ? t("contentSources.yes") : t("contentSources.no")),
      el("td", { class: "muted" }, fmtNs(s.updatedAt)),
      el("td", { class: "row-actions" }, bCheck, bEdit, bDel));
  });

  const bNew = el("button", { type: "button", class: "btn" }, t("contentSources.new"));
  bNew.addEventListener("click", () => openForm(null));

  view.replaceChildren(
    el("div", { class: "row" }, el("h2", { style: "margin:0" }, t("contentSources.title")), bNew),
    el("p", { class: "muted", style: "max-width:72ch" }, t("contentSources.intro")),
    el("p", { class: "muted", style: "max-width:72ch" }, t("contentSources.account")),
    sources.length
      ? el("table", { class: "table content-sources" },
        el("thead", {}, el("tr", {},
          el("th", {}, t("contentSources.th.name")), el("th", {}, t("contentSources.th.kind")),
          el("th", {}, t("contentSources.th.baseURL")), el("th", {}, t("contentSources.th.credentials")),
          el("th", {}, t("contentSources.th.picture")), el("th", {}, t("contentSources.th.labels")),
          el("th", {}, t("contentSources.th.link")), el("th", {}, t("contentSources.th.updated")),
          el("th", {}))),
        el("tbody", {}, rows))
      : el("div", { class: "card empty" }, el("p", {}, t("contentSources.empty"))),
    report,
    panel);
}

// sourceForm is the create and edit form. save resolves to "" on success or to the
// server's refusal, which is shown under the form: an https rule or a taken id is
// something to correct here, not a toast that vanishes.
function sourceForm(src, secretNames, save, cancel) {
  const v = src || { kind: KINDS[0], excludedLabels: [] };
  const editing = src ? src.id : "";
  const listID = "content-source-secrets";
  const input = (name, value, attrs) => el("input", { name, value: value || "", autocomplete: "off", ...(attrs || {}) });
  const field = (label, control, hint) => el("label", { class: "field" },
    el("span", {}, label), control, hint ? el("span", { class: "muted small" }, hint) : null);
  const idIn = input("id", v.id, editing ? { readonly: true } : { required: true, pattern: "[a-z0-9][a-z0-9-]{0,62}", placeholder: "wiki" });
  const nameIn = input("name", v.name, { required: true, placeholder: "Wiki" });
  const kindSel = el("select", { name: "kind" }, KINDS.map((k) => el("option", { value: k, selected: k === v.kind }, k)));
  const baseIn = input("baseURL", v.baseURL, { type: "url", required: true, placeholder: "https://wiki.example.org" });
  const credIn = input("credentialsRef", v.credentialsRef, { required: true, list: listID, placeholder: "wiki_read_token" });
  const picIn = input("pictureField", v.pictureField, { placeholder: "pictures" });
  const labelsIn = input("excludedLabels", (v.excludedLabels || []).join(", "), { placeholder: "withdrawn, draft" });
  const linkIn = el("input", { type: "checkbox", name: "linkInShop", checked: !!v.linkInShop });
  const problem = el("div", { class: "content-failure", role: "alert", hidden: true });
  const form = el("form", { class: "content-source-form" },
    field(t("contentSources.field.id"), idIn, editing ? t("contentSources.field.idFixed") : t("contentSources.field.idHint")),
    field(t("contentSources.field.name"), nameIn),
    field(t("contentSources.field.kind"), kindSel),
    field(t("contentSources.field.baseURL"), baseIn, t("contentSources.field.baseURLHint")),
    field(t("contentSources.field.credentialsRef"), credIn, t("contentSources.field.credentialsHint")),
    el("datalist", { id: listID }, secretNames.map((n) => el("option", { value: n }))),
    field(t("contentSources.field.pictureField"), picIn, t("contentSources.field.pictureHint")),
    field(t("contentSources.field.excludedLabels"), labelsIn, t("contentSources.field.excludedHint")),
    el("label", { class: "field inline" }, linkIn, " ", t("contentSources.field.linkInShop")),
    el("p", { class: "muted small" }, t("contentSources.field.linkHint")),
    problem,
    el("div", { class: "row" },
      el("button", { type: "submit", class: "btn" }, t("common.save")),
      el("button", { type: "button", class: "btn neutral", onclick: cancel }, t("common.cancel"))));
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    problem.hidden = true;
    const body = {
      id: editing || idIn.value.trim(),
      name: nameIn.value.trim(),
      kind: kindSel.value,
      baseURL: baseIn.value.trim(),
      credentialsRef: credIn.value.trim(),
      pictureField: picIn.value.trim(),
      excludedLabels: labelsIn.value.split(",").map((x) => x.trim()).filter(Boolean),
      linkInShop: linkIn.checked,
    };
    const why = await save(body, editing);
    if (why) {
      problem.replaceChildren(el("b", {}, t("contentSources.saveFailed")), " ", why);
      problem.hidden = false;
    }
  });
  return el("div", { class: "card", style: "max-width:62ch; margin-top:14px" },
    el("h3", { style: "margin-top:0" }, editing ? t("contentSources.editTitle", { id: editing }) : t("contentSources.newTitle")),
    form);
}

// checkCard is the answer of the check: whether the source answered, and whether
// the account is nothing but a read-only viewer — with every problem the server
// found, since each one stops every fetch and quarantines every bound product.
export function checkCard(src, res) {
  const problems = res.problems || [];
  const spaces = res.spaces || [];
  const good = res.reachable && !problems.length;
  return el("div", { class: "card content-check", style: `margin-top:12px; border-color:var(${good ? "--ok" : "--danger"})` },
    el("b", {}, t("contentSources.check.title", { id: src.id })),
    el("ul", {},
      el("li", {}, res.reachable ? t("contentSources.check.reachable")
        : t("contentSources.check.unreachable", { error: res.error || "?" })),
      res.reachable ? el("li", {}, t("contentSources.check.scope", { scope: res.scope || "—" })) : null,
      res.reachable ? el("li", {}, spaces.length
        ? t("contentSources.check.spaces", { spaces: spaces.join(", ") })
        : t("contentSources.check.noSpaces")) : null),
    problems.length
      ? el("div", { class: "content-refusals", role: "alert" },
        el("b", {}, t("contentSources.check.problems")),
        el("ul", {}, problems.map((p) => el("li", {}, p))))
      : res.reachable ? el("p", { class: "content-passes" }, t("contentSources.check.ok")) : null);
}

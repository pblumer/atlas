// MFE2 / ADR-0514. A read-only, cause-first mobile view of the existing
// incident contracts. The server remains the authority for every GET; no
// incident command, acknowledgement or alternative engine path lives here.
const el = (tag, className, value) => {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (value !== undefined) node.textContent = String(value);
  return node;
};

export async function viewMobileIncidentTriage({ api, view, t, fmtRaised, canOperate, isSuperseded }) {
  const root = el("section", "mfe2-shell");
  root.id = "mfe2-triage";
  const head = el("div", "mfe2-header");
  const title = el("h1", "", t("mfe2.title"));
  title.id = "mfe2-title";
  const refresh = el("button", "btn ghost", t("mfe2.refresh"));
  refresh.type = "button";
  refresh.id = "mfe2-refresh";
  head.append(title, refresh);
  root.append(head, el("p", "muted mfe2-lead", t("mfe2.lead")));

  const status = el("p", "mfe2-state");
  status.id = "mfe2-status";
  status.setAttribute("role", "status");
  const truncation = el("p", "mfe2-warning");
  truncation.id = "mfe2-truncation";
  truncation.hidden = true;
  root.append(status, truncation);

  const layout = el("div", "mfe2-workspace");
  const causes = el("div", "mfe2-cause-pane");
  causes.setAttribute("aria-label", t("mfe2.causes"));
  causes.append(el("h2", "", t("mfe2.causes")));
  const list = el("div", "mfe2-causes");
  list.id = "mfe2-causes";
  list.setAttribute("role", "list");
  causes.append(list);
  const details = el("section", "mfe2-detail-pane");
  details.id = "mfe2-details";
  details.setAttribute("aria-label", t("mfe2.details"));
  layout.append(causes, details);
  root.append(layout);
  view.replaceChildren(root);

  const stillHere = () => !isSuperseded() && root.isConnected;
  const clear = () => {
    list.replaceChildren();
    details.replaceChildren();
    truncation.hidden = true;
    truncation.textContent = "";
  };
  const deny = () => {
    clear(); 
    status.textContent = t("mfe2.denied");
  };

  // No data is fetched for a role that cannot open the server's operator read.
  if (!canOperate) {
    refresh.hidden = true;
    deny();
    return;
  }

  let groups = [];
  let generation = 0;
  let selection = 0;

  const addField = (host, name, value) => {
    const row = el("div", "mfe2-field");
    row.append(el("dt", "", name), el("dd", "", value));
    host.append(row);
  };

  const displayGroup = async (i) => {
    const g = groups[i];
    if (!g || !stillHere()) return;
    const current = ++selection;
    for (const [n, btn] of [...list.querySelectorAll("button")].entries()) {
      btn.setAttribute("aria-pressed", String(n === i));
    }
    details.replaceChildren();
    const heading = el("h2", "", t("mfe2.details"));
    details.append(heading);
    const data = el("dl", "mfe2-fields");
    addField(data, t("mfe2.process"), g.processId || g.processDefKey || t("mfe2.undeployed"));
    addField(data, t("mfe2.element"), g.elementId || (Number.isInteger(g.elementIndex) ? "#" + g.elementIndex : "—"));
    addField(data, t("mfe2.kind"), g.type || "—");
    addField(data, t("mfe2.count"), Number.isFinite(g.count) ? g.count : 0);
    addField(data, t("mfe2.since"), fmtRaised(g.oldestRaisedAt));
    addField(data, t("mfe2.last"), fmtRaised(g.newestRaisedAt));
    addField(data, t("mfe2.message"), g.message || "—");
    details.append(data);
    if (g.messageVaries) details.append(el("p", "muted", t("mfe2.varies")));
    details.append(el("p", "mfe2-readonly", t("mfe2.readonly")));

    const samples = el("div", "mfe2-samples");
    samples.id = "mfe2-samples";
    details.append(samples);
    // A definition-less cause cannot be scoped safely: omitting its key would
    // accidentally return a much broader set of incidents. Use the group only.
    if (!g.processDefKey || (!g.elementId && !Number.isInteger(g.elementIndex))) {
      samples.append(el("p", "muted", t("mfe2.noScope")));
      return;
    }
    const q = new URLSearchParams({
      limit: "10", process: String(g.processDefKey), type: String(g.type || ""),
    });
    if (g.elementId) q.set("element", String(g.elementId));
    if (Number.isInteger(g.elementIndex)) q.set("elementIndex", String(g.elementIndex));
    samples.append(el("p", "muted", t("mfe2.loading")));
    try {
      const page = await api("GET", "/api/v1/incidents?" + q.toString());
      if (!stillHere() || current !== selection) return;
      samples.replaceChildren();
      if (!page || !Array.isArray(page.items)) throw new Error("invalid incident page");
      const rows = page.items.slice(0, 10);
      if (!rows.length) {
        samples.append(el("p", "muted", t("mfe2.noSamples")));
        return;
      }
      samples.append(el("h3", "", t("mfe2.examples")));
      const examples = el("ol", "mfe2-examples");
      for (const inc of rows) {
        const item = el("li");
        item.append(el("strong", "", String(inc.processInstanceKey || "—")));
        item.append(el("span", "", " · " + fmtRaised(inc.raisedAt)));
        item.append(el("p", "mfe2-incident-message", inc.message || "—"));
        examples.append(item);
      }
      samples.append(examples);
      if (page.truncated || (Number.isFinite(page.total) && page.total > rows.length)) {
        samples.append(el("p", "muted", t("mfe2.sampleLimit", { shown: rows.length })));
      }
    } catch (e) {
      if (!stillHere() || current !== selection) return;
      if (e.status === 401 || e.status === 403) {
        ++selection;
        deny(); // stale list/detail must not survive revoked authorization
      } else {
        samples.replaceChildren(el("p", "mfe2-warning", t("mfe2.detailError")));
      }
    }
  };

  const load = async () => {
    const current = ++generation;
    ++selection;
    clear();
    groups = [];
    status.textContent = t("mfe2.loading");
    try {
      const summary = await api("GET", "/api/v1/incidents/summary");
      if (!stillHere() || current !== generation) return;
      if (!summary || !Array.isArray(summary.groups)) throw new Error("invalid summary");
      groups = summary.groups;
      status.textContent = groups.length ? "" : t("mfe2.empty");
      if (summary.groupsTruncated || Number(summary.ungrouped) > 0) {
        truncation.hidden = false;
        truncation.textContent = t("mfe2.truncated", {
          missing: Number(summary.ungrouped) || 0, total: Number(summary.total) || 0,
        });
      }
      if (groups.length) details.append(el("p", "muted", t("mfe2.choose")));
      for (const [i, g] of groups.entries()) {
        const row = el("div");
        row.setAttribute("role", "listitem");
        const btn = el("button", "mfe2-cause");
        btn.type = "button";
        btn.setAttribute("aria-pressed", "false");
        btn.setAttribute("aria-label", t("mfe2.causeButton", {
          process: g.processId || g.processDefKey || t("mfe2.undeployed"),
          element: g.elementId || (Number.isInteger(g.elementIndex) ? "#" + g.elementIndex : "—"),
          count: Number(g.count) || 0,
        }));
        btn.append(el("strong", "", g.processId || g.processDefKey || t("mfe2.undeployed")));
        btn.append(el("span", "mfe2-cause-element", g.elementId || (Number.isInteger(g.elementIndex) ? "#" + g.elementIndex : "—")));
        btn.append(el("span", "mfe2-cause-meta", (g.type || "—") + " · " + (Number(g.count) || 0)));
        btn.addEventListener("click", () => displayGroup(i));
        row.append(btn);
        list.append(row);
      }
    } catch (e) {
      if (!stillHere() || current !== generation) return;
      groups = [];
      clear();
      status.textContent = e.status === 401 || e.status === 403
        ? t("mfe2.denied") : t("mfe2.summaryError");
    }
  };

  refresh.addEventListener("click", load);
  await load();
}

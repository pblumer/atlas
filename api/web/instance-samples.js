// Live values for the Modeler: what the variables of a process actually hold in its
// instances (ADR-0145). The Developer View shows them beside each name, prefills its
// Test panel from them and offers the instances to choose from, including a search;
// the Variables panel shows the same values.
//
// It lives apart from editor.js because it is a function of what the server answers
// and nothing else, so it can be driven against a mock `api` in a browser test
// without standing up the Modeler.

// How many instances the picker offers. Enough to find the one that took the branch
// being written about, few enough that the <select> stays usable. A search is cut to
// the same number and says so.
export const MAX_SAMPLE_INSTANCES = 50;

// How many versions are read at once. Every version is its own request, because both
// the instance list and the search read a definition's own index; a process with many
// versions should not open dozens of requests in one burst.
const VERSION_CONCURRENCY = 4;

// How many searches are remembered per session. Each is kept so that picking between
// its hits is a re-render, not a request; old ones are not worth holding.
const MAX_REMEMBERED_SEARCHES = 20;

// listCache memoizes the default listing per process id for this editing session:
// opening the Developer View on ten fields of one diagram reads the instances once,
// and switching between them costs nothing. searchCache does the same per query.
// A forced read (the Reload button) drops both, which is how an author picks up an
// instance they started while the Modeler was open.
const listCache = new Map(); // processId -> Promise<row[]>
const searchCache = new Map(); // processId + "\n" + query -> Promise<{ rows, truncated }>

// lookupSamples answers the Developer View's `samples` call for one process id:
// the instance whose values are shown, those values, and the instances the picker
// offers. Without a query those are the newest instances across every deployed
// version, running ones first; with one, the instances the server's instance search
// finds for it in any version — an instance key, name=value, or a term with * and ?,
// the same syntax as the Operations view. `instanceKey` picks one of them, `force`
// reads again.
//
// Returns null when the process was never deployed or has never run, which the
// modal treats as "no samples", not as an error. A query that matches nothing
// answers with no instance and an empty list, so the caller can tell that apart.
export async function lookupSamples(api, processId, opts = {}) {
  if (!processId || !api) return null;
  const query = String(opts.query || "").trim();
  if (opts.force) forget(processId);

  const { rows, truncated } = query
    ? await remember(searchCache, processId + "\n" + query, () => searchSamples(api, processId, query))
    : { rows: await remember(listCache, processId, () => listSamples(api, processId)), truncated: false };
  if (!rows.length) return query ? { instance: null, values: {}, instances: [], query, truncated: false } : null;

  const chosen = rows.find((r) => r.key === opts.instanceKey) || rows[0];
  return {
    instance: meta(chosen),
    values: await valuesOfRow(api, chosen),
    // The picker only needs each instance's identity; the values stay on the cached
    // rows so switching is a re-render, not a fetch.
    instances: rows.map(meta),
    query,
    truncated,
  };
}

// forget drops everything remembered for one process.
function forget(processId) {
  listCache.delete(processId);
  for (const key of [...searchCache.keys()]) {
    if (key.startsWith(processId + "\n")) searchCache.delete(key);
  }
}

// remember memoizes one load. A load that fails is not remembered: the next call asks
// again rather than repeating a stale error for the rest of the session.
function remember(cache, key, load) {
  if (!cache.has(key)) {
    const p = load();
    cache.set(key, p);
    p.catch(() => { if (cache.get(key) === p) cache.delete(key); });
    if (cache === searchCache && cache.size > MAX_REMEMBERED_SEARCHES) {
      cache.delete(cache.keys().next().value);
    }
  }
  return cache.get(key);
}

// listSamples reads the newest instances of every deployed version of the process.
// GET /instances?process= already returns each instance's root-scope variables, so
// this is one request per version and no per-variable round trip. What comes back is
// a page of each version's instances, and a page is all this wants: the result is a
// *sample* of what the variables look like, cut to MAX_SAMPLE_INSTANCES, and nothing
// here counts it or reports a total (ADR-0365).
async function listSamples(api, processId) {
  const versions = await versionsOf(api, processId);
  const pages = await eachVersion(versions, async (v) => {
    // One version that cannot be read leaves the others offered.
    try { return await api("GET", `/api/v1/instances?process=${encodeURIComponent(v.key)}&limit=${MAX_SAMPLE_INSTANCES}`); }
    catch { return null; }
  });
  const rows = [];
  pages.forEach((page, i) => {
    for (const r of (page && page.items) || []) {
      if (r.processDefKey === versions[i].key) rows.push(sampleRow(r, true));
    }
  });
  return newestFirst(rows).slice(0, MAX_SAMPLE_INSTANCES);
}

// searchSamples asks the server's instance search, once per version: scoped to a
// definition the search reads that definition's indexes rather than every instance
// on the server, and the value index answers a name the version declares searchable.
// A failure is reported rather than read as "nothing matches".
//
// A content search answers each instance with only the variables that matched, so
// those rows carry no values yet; the one the author picks is read whole then. An
// instance-key search answers with the whole variable set already.
async function searchSamples(api, processId, query) {
  const versions = await versionsOf(api, processId);
  const pages = await eachVersion(versions, (v) =>
    api("GET", `/api/v1/instances/search?process=${encodeURIComponent(v.key)}&q=${encodeURIComponent(query)}`));
  let truncated = false;
  const rows = [];
  pages.forEach((page, i) => {
    if (page && page.truncated) truncated = true;
    for (const r of (page && page.items) || []) {
      // An archived row describes an instance history retention already removed
      // (ADR-0115): there is nothing left here to read its values from.
      if (r.archived || r.processDefKey !== versions[i].key) continue;
      rows.push(sampleRow(r, String(r.key) === query));
    }
  });
  const sorted = newestFirst(rows);
  if (sorted.length > MAX_SAMPLE_INSTANCES) truncated = true;
  return { rows: sorted.slice(0, MAX_SAMPLE_INSTANCES), truncated };
}

// versionsOf resolves the diagram's process id to its deployed versions, newest first.
async function versionsOf(api, processId) {
  const procs = await api("GET", "/api/v1/processes");
  return (procs || [])
    .filter((p) => p.processId === processId)
    .sort((a, b) => b.version - a.version);
}

// eachVersion runs fn over the versions, VERSION_CONCURRENCY at a time, and keeps
// the answers in version order.
async function eachVersion(versions, fn) {
  const out = new Array(versions.length);
  let next = 0;
  const worker = async () => {
    while (next < versions.length) {
      const i = next++;
      out[i] = await fn(versions[i]);
    }
  };
  await Promise.all(Array.from({ length: Math.min(VERSION_CONCURRENCY, versions.length) }, worker));
  return out;
}

// valuesOfRow returns a row's whole variable set, reading it once when the row came
// from a content search. The read is an instance-key search scoped to the row's own
// version: a point read that answers with every root-scope variable, in the same
// shape (and with the same masking of personal data) as the listing.
async function valuesOfRow(api, row) {
  if (row.values) return row.values;
  if (!row.reading) {
    row.reading = api("GET", `/api/v1/instances/search?process=${encodeURIComponent(row.processDefKey)}&q=${encodeURIComponent(String(row.key))}`)
      .then((page) => {
        const hit = ((page && page.items) || []).find((r) => r.key === row.key);
        if (!hit) throw new Error(`instance ${row.key} can no longer be read`);
        row.values = valuesOf(hit.variables);
        return row.values;
      })
      .finally(() => { row.reading = null; });
  }
  return row.reading;
}

// sampleRow is one instance as this module keeps it. `whole` says the server's row
// carries every root-scope variable; otherwise it carries the ones a search matched,
// and the first of them names the hit in the picker.
function sampleRow(r, whole) {
  const row = {
    key: r.key, state: r.state, version: r.version, createdAt: r.createdAt,
    processDefKey: r.processDefKey, values: whole ? valuesOf(r.variables) : null,
  };
  if (!whole) row.match = matchLabel(r.variables);
  return row;
}

function valuesOf(variables) {
  const values = {};
  for (const v of variables || []) {
    if (v && v.name) values[v.name] = { value: v.value, kind: v.kind };
  }
  return values;
}

// matchLabel is what a search hit matched, short enough for a picker option.
function matchLabel(variables) {
  const v = (variables || [])[0];
  if (!v) return "";
  const text = `${v.name}=${v.value}`.replace(/\s+/g, " ");
  return text.length > 40 ? text.slice(0, 39) + "…" : text;
}

// meta is what the picker needs of an instance.
function meta(r) {
  const m = { key: r.key, state: r.state, version: r.version, createdAt: r.createdAt };
  if (r.match) m.match = r.match;
  return m;
}

// newestFirst orders instances the way the picker offers them: running ones first
// (their values are the live ones), then the newest. Across versions the keys are
// not a clock — a key carries its partition in its high bits — so the start time
// orders them, and the key only breaks a tie.
function newestFirst(rows) {
  return rows.sort((a, b) => {
    if (a.state !== b.state && (a.state === "active" || b.state === "active")) return a.state === "active" ? -1 : 1;
    return (b.createdAt || 0) - (a.createdAt || 0) || b.key - a.key;
  });
}

// sampleFor finds the instance's value behind a listed name. A dotted name the instance
// does not hold as such is a path into a JSON variable: the constant namespaces are
// listed member by member (application.TENANT_ID) but held as one object (ADR-0456),
// and a nested output mapping target reads the same way. The longest holding prefix
// wins; a path the object does not hold yields nothing rather than a wrong value.
const parsedSamples = new WeakMap(); // sample -> its parsed JSON, so a re-render does not re-parse
export function sampleFor(values, name) {
  if (!values) return null;
  if (values[name]) return values[name];
  const parts = String(name).split(".");
  for (let i = parts.length - 1; i > 0; i--) {
    const root = values[parts.slice(0, i).join(".")];
    if (!root) continue;
    if (root.kind !== "json") return null;
    if (!parsedSamples.has(root)) {
      let v; try { v = JSON.parse(root.value); } catch { v = undefined; }
      parsedSamples.set(root, v);
    }
    let v = parsedSamples.get(root);
    for (const key of parts.slice(i)) {
      if (!v || typeof v !== "object" || Array.isArray(v) || !Object.hasOwn(v, key)) return null;
      v = v[key];
    }
    return typedSample(v);
  }
  return null;
}

// typedSample is a JSON value in the shape the server reports a variable in.
function typedSample(v) {
  if (v === null || v === undefined) return { value: "null", kind: "null" };
  if (typeof v === "string") return { value: v, kind: "string" };
  if (typeof v === "number") return { value: String(v), kind: "number" };
  if (typeof v === "boolean") return { value: String(v), kind: "boolean" };
  return { value: JSON.stringify(v), kind: "json" };
}

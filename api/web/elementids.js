// Readable ids for new BPMN elements: <type>_<yyyymmdd>_<researcher>, for example
// activity_20261023_heisenberg (ADR-0474).
//
// bpmn-js names a new element Activity_0jmndkk, and the Modeler named a new diagram's
// process Process_<timestamp+random>. Those ids are what the canvas, the properties
// panel and the breadcrumb show until somebody names the element, and nobody can say
// one aloud or tell two of them apart at a glance. A type, the day it was drawn and a
// researcher's name can be — and still say what the element is.
//
// A shorter id gives up the near-certainty a random one had, so uniqueness is checked
// rather than hoped for:
//
//   - Within a diagram, every candidate is tested against the moddle's id registry
//     (which bpmn-js fills on every import and keeps current through renames) and the
//     element registry, and a claimed id is never handed out again. When every
//     researcher is taken for a type on a day, a counter follows the name
//     (heisenberg2, heisenberg3, ...), so the supply never runs out.
//   - A process id is the identity deployments are grouped by, so it must also be free
//     across the installation: the ids of saved drafts and deployed processes are
//     loaded when the Modeler opens, and a new diagram's process id is in addition
//     asked of the server's availability check, which also sees drafts the author may
//     not. A draft save that still collides is refused by the server (ADR-0222), which
//     stays the authority between two authors who pick the same name in the same
//     moment.

// RESEARCHERS is the pool the name segment is drawn from: lowercase ASCII letters only,
// so every id is a valid XML name and reads the same in every tool (Schrödinger is
// schroedinger, al-Khwarizmi is alkhwarizmi).
export const RESEARCHERS = Object.freeze([
  "einstein", "curie", "heisenberg", "noether", "turing", "lovelace", "newton", "galilei",
  "kepler", "copernicus", "darwin", "mendel", "pasteur", "faraday", "maxwell", "planck",
  "bohr", "schroedinger", "pauli", "dirac", "fermi", "feynman", "hopper", "hawking",
  "meitner", "franklin", "hodgkin", "mcclintock", "carson", "hypatia", "euler", "gauss",
  "riemann", "hilbert", "cantor", "goedel", "leibniz", "pascal", "fourier", "laplace",
  "lagrange", "poincare", "ramanujan", "nash", "shannon", "neumann", "boltzmann", "hertz",
  "ohm", "volta", "ampere", "tesla", "roentgen", "becquerel", "rutherford", "chadwick",
  "hubble", "lemaitre", "sagan", "kelvin", "joule", "celsius", "avogadro", "mendeleev",
  "lavoisier", "dalton", "boyle", "hooke", "huygens", "leeuwenhoek", "linnaeus", "humboldt",
  "wegener", "lyell", "cuvier", "lamarck", "wallace", "watson", "crick", "sanger",
  "mullis", "doudna", "charpentier", "montalcini", "elion", "yalow", "cori", "lederberg",
  "salk", "sabin", "fleming", "koch", "ehrlich", "jenner", "lister", "semmelweis",
  "nightingale", "harvey", "vesalius", "avicenna", "alhazen", "alkhwarizmi", "aryabhata",
  "brahmagupta", "archimedes", "euclid", "pythagoras", "thales", "eratosthenes", "ptolemy",
  "aristarchus", "brahe", "cassini", "herschel", "leavitt", "payne", "rubin", "burnell",
  "wu", "goeppert", "germain", "kovalevskaya", "mirzakhani", "agnesi", "somerville",
  "babbage", "boole", "zuse", "dijkstra", "knuth", "hamming", "wiener", "kolmogorov",
  "markov", "chebyshev", "lobachevsky", "bernoulli", "fibonacci", "descartes", "fermat",
  "napier", "cauchy", "galois", "abel", "jacobi", "dedekind", "weierstrass", "minkowski",
  "lorentz", "sommerfeld", "hahn", "fraunhofer", "bunsen", "kirchhoff", "helmholtz",
  "clausius", "carnot", "mach", "doppler", "piccard", "zwicky", "paracelsus", "haller",
  "agassiz", "saussure", "kocher", "ruzicka", "prelog", "wuethrich", "rohrer", "mayor",
  "queloz", "dubochet",
]);

// idDate is the date segment: the author's local calendar day as yyyymmdd, the day the
// element was drawn as the author would say it.
export function idDate(date) {
  const d = date instanceof Date ? date : new Date();
  const p = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}`;
}

// idType turns the prefix bpmn-js asks for ("Activity_", "DataObjectReference_") into
// the type segment: lowercase letters and digits only, so the underscore stays the
// separator between the three segments.
export function idType(prefix) {
  const t = String(prefix || "").replace(/[^A-Za-z0-9]/g, "").toLowerCase();
  return t || "element";
}

// shuffled returns the indexes 0..n-1 in a random order (Fisher-Yates), so names are
// drawn without repetition until the pool is used up.
function shuffled(n, random) {
  const order = Array.from({ length: n }, (_, i) => i);
  for (let i = n - 1; i > 0; i--) {
    const j = Math.floor(random() * (i + 1));
    [order[i], order[j]] = [order[j], order[i]];
  }
  return order;
}

// readableId mints <type>_<yyyymmdd>_<name> that taken() does not hold. Every name is
// tried once in random order; when all are taken, a counter follows the name, so the
// loop always ends: taken() can only ever hold finitely many ids.
export function readableId(type, { taken = () => false, now = () => new Date(), random = Math.random, names = RESEARCHERS } = {}) {
  const stem = `${idType(type)}_${idDate(now())}_`;
  const order = shuffled(names.length, random);
  for (const i of order) {
    const id = stem + names[i];
    if (!taken(id)) return id;
  }
  for (let n = 2; ; n++) {
    for (const i of order) {
      const id = stem + names[i] + n;
      if (!taken(id)) return id;
    }
  }
}

// isReadablePrefix says which of bpmn-js's requests get a readable id. The diagram
// interchange (bpmndi:BPMNShape, BPMNEdge, BPMNPlane, BPMNDiagram) is never shown to an
// author, and bpmn-js derives most of it from the element's own id anyway.
function isReadablePrefix(prefix) {
  return !/^BPMN/.test(String(prefix || ""));
}

// installReadableIds makes a bpmn-js modeler hand out readable ids for every element it
// creates from now on: tasks, events, gateways, flows, pools and their processes,
// lanes, data objects, annotations, messages, signals, errors, escalations. It replaces
// nextPrefixed on the moddle's id registry, the one door bpmn-js mints ids through, so
// it reaches the palette, the context pad, replace, copy-paste and Atlas's own code
// alike. Ids already in a diagram are never touched.
//
//   processTaken(id) — whether a process id is held elsewhere on the installation.
export function installReadableIds(modeler, { processTaken = () => false, now, random } = {}) {
  const moddle = modeler && modeler.get("moddle");
  const ids = moddle && moddle.ids;
  if (!ids || typeof ids.nextPrefixed !== "function" || ids.__readable) return;
  let registry = null;
  try { registry = modeler.get("elementRegistry"); } catch { /* a viewer without one */ }
  const original = ids.nextPrefixed.bind(ids);
  ids.nextPrefixed = function nextPrefixed(prefix, element) {
    if (!isReadablePrefix(prefix)) return original(prefix, element);
    const type = idType(prefix);
    const id = readableId(type, {
      now,
      random,
      taken: (c) => !!ids.assigned(c) || !!(registry && registry.get(c)) ||
        (type === "process" && !!processTaken(c)),
    });
    ids.claim(id, element || true);
    return id;
  };
  ids.__readable = true;
}

// takenProcessIds loads the process ids the installation already holds: every saved
// draft the caller can see and every deployed process. A listing that fails costs
// only this extra check, never the Modeler; the server's own checks remain.
export async function takenProcessIds(api) {
  const out = new Set();
  const add = (list, field) => {
    if (!Array.isArray(list)) return;
    for (const item of list) {
      const id = item && item[field];
      if (typeof id === "string" && id) out.add(id);
    }
  };
  // Through a promise, so an api that throws or answers synchronously is caught too.
  const get = (url) => Promise.resolve().then(() => api("GET", url)).catch(() => null);
  const [drafts, processes] = await Promise.all([get("/api/v1/drafts"), get("/api/v1/processes")]);
  add(drafts, "processId");
  add(processes, "processId");
  return out;
}

// freshProcessId mints the process id for a new diagram. Besides the ids loaded up
// front it asks the server's availability check, which also counts drafts the author
// cannot see. An id the check reports taken is remembered and another one drawn; a
// check that cannot be reached is not a reason to refuse the diagram, because the
// draft save refuses a collision either way.
export async function freshProcessId(api, { taken = new Set(), now, random, attempts = 20 } = {}) {
  let id = "";
  for (let i = 0; i < attempts; i++) {
    id = readableId("process", { taken: (c) => taken.has(c), now, random });
    let res;
    try {
      res = await api("GET", `/api/v1/drafts/${encodeURIComponent(id)}/availability`);
    } catch {
      return id;
    }
    if (!res || res.available !== false) return id;
    taken.add(id);
  }
  return id;
}

// Build a presentation-only index over the persisted replay facts. A loop body and
// a round share the BPMN element id; their durable token lineage identifies which
// exact body execution owns the round (ADR-0077/0065). Never group by element name:
// the same task can be revisited, nested, or active on several branches.
//
// A second level nests what runs *inside* an iteration under it. Token lineage cannot
// say that for a multi-instance subprocess — the engine starts each iteration's inner
// start event with a fresh token — so the server says it per step instead: inIteration
// is the element instance key of the iteration the step ran inside (the outline's
// owner, ADR-0517). Without it (older history) the inner steps stay top-level rows.
//
// This is deliberately not the scale solution: the current timeline API still
// materializes every step and frame. A paged read API is the next delivery slice.
// Returns { children, inner, parents }: a body's iterations, an iteration's inner
// steps, and every grouped step's one parent (a body or an iteration).
export function groupReplayHistory(steps) {
  const bodyByToken = new Map(); // element + body token -> most recent body activation
  const children = new Map();    // body step index -> iteration step indices
  const inner = new Map();       // iteration step index -> indices of the steps inside it
  const parents = new Map();     // grouped step index -> body or iteration step index

  // Element ids and token ids are independently unambiguous within a replay;
  // JSON.stringify prevents accidental collisions between concatenated strings.
  const keyOf = (elementId, tokenId) => JSON.stringify([elementId, String(tokenId)]);
  for (let i = 0; i < steps.length; i++) {
    const s = steps[i];
    if (s?.loop?.kind !== "multi-instance" || s.action === "migrate" || s.action === "fork") continue;
    if (!s.iteration) {
      if (s.tokenId === undefined || s.tokenId === null) continue;
      bodyByToken.set(keyOf(s.elementId, s.tokenId), i);
      continue;
    }
    if (s.parentTokenId === undefined || s.parentTokenId === null || s.parentTokenId === 0 || s.parentTokenId === "0") {
      continue; // Old history has no proven parent: show it as an ordinary row.
    }
    const bodyIndex = bodyByToken.get(keyOf(s.elementId, s.parentTokenId));
    if (bodyIndex === undefined) continue;
    if (!children.has(bodyIndex)) children.set(bodyIndex, []);
    children.get(bodyIndex).push(i);
    parents.set(i, bodyIndex);
  }
  const byKey = new Map(); // element instance key -> its step
  for (let i = 0; i < steps.length; i++) {
    const s = steps[i];
    if (s && s.elementInstanceKey && s.action !== "migrate" && s.action !== "fork") byKey.set(String(s.elementInstanceKey), i);
  }
  for (let i = 0; i < steps.length; i++) {
    const s = steps[i];
    if (!s || !s.inIteration || parents.has(i)) continue;
    const owner = byKey.get(String(s.inIteration));
    if (owner === undefined || owner === i) continue;
    if (!inner.has(owner)) inner.set(owner, []);
    inner.get(owner).push(i);
    parents.set(i, owner);
  }
  return { children, inner, parents };
}

// historyAncestors lists a step's groups from the nearest outwards: the iteration it
// ran in, that iteration's body, the iteration that body ran in, and so on.
export function historyAncestors(groups, i) {
  const out = [];
  const seen = new Set([i]);
  for (let p = groups.parents.get(i); p !== undefined && !seen.has(p); p = groups.parents.get(p)) {
    seen.add(p);
    out.push(p);
  }
  return out;
}

// historyRows lays the grouped steps out as the rows the history shows, in tree order:
// every top-level step in the order it happened, and under an expanded group its
// members, indented one level deeper — a body's iterations, then under an expanded
// iteration the steps it ran. A parallel subprocess's iterations run interleaved, so
// the order of the log would scatter one iteration's steps between the others'; the
// tree keeps each iteration's steps together under it. A collapsed group's members are
// not rows at all, so a body of several hundred iterations costs one row until opened.
// Returns [{ i, depth }].
export function historyRows(count, groups, expanded) {
  const rows = [];
  const visit = (i, depth) => {
    rows.push({ i, depth });
    if (!expanded.has(i)) return;
    for (const c of groups.children.get(i) || []) visit(c, depth + 1);
    for (const c of groups.inner.get(i) || []) visit(c, depth + 1);
  };
  for (let i = 0; i < count; i++) if (!groups.parents.has(i)) visit(i, 0);
  return rows;
}

// historyDescendants lists every step grouped under i, at any depth.
export function historyDescendants(groups, i) {
  const out = [];
  const stack = [...(groups.children.get(i) || []), ...(groups.inner.get(i) || [])];
  while (stack.length) {
    const n = stack.pop();
    out.push(n);
    stack.push(...(groups.children.get(n) || []), ...(groups.inner.get(n) || []));
  }
  return out;
}

// foldLegendTokens groups the tokens of one frame for the chip row under the replay's
// diagram. A multi-instance activity holds its body's token and one token per running
// iteration, all on the same shape: listing each as its own chip names the same task
// two hundred times and pushes the diagram off the screen, while the history row of
// the body already counts the iterations and opens them. So every multi-instance
// element holding more than one token becomes one entry carrying its tokens, at the
// place of its first token; everything else — a join's arrivals, a race, a standard
// loop's body and round — stays one chip per token, in the order it arrived.
// Entries are { token } or { elementId, tokens }.
export function foldLegendTokens(tokens, isMultiInstance) {
  const entries = [];
  const groups = new Map();
  for (const token of tokens) {
    if (!isMultiInstance(token.elementId)) {
      entries.push({ token });
      continue;
    }
    let group = groups.get(token.elementId);
    if (!group) {
      group = { elementId: token.elementId, tokens: [] };
      groups.set(token.elementId, group);
      entries.push(group);
    }
    group.tokens.push(token);
  }
  return entries.map((e) => (e.tokens && e.tokens.length === 1 ? { token: e.tokens[0] } : e));
}

// multiInstanceScope says which multi-instance activity a token's element answers to in
// the chip row, or null when it answers to none. An element that is itself
// multi-instance is its own scope. An element inside a multi-instance subprocess answers
// to the nearest enclosing one: every running iteration of that subprocess walks its own
// token through the same inner shapes, so twelve iterations parked on one inner task are
// twelve tokens on that task — the same repetition foldLegendTokens folds on the activity
// itself, one level down. describe(id) returns { mode, parentId } for an element of the
// diagram (mode "parallel" or "sequential" for a multi-instance one, "" otherwise) or
// null for an id the diagram does not know. Returns { scopeId, mode, inside }.
export function multiInstanceScope(elementId, describe) {
  const own = describe(elementId);
  if (!own) return null;
  if (own.mode) return { scopeId: elementId, mode: own.mode, inside: false };
  const seen = new Set([elementId]);
  for (let id = own.parentId; id && !seen.has(id);) {
    seen.add(id);
    const up = describe(id);
    if (!up) return null;
    if (up.mode) return { scopeId: id, mode: up.mode, inside: true };
    id = up.parentId;
  }
  return null;
}

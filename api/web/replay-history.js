// Build a presentation-only index over the persisted replay facts. A loop body and
// a round share the BPMN element id; their durable token lineage identifies which
// exact body execution owns the round (ADR-0077/0065). Never group by element name:
// the same task can be revisited, nested, or active on several branches.
//
// This is deliberately not the scale solution: the current timeline API still
// materializes every step and frame. A paged read API is the next delivery slice.
export function groupReplayHistory(steps) {
  const bodyByToken = new Map(); // element + body token -> most recent body activation
  const children = new Map();    // body step index -> iteration step indices
  const parents = new Map();     // iteration step index -> body step index

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
  return { children, parents };
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

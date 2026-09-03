// Saved landscape views (ADR-0211 §7).
//
// Reading a landscape is not a single act. Somebody watching one node filters down
// to it, zooms in, arranges what is around it — and then has to do all of it again
// tomorrow, because a reload puts them back at the whole landscape. A saved view is
// that setup, given a name.
//
// It lives in this browser's own storage, which is where every other remembered
// piece of UI state in Atlas lives (the task list's sort, the reference panel's
// width, a collapsed section). That is a real limit and the UI says so rather than
// letting it look like a server-side feature: these views are not shared with
// anyone and do not follow you to another machine. The alternative — a stored
// resource with an owner, an access rule and a sharing scope — is a decision about
// the product, not a way of not re-zooming, and this is the second thing.
//
// Everything here is a function over plain data so it can be checked as arithmetic:
// what a view captures, and where it puts you when you open it, are the two things
// that can be wrong in a way a screenshot would not show.

export const VIEWS_KEY = "atlas.panorama.views";
export const VIEWS_VERSION = 1;
// A bound, because storage is shared with the rest of the app and a list nobody can
// read is not a feature. Reaching it refuses the save and says so — dropping the
// oldest silently would throw away something somebody deliberately kept.
export const MAX_VIEWS = 24;
export const MAX_NAME = 60;

// readViews returns the saved views, and an empty list for anything it does not
// recognise.
//
// A malformed body reads as *absent*, never as an empty object with usable-looking
// properties: half-understood storage is how a view opens onto a filter nobody
// saved. The version is checked rather than assumed for the same reason — a future
// shape is not this shape.
export function readViews(storage) {
  let raw;
  try { raw = storage?.getItem(VIEWS_KEY); } catch { return []; }
  if (!raw) return [];
  let parsed;
  try { parsed = JSON.parse(raw); } catch { return []; }
  if (!parsed || parsed.version !== VIEWS_VERSION || !Array.isArray(parsed.views)) return [];
  return parsed.views.filter((v) => v && typeof v.id === "string" && typeof v.name === "string");
}

// writeViews stores the list, and reports whether the browser actually took it.
// Storage can be full, or off (private mode, an embedded frame), and a save that
// quietly evaporated is worse than one that was refused out loud.
export function writeViews(storage, views) {
  try {
    storage.setItem(VIEWS_KEY, JSON.stringify({ version: VIEWS_VERSION, views }));
    return true;
  } catch {
    return false;
  }
}

// sameName is how a save decides between replacing and adding. Case and surrounding
// space are not what somebody means by a different view.
const sameName = (a, b) => a.trim().toLowerCase() === b.trim().toLowerCase();

// saveView adds a view, or replaces the one that already carries its name.
//
// Replacing rather than duplicating is the point: "Billing watch" is a thing
// somebody keeps up to date, and two entries with one name is a list you have to
// read twice to use once. Returns { views, error } — the error is a sentence for the
// reader, not a code, because the only two failures are "name it" and "you are full".
export function saveView(views, view) {
  if (!view.name.trim()) return { views, error: "Give the view a name first." };
  const at = views.findIndex((v) => sameName(v.name, view.name));
  if (at >= 0) {
    const next = views.slice();
    next[at] = { ...view, id: views[at].id };
    return { views: next };
  }
  if (views.length >= MAX_VIEWS) {
    return {
      views,
      error: `You have ${MAX_VIEWS} saved views, which is the limit. Remove one to save another.`,
    };
  }
  return { views: [...views, view] };
}

// removeView forgets one. Unknown ids are not an error: the list is the only record,
// so an id it does not carry is already forgotten.
export function removeView(views, id) {
  return views.filter((v) => v.id !== id);
}

// captureView records what is on screen as data that survives the picture changing.
//
// Two things are stored as *fractions of the world* rather than as coordinates: how
// far in the view is zoomed, and where it is centred. The world is sized from the
// graph and the shape of the window, so a coordinate captured on one screen means
// somewhere else on another — and a saved view that reopened on empty space would be
// worse than no saved view. The pins go the same way, for the same reason.
export function captureView({ name, term, direction, depth, notation, selected, frameView, world, pinned, at, id }) {
  const width = Math.max(world?.width || 0, 1), height = Math.max(world?.height || 0, 1);
  const zoom = frameView ? Math.min(Math.max(frameView.w / width, 0), 1) : 1;
  const centre = frameView
    ? { fx: (frameView.x + frameView.w / 2) / width, fy: (frameView.y + frameView.h / 2) / height }
    : { fx: 0.5, fy: 0.5 };
  return {
    id: id || `view-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`,
    name: name.trim().slice(0, MAX_NAME),
    at: at ?? Date.now(),
    term: term || "",
    direction: direction || "dependents",
    depth: depth ?? "2",
    // The vocabulary the picture was read in. A view is the whole question somebody
    // saved, and reopening a C4 landscape as an Atlas one answers a different one —
    // the same reason the filter and the depth are stored rather than left as
    // whatever the page happened to be showing.
    notation: notation || "atlas",
    selected: selected || null,
    zoom,
    centre,
    pins: [...(pinned || [])].map(([nodeId, p]) => [nodeId, p.x / width, p.y / height]),
  };
}

// frameFor is where opening a view puts you. null means the whole landscape, which is
// what a view saved un-zoomed asked for.
//
// The anchor is the correction that matters. A saved view is nearly always somebody
// watching one node, and the landscape it sits in is derived — it changes as things
// are deployed, so the coordinates that framed that node last week frame empty space
// today. So when the view had a node selected and that node is still here, the frame
// is centred on wherever it is *now*, at the magnification that was saved. The stored
// centre is the fallback for a view that was watching a region rather than a thing.
export function frameFor(view, world, positionOf) {
  const zoom = Math.min(Math.max(view?.zoom ?? 1, 0), 1);
  if (!(zoom > 0 && zoom < 1)) return null;
  const w = world.width * zoom, h = world.height * zoom;
  const anchor = view.selected && positionOf ? positionOf(view.selected) : null;
  const cx = anchor ? anchor.x : (view.centre?.fx ?? 0.5) * world.width;
  const cy = anchor ? anchor.y : (view.centre?.fy ?? 0.5) * world.height;
  return { x: cx - w / 2, y: cy - h / 2, w, h };
}

// pinsFor turns a view's stored arrangement back into world coordinates.
export function pinsFor(view, world) {
  const pins = new Map();
  for (const entry of view?.pins || []) {
    if (!Array.isArray(entry) || entry.length < 3) continue;
    const [id, fx, fy] = entry;
    if (typeof id !== "string" || !Number.isFinite(fx) || !Number.isFinite(fy)) continue;
    pins.set(id, { x: fx * world.width, y: fy * world.height });
  }
  return pins;
}

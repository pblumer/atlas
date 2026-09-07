// The Atlas canvas bundle: both diagram-js canvases Atlas draws with, in one file
// carrying one copy of the library (ADR-0189, ADR-0237).
//
// The two were vendored separately, a bundle each, because the second one arrived
// later and merging them first would have meant touching Panorama's shipped canvas
// for a saving that was real but not urgent. ADR-0237 named this merge as the
// follow-up rather than pretending the duplication was free; this is it.
//
// What they share is diagram-js and nothing else. Each canvas keeps its own
// renderer, its own rules and its own idea of what a document is — an ArchiMate view
// is read from a server that owns it, an information model is a working copy with an
// explicit Save — so they are namespaced rather than flattened. `Viewer` and
// `ClassCanvas` in one namespace would read as two kinds of the same thing, and they
// are not.
//
// One bundle also means one page load carries both renderers, where before each
// carried only its own. That is a few kilobytes more for somebody who opens only one
// of the two canvases, against one copy of the library instead of two for anybody who
// opens both, and one cache entry rather than two. The measurement is in
// ATLAS-VENDORED.txt beside this file.

export * as archimate from "./archimate.js";
export * as uml from "./uml.js";

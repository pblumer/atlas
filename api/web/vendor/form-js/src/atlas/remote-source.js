// A select's options can come from a remote data source (ADR-0473): a fourth options
// source beside form-js's own Static, Input data and Expression.
//
// form-js describes its options sources in four exported tables — the sources, their
// labels, the property each one lives in, and the value a newly chosen one starts with —
// and everything that deals with them reads those tables: the editor's "Type" select,
// the behaviour that clears the other sources when one is chosen, and the viewer's
// getOptionsSource. Adding the remote source to the tables is therefore all it takes for
// form-js to treat it as one of its own; nothing of form-js is patched or copied.
//
// This module runs once, when the bundle is evaluated, before any editor or viewer is
// constructed.

import {
  OPTIONS_SOURCES,
  OPTIONS_SOURCES_DEFAULTS,
  OPTIONS_SOURCES_LABELS,
  OPTIONS_SOURCES_PATHS,
  getOptionsSource
} from '@bpmn-io/form-js-viewer';

export const DATA_SOURCE_MEMBER = 'atlasDataSource';
export const REMOTE = 'remote';

// The block a newly chosen remote source starts with. It is deliberately incomplete: the
// editor fills in the source, the Worker and the defaults from what this server offers
// (editor.jsx), because those are not knowable here.
export const REMOTE_SEED = 1;

if (!OPTIONS_SOURCES.REMOTE) {
  OPTIONS_SOURCES.REMOTE = REMOTE;
  OPTIONS_SOURCES_LABELS[REMOTE] = 'Remote data source';
  OPTIONS_SOURCES_PATHS[REMOTE] = [ DATA_SOURCE_MEMBER ];

  // A getter, so every field that chooses the source gets its own object: the editor
  // stores the default by reference, and a shared one would be edited for all of them.
  Object.defineProperty(OPTIONS_SOURCES_DEFAULTS, REMOTE, {
    enumerable: true,
    get: () => ({ version: REMOTE_SEED })
  });
}

// isRemoteSelect reports whether a field is a select whose options come from a remote
// data source — the one kind this source is offered for.
export function isRemoteSelect(field) {
  return !!field && field.type === 'select' && getOptionsSource(field) === REMOTE;
}

// isLegacyDirectoryField reports a text field carrying a data source: the shape directory
// fields were saved in before they became selects. It is still read everywhere; the
// editor offers to convert it.
export function isLegacyDirectoryField(field) {
  return !!field && field.type === 'textfield' && !!field[DATA_SOURCE_MEMBER] && typeof field[DATA_SOURCE_MEMBER] === 'object';
}

// A field's options can come from a deployed DMN decision
// (ADR-draft-form-options-from-a-decision): a fifth options source beside form-js's own
// Static, Input data and Expression and the directory field's remote one (ADR-0473).
//
// The decision answers with a list — typically a boxed list its owners maintain in the
// decision editor — and takes no inputs. It is evaluated on the server when the form is
// shown, so the field offers what the newest deployment of the decision answers now.
//
// The source is added to the same four tables form-js describes its sources in
// (remote-source.js says why that is all it takes). Unlike the remote source it is
// offered on every field form-js gives options to: a select, a radio group, a checklist
// and a tag list.
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

export const DECISION_MEMBER = 'atlasDecision';
export const DECISION = 'decision';
export const DECISION_VERSION = 1;

// The fields that take their options from a source. A select and a radio group submit
// one value; a checklist and a tag list submit a list of them.
export const DECISION_FIELD_TYPES = [ 'select', 'radio', 'checklist', 'taglist' ];
export const MULTI_TYPES = [ 'checklist', 'taglist' ];

if (!OPTIONS_SOURCES.DECISION) {
  OPTIONS_SOURCES.DECISION = DECISION;
  OPTIONS_SOURCES_LABELS[DECISION] = 'Decision (DMN)';
  OPTIONS_SOURCES_PATHS[DECISION] = [ DECISION_MEMBER ];

  // A getter, for the reason remote-source.js gives: the editor stores the default by
  // reference. Which decision is filled in by the editor, which knows what is deployed.
  Object.defineProperty(OPTIONS_SOURCES_DEFAULTS, DECISION, {
    enumerable: true,
    get: () => ({ version: DECISION_VERSION, decisionId: '' })
  });
}

// isDecisionField reports whether a field takes its options from a decision.
export function isDecisionField(field) {
  return !!field && DECISION_FIELD_TYPES.includes(field.type) && getOptionsSource(field) === DECISION;
}

// decisionIdOf is the decision a field names, or ''.
export function decisionIdOf(field) {
  const block = field && field[DECISION_MEMBER];
  return block && typeof block === 'object' && typeof block.decisionId === 'string' ? block.decisionId.trim() : '';
}

// hasDecisionSource reports whether options a field is created from name a decision and
// none of the other sources — so form-js's create must not give it a static option to
// start from, which would make it read as a static field.
export function hasDecisionSource(options) {
  const block = options[DECISION_MEMBER];
  return !!block && typeof block === 'object' &&
    options.values === undefined && options.valuesKey === undefined && options.valuesExpression === undefined;
}

// bindingsOf is the field's inputs as the block states them: [{name, field}] for an
// input bound to another field, [{name, value}] for a fixed one.
export function bindingsOf(field) {
  const block = field && field[DECISION_MEMBER];
  return block && Array.isArray(block.inputs) ? block.inputs.filter((b) => b && typeof b.name === 'string') : [];
}

// isEmptyValue reports a field that holds nothing chosen yet.
export function isEmptyValue(v) {
  return v === undefined || v === null || v === '' || (Array.isArray(v) && v.length === 0);
}

// dependsOn is every field key the field's options depend on: the fields its inputs are
// bound to, and theirs in turn where those take their options from a decision. find
// looks a field up by key. The server refuses cycles; the walk stops at a key seen.
export function dependsOn(field, find) {
  const out = [];
  const visit = (f) => {
    for (const b of bindingsOf(f)) {
      if (typeof b.field !== 'string' || !b.field || out.includes(b.field)) continue;
      out.push(b.field);
      const next = find(b.field);
      if (next && next[DECISION_MEMBER]) visit(next);
    }
  };
  visit(field);
  return out;
}

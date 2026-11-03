// The viewer's half of a select whose options come from a remote data source (ADR-0473),
// and of a field whose options come from a decision (ADR-draft-form-options-from-a-decision).
//
// form-js renders every select through the component registered for the type "select".
// This module registers one that renders form-js's own Select for every options source
// form-js knows, and a search box for the remote one. The field stays a form-js select:
// its label, its `required`, its description, its errors and its value are form-js's,
// and the value it submits is the id of the entry chosen, never its label.
//
// What a search is — the request, the paging, the announcements, the keyboard — is not
// in this bundle. The page that renders the form provides it as the `atlasRemote`
// service, an adapter the field mounts into its own element (api/web/remote-select.js).
// A form rendered without one still works: as a box for the id, as a text field would
// take it. The editor's canvas shows the field that way too.
//
// A field whose options come from a decision is form-js's own select, radio group,
// checklist or tag list, given the decision's list as its static `values` once the page
// has fetched it through the `atlasDecisions` service. Until then it shows that it is
// loading, rather than an empty list form-js would clear a prefilled value against.
// When the decision's inputs are bound to other fields, it asks again as those change —
// a group chosen, its members offered — and form-js drops a value the new list does not
// offer.

import { useContext, useEffect, useMemo, useRef, useState } from 'preact/hooks';
import {
  Checklist,
  Description,
  Errors,
  FormContext,
  Label,
  Radio,
  Select,
  Taglist
} from '@bpmn-io/form-js-viewer';

import { DATA_SOURCE_MEMBER, isRemoteSelect } from './remote-source.js';
import {
  DECISION_MEMBER,
  MULTI_TYPES,
  bindingsOf,
  decisionIdOf,
  dependsOn,
  hasDecisionSource,
  isDecisionField,
  isEmptyValue
} from './decision-source.js';
import { MAIL_VIEW_TYPE, MailView } from './mailview.jsx';

// The longest value a remote select keeps from the data a form is opened with. An id is
// short; anything longer is not one, and is dropped as form-js drops a select's value
// that is not among its options.
export const MAX_VALUE_LENGTH = 256;

// sanitizeRemoteValue is what a remote select keeps of a value it is opened with. form-js
// keeps a select's value only when it is one of the options; a remote select's options
// are not known until somebody searches, so it keeps any plausible id and leaves the
// rest — whether the source offers it — to the server, which looks it up again before
// the task completes.
export function sanitizeRemoteValue(value) {
  if (typeof value !== 'string') return null;
  const v = value.trim();
  return v && v.length <= MAX_VALUE_LENGTH ? v : null;
}

function fieldClasses({ errors = [], disabled = false, readonly = false }) {
  return [
    'fjs-form-field',
    'fjs-form-field-select',
    'atlas-remote-select',
    errors.length > 0 ? 'fjs-has-errors' : '',
    disabled ? 'fjs-disabled' : '',
    readonly ? 'fjs-readonly' : ''
  ].filter(Boolean).join(' ');
}

function RemoteSelect(props) {
  const {
    disabled,
    errors = [],
    domId,
    field,
    readonly,
    value
  } = props;
  const {
    description,
    label,
    validate = {}
  } = field;
  const { required } = validate;
  const descriptionId = `${domId}-description`;
  const errorMessageId = `${domId}-error-message`;

  const { getService } = useContext(FormContext);
  const adapter = getService('atlasRemote', false);
  const form = getService('form', false);

  const hostRef = useRef(null);
  const handleRef = useRef(null);

  // The adapter is mounted once per field and told about every render after; what it
  // calls back reads the props of the latest render, never those of the first.
  const latest = useRef(props);
  latest.current = props;

  useEffect(() => {
    if (!adapter || typeof adapter.mount !== 'function' || !hostRef.current) {
      return undefined;
    }
    const handle = adapter.mount(hostRef.current, {
      field,
      form,
      inputId: domId,
      describedBy: [ descriptionId, errorMessageId ].join(' '),
      getValue: () => latest.current.value,
      setValue: (next) => latest.current.onChange({ value: sanitizeRemoteValue(next) }),
      onBlur: () => latest.current.onBlur && latest.current.onBlur(),
      onFocus: () => latest.current.onFocus && latest.current.onFocus()
    });
    handleRef.current = handle;
    return () => {
      handleRef.current = null;
      if (handle && typeof handle.destroy === 'function') handle.destroy();
    };
  }, [ adapter, field, form, domId, descriptionId, errorMessageId ]);

  useEffect(() => {
    const handle = handleRef.current;
    if (handle && typeof handle.update === 'function') {
      handle.update({ value: value == null ? '' : value, disabled: !!disabled, readonly: !!readonly });
    }
  });

  const mounted = !!(adapter && typeof adapter.mount === 'function');

  return (
    <div
      class={ fieldClasses({ errors, disabled, readonly }) }
      onKeyDown={ (event) => {
        // Never the form's implicit submission: choosing an entry is not completing
        // the task.
        if (event.key === 'Enter') {
          event.preventDefault();
          event.stopPropagation();
        }
      } }>
      <Label htmlFor={ domId } label={ label } required={ required } />
      { mounted
        ? <div class="atlas-remote-host" ref={ hostRef } />
        : <input
          class="fjs-input atlas-remote-plain"
          type="text"
          id={ domId }
          name={ field.key }
          autoComplete="off"
          spellcheck={ false }
          disabled={ disabled }
          readOnly={ readonly }
          value={ value == null ? '' : value }
          aria-invalid={ errors.length > 0 }
          aria-describedby={ [ descriptionId, errorMessageId ].join(' ') }
          onInput={ (e) => props.onChange({ value: sanitizeRemoteValue(e.target.value) }) }
          onBlur={ () => props.onBlur && props.onBlur() }
          onFocus={ () => props.onFocus && props.onFocus() } /> }
      <Description id={ descriptionId } description={ description } />
      <Errors id={ errorMessageId } errors={ errors } />
    </div>
  );
}

// ---------- options from a decision -------------------------------------------------------

const DECISION_TEXT = {
  de: { loading: 'Auswahl wird geladen …', failed: 'Die Auswahl konnte nicht geladen werden.' },
  en: { loading: 'Loading the choices …', failed: 'The choices could not be loaded.' }
};

function decisionText(locale) {
  return DECISION_TEXT[locale] || DECISION_TEXT.de;
}

// sanitizeDecisionValue is what a field whose options come from a decision keeps of a
// value it is opened with. form-js keeps a value only when it is one of the options, and
// these are not known until the page has fetched them; so a plausible value is kept, and
// form-js's own field drops it once the options are there if the list does not offer it.
// The server checks the value against the list again before the task completes.
export function sanitizeDecisionValue(value, multi) {
  const plausible = (v) => v !== null && v !== undefined && v !== '' &&
    ([ 'string', 'number', 'boolean' ].includes(typeof v) || (typeof v === 'object' && !Array.isArray(v)));
  if (multi) {
    return Array.isArray(value) ? value.filter(plausible) : [];
  }
  return plausible(value) ? value : null;
}

// How long a field waits after a field it is bound to changes before it asks again, so
// a value being typed is asked about once and not per keystroke.
const REASK_MS = 250;

// boundValues reads the values of the fields the options depend on from the form's
// current data, in the row of every dynamic list the field is in (indexes).
function boundValues(field, indexes, getService) {
  const registry = getService('formFieldRegistry', false);
  const paths = getService('pathRegistry', false);
  const form = getService('form', false);
  if (!registry || !paths || !form) return {};
  const all = registry.getAll();
  const find = (key) => all.find((f) => f && f.key === key);
  const data = form._getState().data || {};
  const values = {};
  for (const key of dependsOn(field, find)) {
    const f = find(key);
    if (!f) continue;
    let path = null;
    try { path = paths.getValuePath(f, { indexes }); } catch { path = null; }
    let v = data;
    for (const step of path || []) v = v == null ? undefined : v[step];
    values[key] = v === undefined ? null : v;
  }
  return values;
}

function DecisionOptions(props) {
  const { Base, field, domId, errors = [], onChange, indexes } = props;
  const { getService } = useContext(FormContext);
  const service = getService('atlasDecisions', false);
  const text = decisionText(getService('atlasLocale', false));
  const id = decisionIdOf(field);

  // What the decision is asked about: the values of the fields it depends on, and the
  // inputs those and the fixed ones make. Read on every render, because form-js renders
  // every field again whenever the form's data changes.
  const values = boundValues(field, indexes, getService);
  const inputs = {};
  let waiting = false;
  for (const b of bindingsOf(field)) {
    if (typeof b.field === 'string' && b.field) {
      inputs[b.name] = values[b.field];
      if (isEmptyValue(values[b.field])) waiting = true;
    } else {
      inputs[b.name] = b.value;
    }
  }
  const asked = JSON.stringify([ id, values ]);

  const [ state, setState ] = useState({ status: 'loading' });
  const first = useRef(true);

  useEffect(() => {
    let live = true;
    if (!service || typeof service.options !== 'function' || !id) {
      setState({ status: 'failed', reason: id ? '' : 'The field names no decision.' });
      return undefined;
    }
    // A field bound to one that holds nothing yet offers nothing, without asking.
    if (waiting) {
      setState({ status: 'loaded', options: [] });
      return undefined;
    }
    const ask = () => Promise.resolve()
      .then(() => service.options({ decisionId: id, field: field.key, inputs, values }))
      .then(
        (options) => { if (live) setState({ status: 'loaded', options: Array.isArray(options) ? options : [] }); },
        (err) => { if (live) setState({ status: 'failed', reason: (err && err.message) || '' }); });
    if (first.current) {
      first.current = false;
      ask();
      return () => { live = false; };
    }
    // Asked again: the options shown stay until the new ones are there.
    const timer = setTimeout(ask, REASK_MS);
    return () => { live = false; clearTimeout(timer); };
  }, [ service, asked ]);

  // The field form-js renders: the same field, with the decision's list as its own
  // static options. One object per list, so form-js's effects keyed on the field run
  // when the list changes and not on every render.
  const resolved = useMemo(() => {
    if (state.status !== 'loaded') return null;
    const { [DECISION_MEMBER]: _, ...rest } = field;
    return { ...rest, values: state.options };
  }, [ field, state ]);

  if (resolved) {
    // form-js reports a change with the field it rendered; the form knows the field it
    // imported, so that is the one handed back.
    return <Base { ...props } field={ resolved } onChange={ (update) => onChange({ ...update, field }) } />;
  }

  const { description, label, validate = {} } = field;
  const descriptionId = `${domId}-description`;
  const errorMessageId = `${domId}-error-message`;
  return (
    <div class={ [ 'fjs-form-field', `fjs-form-field-${field.type}`, 'atlas-decision-field', errors.length ? 'fjs-has-errors' : '' ].filter(Boolean).join(' ') }>
      <Label htmlFor={ domId } label={ label } required={ !!validate.required } />
      <div
        id={ domId }
        class={ `atlas-decision-${state.status}` }
        role="status"
        aria-live="polite"
        title={ state.status === 'failed' && state.reason ? state.reason : undefined }>
        { state.status === 'failed' ? text.failed : text.loading }
      </div>
      <Description id={ descriptionId } description={ description } />
      <Errors id={ errorMessageId } errors={ errors } />
    </div>
  );
}

// decisionAware wraps one of form-js's option fields: it is form-js's own field, unless
// its options come from a decision.
function decisionAware(Base, Inner = Base) {
  const multi = MULTI_TYPES.includes(Base.config.type);
  function Field(props) {
    if (isDecisionField(props.field)) {
      return <DecisionOptions { ...props } Base={ Base } />;
    }
    return <Inner { ...props } />;
  }
  Field.config = {
    ...Base.config,
    sanitizeValue: (options) => {
      if (isDecisionField(options.formField)) {
        return sanitizeDecisionValue(options.value, multi);
      }
      return Inner.config.sanitizeValue(options);
    },
    // form-js gives a field that names none of its own sources a static option to start
    // from, which would make one naming a decision read as a static field. The label is
    // the one form-js starts the type with.
    create: (options = {}) => {
      if (hasDecisionSource(options)) {
        const { values: _, ...start } = Base.config.create({ ...options, values: [] });
        return start;
      }
      return Inner.config.create(options);
    }
  };
  return Field;
}

// RemoteAwareSelect is form-js's own select, unless its options come from a remote data
// source.
function RemoteAwareSelect(props) {
  if (isRemoteSelect(props.field)) {
    return <RemoteSelect { ...props } />;
  }
  return <Select { ...props } />;
}

RemoteAwareSelect.config = {
  ...Select.config,
  sanitizeValue: (options) => {
    if (isRemoteSelect(options.formField)) {
      return sanitizeRemoteValue(options.value);
    }
    return Select.config.sanitizeValue(options);
  },
  // form-js runs create for every field it imports, and gives a select that names none
  // of its own options sources a static option to start from. A remote select names the
  // fourth; given that option too, it would read as a static select.
  create: (options = {}) => {
    if (hasRemoteSource(options)) {
      return { label: 'Select', ...options };
    }
    return Select.config.create(options);
  }
};

// AtlasSelect is what renders every select: form-js's own, unless its options come from a
// remote data source or a decision.
export const AtlasSelect = decisionAware(Select, RemoteAwareSelect);
export const AtlasRadio = decisionAware(Radio);
export const AtlasChecklist = decisionAware(Checklist);
export const AtlasTaglist = decisionAware(Taglist);

function hasRemoteSource(options) {
  const block = options[DATA_SOURCE_MEMBER];
  return !!block && typeof block === 'object' &&
    options.values === undefined && options.valuesKey === undefined && options.valuesExpression === undefined;
}

class AtlasRemoteFields {
  constructor(formFields) {
    formFields.register('select', AtlasSelect);
    formFields.register('radio', AtlasRadio);
    formFields.register('checklist', AtlasChecklist);
    formFields.register('taglist', AtlasTaglist);
    // The e-mail view (ADR-draft-mail-html-view): Atlas's own component, in every
    // form this bundle renders and in the editor's palette, canvas and preview.
    formFields.register(MAIL_VIEW_TYPE, MailView);
  }
}

AtlasRemoteFields.$inject = [ 'formFields' ];

// AtlasViewerModule registers the select above and the e-mail view. It is part of every
// form this bundle renders (viewer.js) and of the form editor's canvas and preview
// (playground.js).
export const AtlasViewerModule = {
  __init__: [ 'atlasRemoteFields' ],
  atlasRemoteFields: [ 'type', AtlasRemoteFields ]
};

// The form editor's half of a select whose options come from a remote data source
// (ADR-0473), and of a field whose options come from a decision
// (ADR-draft-form-options-from-a-decision).
//
// form-js's properties panel already has a place for where a select's options come
// from: Options source ▸ Type. The remote source is a fourth type there (remote-source.js),
// and what it needs — the source, the Worker, what is searched, filtered and shown — is
// edited where a static select's options are edited, in entries of the panel's own markup.
// Every edit is an ordinary command on the editor's stack: it undoes with Ctrl+Z and saves
// with the form.
//
// The entries below are written against the panel's markup rather than imported from
// @bpmn-io/properties-panel: form-js's editor carries that library inside its own bundle
// and exports none of it, and a second copy would bring a second Preact, whose hooks do
// not run under the first.
//
// The panel offers what this server offers: the page that opens the editor passes the
// answer of GET /api/v1/form-data/sources as the `atlasRemoteSources` value. Nothing here
// is trusted. The block is checked again when the form is saved, against the same
// registry, and refused with the reason if it does not hold.
//
// A decision is the fifth source (decision-source.js), offered on every field form-js
// gives options to. The panel picks it from GET /api/v1/form-data/decisions, passed as
// the `atlasDecisionList` value, and shows what it answers now through the
// `atlasDecisions` service. The save checks the block's shape; whether the decision is
// deployed is the server's state at the time the form is shown.

import { useEffect, useRef, useState } from 'preact/hooks';
import {
  OPTIONS_SOURCES,
  OPTIONS_SOURCES_DEFAULTS,
  OPTIONS_SOURCES_LABELS,
  OPTIONS_SOURCES_PATHS,
  Select,
  getOptionsSource
} from '@bpmn-io/form-js-viewer';

import {
  DATA_SOURCE_MEMBER,
  REMOTE,
  isLegacyDirectoryField,
  isRemoteSelect
} from './remote-source.js';
import {
  DECISION,
  DECISION_FIELD_TYPES,
  DECISION_MEMBER,
  DECISION_VERSION,
  decisionIdOf,
  isDecisionField
} from './decision-source.js';
import { MAIL_VIEW_TYPE } from './mailview.jsx';

// The palette entry that adds a select searching the first remote source. It is a preset,
// not a type of its own: what it creates is a select, and a select is what is saved.
export const PRESET_TYPE = 'directorypicker';

const MAX_DETAILS = 4;
const MAX_PAGE_SIZE = 50;
const MAX_MIN_SEARCH = 10;
const DEFAULT_PAGE_SIZE = 25;
const DEFAULT_MIN_SEARCH = 2;

const providersOf = (sources) => (sources && Array.isArray(sources.providers) ? sources.providers : []);
const workersOf = (sources) => (sources && Array.isArray(sources.workers) ? sources.workers : []);

export function providerFor(sources, block) {
  if (!block) return null;
  return providersOf(sources).find((p) => p.workerType === block.workerType && p.resource === block.resource) || null;
}

// firstOpenWorker names a Worker of the provider's type that already lets forms search,
// so a new field starts with one that answers.
export function firstOpenWorker(sources, provider) {
  const w = workersOf(sources).find((x) => x.workerType === provider.workerType && x.formLookups);
  return w ? w.name : '';
}

// defaultDataSource is the block a newly chosen remote source starts with.
export function defaultDataSource(provider, worker) {
  return {
    version: 1,
    workerType: provider.workerType,
    worker: worker || '',
    resource: provider.resource,
    searchFields: [ 'displayName', 'mail', 'userPrincipalName' ].filter((p) => (provider.searchable || []).includes(p)),
    filters: [],
    option: { label: provider.defaultLabel, description: provider.defaultDescription, details: [] },
    pageSize: DEFAULT_PAGE_SIZE,
    minSearchLength: DEFAULT_MIN_SEARCH
  };
}

const clone = (v) => JSON.parse(JSON.stringify(v));

// isFixed reports whether a filter is a fixed value rather than bound to a field.
const isFixed = (f) => Object.prototype.hasOwnProperty.call(f, 'value') && !Object.prototype.hasOwnProperty.call(f, 'field');

function cleanFilter(f) {
  const out = { property: f.property };
  if (isFixed(f)) {
    out.value = f.value || '';
  } else {
    out.field = f.field || '';
    if (f.required) out.required = true;
  }
  return out;
}

// keyedFields lists the fields a filter may be bound to: every keyed field of the form
// other than the remote select itself.
function keyedFields(registry, except) {
  if (!registry) return [];
  return registry.getAll()
    .filter((f) => f && f !== except && typeof f.key === 'string' && f.key && f.type !== 'default')
    .map((f) => ({ key: f.key, label: f.label || f.key }));
}

// The context every entry below works from. block() reads the field as it is now, not as
// it was when the panel rendered.
function contextFor(field, editField, services) {
  const ctx = {
    field,
    editField,
    sources: services.sources,
    registry: services.registry,
    eventBus: services.eventBus,
    block: () => {
      const b = field[DATA_SOURCE_MEMBER];
      return b && typeof b === 'object' ? b : {};
    },
    provider: () => providerFor(services.sources, ctx.block()),
    write: (mutate) => {
      const next = clone(ctx.block());
      mutate(next);
      editField(field, DATA_SOURCE_MEMBER, next);
    }
  };
  return ctx;
}

// ---------- entries in the panel's markup ------------------------------------------------

const inputId = (id) => `bio-properties-panel-${id}`;
const isEditedValue = (node) => !!(node && node.value);
const isEditedCheck = (node) => !!(node && node.checked);
// The Type select reads as edited once it names anything but form-js's default source.
const isEditedSource = (node) => !!(node && node.value && node.value !== OPTIONS_SOURCES.STATIC);

// What the Options source group's tooltip adds for a select: form-js's own explains the
// three sources it knows.
const REMOTE_TOOLTIP = '"Remote data source" defines options a person searches for in a directory, through a Worker this server configures; the form submits the id of the entry chosen.';
const DECISION_TOOLTIP = '"Decision (DMN)" takes the options from a deployed decision without inputs — typically a List its owners maintain in the decision editor. The field shows what the newest version answers when the form is opened.';

function Entry(props) {
  const { id, error, description, extraClass, children } = props;
  const cls = [ 'bio-properties-panel-entry', extraClass, error ? 'has-error' : '' ].filter(Boolean).join(' ');
  return (
    <div class={ cls } data-entry-id={ id }>
      { children }
      { error ? <div class="bio-properties-panel-error">{ error }</div> : null }
      { description ? <div class="bio-properties-panel-description">{ description }</div> : null }
    </div>
  );
}

function PanelSelect(props) {
  const { id, label, value, options, onChange, error, description } = props;
  return (
    <Entry id={ id } error={ error } description={ description }>
      <div class="bio-properties-panel-select">
        <label for={ inputId(id) } class="bio-properties-panel-label">{ label }</label>
        <select
          id={ inputId(id) }
          name={ id }
          class="bio-properties-panel-input"
          value={ value }
          onChange={ (e) => { if (e.target.value !== value) onChange(e.target.value); } }>
          { options.map((o) => <option key={ o.value } value={ o.value }>{ o.label }</option>) }
        </select>
      </div>
    </Entry>
  );
}

function PanelCheckbox(props) {
  const { id, label, value, onChange, description } = props;
  return (
    <Entry id={ id } description={ description } extraClass="bio-properties-panel-checkbox-entry atlas-remote-checkbox">
      <div class="bio-properties-panel-checkbox">
        <input
          id={ inputId(id) }
          name={ id }
          type="checkbox"
          class="bio-properties-panel-input"
          checked={ !!value }
          onChange={ (e) => onChange(e.target.checked) } />
        <label for={ inputId(id) } class="bio-properties-panel-label">{ label }</label>
      </div>
    </Entry>
  );
}

// PanelInput holds what is being typed and commits it when the input is left or Enter is
// pressed: one edit, one step on the undo stack, rather than one per keystroke.
function PanelInput(props) {
  const { id, label, value, type = 'text', min, max, onCommit, validate, description } = props;
  const shown = value === undefined || value === null ? '' : String(value);
  const [ text, setText ] = useState(shown);
  useEffect(() => { setText(shown); }, [ shown ]);
  const error = validate ? validate(text) : null;
  return (
    <Entry id={ id } error={ error } description={ description }>
      <div class={ type === 'number' ? 'bio-properties-panel-numberfield' : 'bio-properties-panel-textfield' }>
        <label for={ inputId(id) } class="bio-properties-panel-label">{ label }</label>
        <input
          id={ inputId(id) }
          name={ id }
          type={ type }
          class="bio-properties-panel-input"
          spellcheck={ false }
          autoComplete="off"
          min={ min }
          max={ max }
          value={ text }
          onInput={ (e) => setText(e.target.value) }
          onChange={ (e) => {
            const next = e.target.value;
            if (next !== shown && !(validate && validate(next))) onCommit(next);
          } } />
      </div>
    </Entry>
  );
}

// PanelChecks is a labelled list of checkboxes: the properties to search in, the details
// an option shows.
function PanelChecks(props) {
  const { id, label, options, checked, onToggle, description, limit } = props;
  const full = Number.isInteger(limit) && checked.length >= limit;
  return (
    <Entry id={ id } description={ description } extraClass="atlas-remote-checks">
      <span class="bio-properties-panel-label">{ label }</span>
      { options.map((o, i) => {
        const on = checked.includes(o.value);
        const cid = `${inputId(id)}-${i}`;
        return (
          <div class="bio-properties-panel-checkbox" key={ o.value }>
            <input
              id={ cid }
              name={ `${id}-${o.value}` }
              type="checkbox"
              class="bio-properties-panel-input"
              checked={ on }
              disabled={ !on && full }
              onChange={ (e) => onToggle(o.value, e.target.checked) } />
            <label for={ cid } class="bio-properties-panel-label">{ o.label }</label>
          </div>
        );
      }) }
    </Entry>
  );
}

function toggled(list, value, on) {
  const rest = (list || []).filter((v) => v !== value);
  return on ? [ ...rest, value ] : rest;
}

function wholeNumber(min, max) {
  return (text) => {
    if (text === '' || text === undefined || text === null) return null;
    const n = Number(text);
    return Number.isInteger(n) && n >= min && n <= max ? null : `Must be a whole number between ${min} and ${max}.`;
  };
}

// ---------- Options source ▸ Type -------------------------------------------------------

// The Type select of form-js, offering the remote source only where it is supported: on a
// select, and only when this server offers a source — or the field already uses one.
function OptionsSourceType(props) {
  const { element: field, id, ctx } = props;
  const value = getOptionsSource(field);
  const prev = useRef(value);

  useEffect(() => {
    const focus = autoFocusEntry(value);
    if (prev.current !== value && focus && ctx.eventBus) {
      setTimeout(() => ctx.eventBus.fire('propertiesPanel.showEntry', { id: focus }));
    }
    prev.current = value;
  }, [ value ]);

  const options = Object.values(OPTIONS_SOURCES)
    .filter((source) => source !== REMOTE || (field.type === 'select' && (providersOf(ctx.sources).length > 0 || value === REMOTE)))
    .filter((source) => source !== DECISION || DECISION_FIELD_TYPES.includes(field.type))
    .map((source) => ({ label: OPTIONS_SOURCES_LABELS[source], value: source }));

  return (
    <PanelSelect
      id={ id }
      label="Type"
      value={ value }
      options={ options }
      onChange={ (next) => ctx.editField(field, { [OPTIONS_SOURCES_PATHS[next]]: OPTIONS_SOURCES_DEFAULTS[next] }) } />
  );
}

// The entry form-js focuses after its Type changes, so the next thing to fill in is ready.
function autoFocusEntry(source) {
  if (source === OPTIONS_SOURCES.EXPRESSION) return 'optionsExpression-expression';
  if (source === OPTIONS_SOURCES.INPUT) return 'dynamicOptions-key';
  if (source === OPTIONS_SOURCES.STATIC) return 'staticOptions-0-label';
  if (source === DECISION) return 'atlasDecision-decision';
  return null;
}

// ---------- the source ------------------------------------------------------------------

function SourceEntry(props) {
  const { id, ctx } = props;
  const block = ctx.block();
  const provider = ctx.provider();
  const current = block.workerType ? block.workerType + '/' + block.resource : '';
  const options = [
    ...(provider ? [] : [ { value: current, label: current ? current + ' (not offered by this server)' : 'Choose a source…' } ]),
    ...providersOf(ctx.sources).map((p) => ({ value: p.workerType + '/' + p.resource, label: p.label }))
  ];
  return (
    <PanelSelect
      id={ id }
      label="Source"
      value={ current }
      options={ options }
      error={ provider ? null : 'This server offers no such remote data source.' }
      onChange={ (v) => {
        const [ workerType, resource ] = String(v).split('/');
        const p = providersOf(ctx.sources).find((x) => x.workerType === workerType && x.resource === resource);
        if (p) ctx.editField(ctx.field, DATA_SOURCE_MEMBER, defaultDataSource(p, firstOpenWorker(ctx.sources, p)));
      } } />
  );
}

function WorkerEntry(props) {
  const { id, ctx } = props;
  const block = ctx.block();
  const provider = ctx.provider();
  const ofType = workersOf(ctx.sources).filter((w) => provider && w.workerType === provider.workerType);
  const chosen = ofType.find((w) => w.name === block.worker);
  const options = [
    { value: '', label: 'Choose a Worker…' },
    ...ofType.map((w) => ({
      value: w.name,
      label: w.name + (w.formLookups ? '' : ' — not open to forms') + (w.enabled ? '' : ' (switched off)')
    })),
    ...(block.worker && !chosen ? [ { value: block.worker, label: block.worker + ' (not configured)' } ] : [])
  ];
  const description = chosen && !chosen.formLookups
    ? `The Worker ${chosen.name} does not let forms search through it yet. Its owner can allow it in the Console (Workers ▸ ${chosen.name} ▸ Forms may search). Until then the search answers “forbidden”.`
    : 'Searches run with this Worker’s credential. Whether forms may use it is set on the Worker, not here.';
  return (
    <PanelSelect
      id={ id }
      label="Worker"
      value={ block.worker || '' }
      options={ options }
      description={ description }
      error={ block.worker && String(block.worker).trim() ? null : 'Choose the Worker whose credential answers the search.' }
      onChange={ (v) => ctx.write((b) => { b.worker = v; }) } />
  );
}

function SearchFieldsEntry(props) {
  const { id, ctx } = props;
  const block = ctx.block();
  const provider = ctx.provider();
  const offered = provider ? provider.searchable || [] : [];
  const stored = block.searchFields || [];
  const options = [
    ...offered.map((p) => ({ value: p, label: p })),
    ...stored.filter((p) => !offered.includes(p)).map((p) => ({ value: p, label: p + ' (cannot be searched)' }))
  ];
  return (
    <PanelChecks
      id={ id }
      label="Search in"
      options={ options }
      checked={ stored }
      onToggle={ (p, on) => ctx.write((b) => { b.searchFields = toggled(b.searchFields, p, on); }) } />
  );
}

function MinSearchEntry(props) {
  const { id, ctx } = props;
  const block = ctx.block();
  return (
    <PanelInput
      id={ id }
      type="number"
      label="Search after (characters)"
      description="0 lists the first page as soon as the field is shown."
      min={ 0 }
      max={ MAX_MIN_SEARCH }
      value={ block.minSearchLength === undefined ? DEFAULT_MIN_SEARCH : block.minSearchLength }
      validate={ wholeNumber(0, MAX_MIN_SEARCH) }
      onCommit={ (t) => ctx.write((b) => { b.minSearchLength = t === '' ? DEFAULT_MIN_SEARCH : Number(t); }) } />
  );
}

function PageSizeEntry(props) {
  const { id, ctx } = props;
  const block = ctx.block();
  return (
    <PanelInput
      id={ id }
      type="number"
      label="Results per page"
      min={ 1 }
      max={ MAX_PAGE_SIZE }
      value={ block.pageSize || DEFAULT_PAGE_SIZE }
      validate={ wholeNumber(1, MAX_PAGE_SIZE) }
      onCommit={ (t) => ctx.write((b) => { b.pageSize = t === '' ? DEFAULT_PAGE_SIZE : Number(t); }) } />
  );
}

function ActiveOnlyEntry(props) {
  const { id, ctx } = props;
  const block = ctx.block();
  return (
    <PanelCheckbox
      id={ id }
      label="Leave out disabled accounts"
      value={ block.activeOnly !== false }
      onChange={ (on) => ctx.write((b) => { if (on) delete b.activeOnly; else b.activeOnly = false; }) } />
  );
}

// ---------- Remote filters ----------------------------------------------------------------

function editFilter(ctx, index, mutate) {
  ctx.write((b) => {
    const filters = b.filters || [];
    if (!filters[index]) return;
    mutate(filters[index]);
    filters[index] = cleanFilter(filters[index]);
    b.filters = filters;
  });
}

function FilterRow(props) {
  const { id, ctx, index, filter: f, fields, offered, taken } = props;
  const known = fields.some((k) => k.key === f.field);
  const propertyOptions = [
    ...(f.property && !offered.includes(f.property) ? [ { value: f.property, label: f.property + ' (cannot be filtered on)' } ] : []),
    ...offered.map((p) => ({ value: p, label: p }))
  ];
  const fieldOptions = [
    { value: '', label: 'Choose a field…' },
    ...fields.map((k) => ({ value: k.key, label: k.label === k.key ? k.key : `${k.label} (${k.key})` })),
    ...(f.field && !known ? [ { value: f.field, label: f.field + ' (not a field of this form)' } ] : [])
  ];
  return (
    <div class="atlas-remote-filter" data-index={ index }>
      <PanelSelect
        id={ `${id}-property` }
        label="Property"
        value={ f.property || '' }
        options={ propertyOptions }
        error={ taken.includes(f.property) ? 'This property is filtered twice.' : null }
        onChange={ (v) => editFilter(ctx, index, (x) => { x.property = v; }) } />
      <PanelSelect
        id={ `${id}-mode` }
        label="Compare with"
        value={ isFixed(f) ? 'value' : 'field' }
        options={ [ { value: 'field', label: 'Another field of this form' }, { value: 'value', label: 'A fixed value' } ] }
        onChange={ (v) => editFilter(ctx, index, (x) => {
          if (v === 'value') { delete x.field; delete x.required; x.value = x.value || ''; } else { delete x.value; x.field = ''; }
        }) } />
      { isFixed(f)
        ? <PanelInput
          id={ `${id}-value` }
          label="Value"
          value={ f.value || '' }
          validate={ (t) => (t && String(t).trim() ? null : 'A fixed filter needs a value.') }
          onCommit={ (t) => editFilter(ctx, index, (x) => { x.value = t; }) } />
        : <PanelSelect
          id={ `${id}-field` }
          label="Field"
          value={ f.field || '' }
          options={ fieldOptions }
          error={ !f.field ? 'Choose the field whose value the search is filtered by.' : known ? null : 'This is not a field of this form.' }
          onChange={ (v) => editFilter(ctx, index, (x) => { x.field = v; }) } /> }
      { isFixed(f) ? null : <PanelCheckbox
        id={ `${id}-required` }
        label="Required before searching"
        description="No search runs until this field has a value."
        value={ !!f.required }
        onChange={ (on) => editFilter(ctx, index, (x) => { if (on) x.required = true; else delete x.required; }) } /> }
      <button
        type="button"
        class="atlas-remote-button atlas-remote-remove"
        onClick={ () => ctx.write((b) => { b.filters = (b.filters || []).filter((_, i) => i !== index); }) }>
        Remove filter
      </button>
    </div>
  );
}

function FiltersEntry(props) {
  const { id, ctx } = props;
  const filters = ctx.block().filters || [];
  const provider = ctx.provider();
  const offered = provider ? provider.filterable || [] : [];
  const fields = keyedFields(ctx.registry, ctx.field);
  const used = new Set(filters.map((f) => f.property));
  const free = offered.find((p) => !used.has(p));
  return (
    <div class="bio-properties-panel-entry atlas-remote-filters" data-entry-id={ id }>
      { filters.length
        ? filters.map((f, index) => (
          <FilterRow
            key={ index }
            id={ `${id}-${index}` }
            ctx={ ctx }
            index={ index }
            filter={ f }
            fields={ fields }
            offered={ offered }
            taken={ filters.filter((_, i) => i < index).map((x) => x.property) } />
        ))
        : <div class="bio-properties-panel-description">No filter: a search runs over the whole source.</div> }
      <button
        type="button"
        class="atlas-remote-button"
        disabled={ !free }
        onClick={ () => ctx.write((b) => { b.filters = [ ...(b.filters || []), { property: free, field: '' } ]; }) }>
        Add filter
      </button>
    </div>
  );
}

// ---------- Option display ----------------------------------------------------------------

function OptionPropertyEntry(props) {
  const { id, ctx, member, label } = props;
  const block = ctx.block();
  const provider = ctx.provider();
  const readable = provider ? provider.readable || [] : [];
  const fallback = provider ? (member === 'label' ? provider.defaultLabel : provider.defaultDescription) : '';
  const current = (block.option && block.option[member]) || fallback || '';
  const options = [
    ...(current && !readable.includes(current) ? [ { value: current, label: current + ' (cannot be shown)' } ] : []),
    ...readable.map((p) => ({ value: p, label: p }))
  ];
  return (
    <PanelSelect
      id={ id }
      label={ label }
      value={ current }
      options={ options }
      onChange={ (v) => ctx.write((b) => { b.option = { ...(b.option || {}), [member]: v }; }) } />
  );
}

function OptionDetailsEntry(props) {
  const { id, ctx } = props;
  const block = ctx.block();
  const provider = ctx.provider();
  const readable = provider ? (provider.readable || []).filter((p) => p !== provider.valueProperty) : [];
  const stored = (block.option && block.option.details) || [];
  const options = [
    ...readable.map((p) => ({ value: p, label: p })),
    ...stored.filter((p) => !readable.includes(p)).map((p) => ({ value: p, label: p + ' (cannot be shown)' }))
  ];
  return (
    <PanelChecks
      id={ id }
      label="Details"
      options={ options }
      checked={ stored }
      limit={ MAX_DETAILS }
      description={ `The form submits the entry’s ${provider ? provider.valueProperty : 'id'}, never its label. An option shows at most ${MAX_DETAILS} details.` }
      onToggle={ (p, on) => ctx.write((b) => {
        b.option = { ...(b.option || {}), details: toggled(b.option && b.option.details, p, on) };
      }) } />
  );
}

// ---------- Validation --------------------------------------------------------------------

function VerifyEntry(props) {
  const { id, ctx } = props;
  const block = ctx.block();
  return (
    <PanelSelect
      id={ id }
      label="When the task is completed"
      value={ block.verify === 'format' ? 'format' : 'lookup' }
      options={ [
        { value: 'lookup', label: 'Look the entry up again' },
        { value: 'format', label: 'Check its format only' }
      ] }
      onChange={ (v) => ctx.write((b) => { if (v === 'format') b.verify = 'format'; else delete b.verify; }) } />
  );
}

// ---------- options from a decision (ADR-draft-form-options-from-a-decision) ------------------

const decisionsOf = (list) => (list && Array.isArray(list.decisions) ? list.decisions : []);

// decisionLabel names a decision as the picker shows it: its name, and the model it is in
// where that says more.
function decisionLabel(d) {
  return d.model && d.model !== d.name ? `${d.name} — ${d.model}` : d.name;
}

function DecisionEntry(props) {
  const { id, field, editField, list } = props;
  const current = decisionIdOf(field);
  const offered = decisionsOf(list);
  const known = offered.some((d) => d.id === current);
  const options = [
    ...(current ? [] : [ { value: '', label: offered.length ? 'Choose a decision…' : 'No decision without inputs is deployed' } ]),
    ...(current && !known ? [ { value: current, label: current + ' (not deployed, or takes inputs)' } ] : []),
    ...offered.map((d) => ({ value: d.id, label: decisionLabel(d) }))
  ];
  const error = !current
    ? 'Choose the decision whose list the options are.'
    : known ? null : 'This server has no deployed decision of that name that takes no inputs. Until it has, the field says its options could not be loaded.';
  return (
    <PanelSelect
      id={ id }
      label="Decision"
      value={ current }
      options={ options }
      error={ error }
      description="Offered are the decisions deployed on their own that take no inputs. The field shows the list the newest version answers when the form is opened; the form submits the value of the entry chosen."
      onChange={ (v) => editField(field, DECISION_MEMBER, { version: DECISION_VERSION, decisionId: v }) } />
  );
}

const PREVIEW_LABELS = 8;

// DecisionPreviewEntry shows what the decision answers now: how many options, the first
// few labels, and how many of its entries are no option a form can show — or why it
// cannot answer.
function DecisionPreviewEntry(props) {
  const { id, field, service } = props;
  const decisionId = decisionIdOf(field);
  const [ state, setState ] = useState({ status: 'idle' });

  useEffect(() => {
    let live = true;
    if (!decisionId || !service || typeof service.answer !== 'function') {
      setState({ status: 'idle' });
      return undefined;
    }
    setState({ status: 'loading' });
    Promise.resolve()
      .then(() => service.answer(decisionId))
      .then(
        (answer) => { if (live) setState({ status: 'loaded', answer: answer || {} }); },
        (err) => { if (live) setState({ status: 'failed', reason: (err && err.message) || 'The decision did not answer.' }); });
    return () => { live = false; };
  }, [ decisionId, service ]);

  if (state.status === 'idle') return null;
  let text = 'Asking the decision…';
  if (state.status === 'failed') {
    text = state.reason;
  } else if (state.status === 'loaded') {
    const options = Array.isArray(state.answer.options) ? state.answer.options : [];
    const labels = options.slice(0, PREVIEW_LABELS).map((o) => o.label).join(', ');
    text = `${options.length} option${options.length === 1 ? '' : 's'}` +
      (labels ? `: ${labels}${options.length > PREVIEW_LABELS ? ', …' : ''}` : '') +
      (state.answer.skipped ? `. ${state.answer.skipped} entr${state.answer.skipped === 1 ? 'y is' : 'ies are'} no option (null, a list, or no usable value) and not shown.` : '');
  }
  return (
    <Entry id={ id } extraClass={ `atlas-decision-preview atlas-decision-${state.status}` } error={ state.status === 'failed' ? text : null }>
      <span class="bio-properties-panel-label">Options now</span>
      { state.status === 'failed' ? null : <div class="bio-properties-panel-description">{ text }</div> }
    </Entry>
  );
}

// ---------- a directory field saved as a text field ----------------------------------------

// convertToSelect turns a text field that searches a remote data source — the shape such
// fields were saved in first — into the select it is now. What only a text field has goes;
// the data source, the key, the label and `required` stay.
export function convertToSelect(field, editField) {
  const required = !!(field.validate && field.validate.required);
  editField(field, {
    type: 'select',
    defaultValue: undefined,
    appearance: undefined,
    validate: required ? { required: true } : undefined
  });
}

function ConvertEntry(props) {
  const { id, ctx } = props;
  return (
    <Entry
      id={ id }
      extraClass="atlas-remote-convert"
      description="This text field searches a remote data source. It was saved before such fields became selects, and it keeps working as it is. As a select, its data source is edited here, under Options source.">
      <button
        type="button"
        class="atlas-remote-button"
        onClick={ () => convertToSelect(ctx.field, ctx.editField) }>
        Convert to select
      </button>
    </Entry>
  );
}

// ---------- the e-mail view (ADR-draft-mail-html-view) -------------------------------------

// MAIL_KEY is what a variable name may be: names joined by dots, as a key is everywhere.
const MAIL_KEY = /^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$/;

function MailViewLabelEntry(props) {
  const { id, field, editField } = props;
  return (
    <PanelInput
      id={ id }
      label="Label"
      value={ field.label || '' }
      onCommit={ (v) => editField(field, 'label', v) } />
  );
}

function MailViewKeyEntry(props) {
  const { id, field, editField } = props;
  return (
    <PanelInput
      id={ id }
      label="Variable"
      value={ field.key || '' }
      validate={ (v) => (MAIL_KEY.test(v.trim()) ? null : 'A variable name, such as mail or request.mail.') }
      description="The variable a mail task's Read message wrote — its result variable. The form shows the mail and never submits it. With Text set to text and HTML, the HTML version is shown too; remote images stay blocked until the reader loads them."
      onCommit={ (v) => editField(field, 'key', v.trim()) } />
  );
}

// ---------- the provider ------------------------------------------------------------------

class AtlasRemotePropertiesProvider {
  constructor(propertiesPanel, injector) {
    this._injector = injector;
    // After form-js's own provider (priority 1000), so its groups are there to extend.
    propertiesPanel.registerProvider(this, 500);
  }

  _services() {
    const get = (name) => this._injector.get(name, false);
    return {
      sources: get('atlasRemoteSources'),
      decisions: get('atlasDecisionList'),
      decisionService: get('atlasDecisions'),
      registry: get('formFieldRegistry'),
      eventBus: get('eventBus')
    };
  }

  getGroups(field, editField) {
    return (groups) => {
      if (!field || Array.isArray(field)) return groups;
      const services = this._services();
      const ctx = contextFor(field, editField, services);

      const source = groups.find((g) => g.id === 'valuesSource');
      if (source && Array.isArray(source.entries)) {
        source.entries = source.entries.map((e) => (e.id === 'valuesSource-select'
          ? { id: e.id, component: OptionsSourceType, ctx, isEdited: isEditedSource }
          : e));
        if (field.type === 'select' && typeof source.tooltip === 'string') {
          source.tooltip = source.tooltip + '\n\n' + REMOTE_TOOLTIP;
        }
        if (DECISION_FIELD_TYPES.includes(field.type) && typeof source.tooltip === 'string') {
          source.tooltip = source.tooltip + '\n\n' + DECISION_TOOLTIP;
        }
      }

      if (field.type === MAIL_VIEW_TYPE) return this._mailView(groups, field, editField);
      if (isRemoteSelect(field)) return this._remote(groups, ctx);
      if (isDecisionField(field)) return this._decision(groups, field, editField, services);
      if (isLegacyDirectoryField(field)) return this._legacy(groups, ctx);
      return groups;
    };
  }

  // _mailView gives the e-mail view what form-js gives a field it knows: a label, and
  // the variable it shows. form-js offers neither for a component that is not keyed.
  _mailView(groups, field, editField) {
    const entries = [
      { id: 'atlasMail-label', component: MailViewLabelEntry, field, editField, isEdited: isEditedValue },
      { id: 'atlasMail-key', component: MailViewKeyEntry, field, editField, isEdited: isEditedValue }
    ];
    const general = groups.find((g) => g.id === 'general');
    if (general && Array.isArray(general.entries)) {
      general.entries = [ ...general.entries.filter((e) => e.id !== 'label' && e.id !== 'key'), ...entries ];
      return groups;
    }
    return [ { id: 'general', label: 'General', entries }, ...groups ];
  }

  _remote(groups, ctx) {
    const general = groups.find((g) => g.id === 'general');
    if (general && Array.isArray(general.entries)) {
      // A remote select is always searched; form-js's "Searchable" switches its own
      // dropdown to a filter box, which a remote select does not have.
      general.entries = general.entries.filter((e) => e.id !== 'searchable');
    }

    const source = groups.find((g) => g.id === 'valuesSource');
    if (source && Array.isArray(source.entries)) {
      source.entries = [
        ...source.entries,
        { id: 'atlasRemote-source', component: SourceEntry, ctx, isEdited: isEditedValue },
        { id: 'atlasRemote-worker', component: WorkerEntry, ctx, isEdited: isEditedValue },
        { id: 'atlasRemote-searchFields', component: SearchFieldsEntry, ctx, isEdited: isEditedCheck },
        { id: 'atlasRemote-minSearchLength', component: MinSearchEntry, ctx, isEdited: isEditedValue },
        { id: 'atlasRemote-pageSize', component: PageSizeEntry, ctx, isEdited: isEditedValue },
        { id: 'atlasRemote-activeOnly', component: ActiveOnlyEntry, ctx, isEdited: isEditedCheck }
      ];
    }

    const extra = [
      {
        id: 'atlasRemoteFilters',
        label: 'Remote filters',
        entries: [ { id: 'atlasRemote-filters', component: FiltersEntry, ctx } ]
      },
      {
        id: 'atlasRemoteOption',
        label: 'Option display',
        entries: [
          { id: 'atlasRemote-optionLabel', component: OptionPropertyEntry, ctx, member: 'label', label: 'Label', isEdited: isEditedValue },
          { id: 'atlasRemote-optionDescription', component: OptionPropertyEntry, ctx, member: 'description', label: 'Below it', isEdited: isEditedValue },
          { id: 'atlasRemote-optionDetails', component: OptionDetailsEntry, ctx, isEdited: isEditedCheck }
        ]
      }
    ];
    const at = groups.indexOf(source);
    const out = at >= 0 ? [ ...groups.slice(0, at + 1), ...extra, ...groups.slice(at + 1) ] : [ ...groups, ...extra ];

    const verify = { id: 'atlasRemote-verify', component: VerifyEntry, ctx, isEdited: isEditedValue };
    const validation = out.find((g) => g.id === 'validation');
    if (validation && Array.isArray(validation.entries)) {
      validation.entries = [ ...validation.entries, verify ];
      return out;
    }
    return [ ...out, { id: 'validation', label: 'Validation', entries: [ verify ] } ];
  }

  _decision(groups, field, editField, services) {
    const source = groups.find((g) => g.id === 'valuesSource');
    const entries = [
      { id: 'atlasDecision-decision', component: DecisionEntry, field, editField, list: services.decisions, isEdited: isEditedValue },
      { id: 'atlasDecision-preview', component: DecisionPreviewEntry, field, service: services.decisionService }
    ];
    if (source && Array.isArray(source.entries)) {
      source.entries = [ ...source.entries, ...entries ];
      return groups;
    }
    return [ ...groups, { id: 'valuesSource', label: 'Options source', entries } ];
  }

  _legacy(groups, ctx) {
    const group = {
      id: 'atlasRemoteLegacy',
      label: 'Remote data source',
      entries: [ { id: 'atlasRemote-convert', component: ConvertEntry, ctx } ]
    };
    const at = groups.findIndex((g) => g.id === 'general');
    return at >= 0 ? [ ...groups.slice(0, at + 1), group, ...groups.slice(at + 1) ] : [ group, ...groups ];
  }
}

AtlasRemotePropertiesProvider.$inject = [ 'propertiesPanel', 'injector' ];

// ---------- choosing the source fills it in -------------------------------------------------

// AtlasRemoteBehavior fills in a remote source the moment it is chosen. form-js starts a
// newly chosen source from a fixed default; for the remote one that default names no
// source and no Worker, because only this server knows them. So the edit that chooses it
// is completed here, before it runs, and is one command: it undoes as one.
class AtlasRemoteBehavior {
  constructor(eventBus, injector) {
    eventBus.on('commandStack.formField.edit.preExecute', (event) => {
      const context = event && event.context;
      if (!context || !context.properties || !context.formField) return;
      const block = context.properties[DATA_SOURCE_MEMBER];
      if (!block || typeof block !== 'object' || block.workerType) return;
      if (context.formField.type !== 'select') return;
      const sources = injector.get('atlasRemoteSources', false);
      const provider = providersOf(sources)[0];
      context.properties = {
        ...context.properties,
        ...(provider ? { [DATA_SOURCE_MEMBER]: defaultDataSource(provider, firstOpenWorker(sources, provider)) } : {}),
        defaultValue: undefined,
        searchable: undefined
      };
    });
  }
}

AtlasRemoteBehavior.$inject = [ 'eventBus', 'injector' ];

// AtlasDecisionBehavior names a decision the moment the decision source is chosen, as
// AtlasRemoteBehavior fills in a remote one: form-js's default for it names none, because
// only this server knows which are deployed. The first decision offered is a start; the
// panel lets the author pick another.
class AtlasDecisionBehavior {
  constructor(eventBus, injector) {
    eventBus.on('commandStack.formField.edit.preExecute', (event) => {
      const context = event && event.context;
      if (!context || !context.properties || !context.formField) return;
      const block = context.properties[DECISION_MEMBER];
      if (!block || typeof block !== 'object' || block.decisionId) return;
      if (!DECISION_FIELD_TYPES.includes(context.formField.type)) return;
      const first = decisionsOf(injector.get('atlasDecisionList', false))[0];
      if (!first) return;
      context.properties = {
        ...context.properties,
        [DECISION_MEMBER]: { version: DECISION_VERSION, decisionId: first.id }
      };
    });
  }
}

AtlasDecisionBehavior.$inject = [ 'eventBus', 'injector' ];

// ---------- the palette ---------------------------------------------------------------------

function DirectoryPickerIcon(props) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" width="54" height="54" viewBox="0 0 54 54" fill="none" { ...props }>
      <rect x="9" y="17" width="36" height="20" rx="2" stroke="currentColor" stroke-width="2" />
      <circle cx="21" cy="25" r="3.5" stroke="currentColor" stroke-width="2" />
      <path d="M15.5 33c.8-2.6 3-4 5.5-4s4.7 1.4 5.5 4" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
      <circle cx="36" cy="25.5" r="3.5" stroke="currentColor" stroke-width="2" />
      <path d="M38.6 28.1l2.4 2.4" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
    </svg>
  );
}

// The preset is registered only where it can be honoured: when this server offers a
// remote source. It is never rendered, and no schema names its type.
function presetFor(sources) {
  function DirectoryPicker(props) {
    return <Select { ...props } />;
  }
  DirectoryPicker.config = {
    type: PRESET_TYPE,
    keyed: true,
    name: 'Directory picker',
    group: 'selection',
    icon: DirectoryPickerIcon,
    create: (options = {}) => {
      const provider = providersOf(sources)[0];
      const rest = { ...options };
      delete rest.type;
      return {
        label: 'Person',
        ...rest,
        type: 'select',
        [DATA_SOURCE_MEMBER]: defaultDataSource(provider, firstOpenWorker(sources, provider))
      };
    }
  };
  return DirectoryPicker;
}

class AtlasRemotePalette {
  constructor(formFields, injector) {
    const sources = injector.get('atlasRemoteSources', false);
    if (providersOf(sources).length) {
      formFields.register(PRESET_TYPE, presetFor(sources));
    }
  }
}

AtlasRemotePalette.$inject = [ 'formFields', 'injector' ];

// AtlasEditorModule is the editor's part. The form editor loads it with the viewer's part
// (playground.js), whose select the canvas renders.
export const AtlasEditorModule = {
  __init__: [ 'atlasRemotePalette', 'atlasRemoteBehavior', 'atlasDecisionBehavior', 'atlasRemotePropertiesProvider' ],
  atlasRemotePalette: [ 'type', AtlasRemotePalette ],
  atlasRemoteBehavior: [ 'type', AtlasRemoteBehavior ],
  atlasDecisionBehavior: [ 'type', AtlasDecisionBehavior ],
  atlasRemotePropertiesProvider: [ 'type', AtlasRemotePropertiesProvider ]
};

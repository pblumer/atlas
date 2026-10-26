// The viewer's half of a select whose options come from a remote data source (ADR-0473).
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

import { useContext, useEffect, useRef } from 'preact/hooks';
import {
  Description,
  Errors,
  FormContext,
  Label,
  Select
} from '@bpmn-io/form-js-viewer';

import { DATA_SOURCE_MEMBER, isRemoteSelect } from './remote-source.js';
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

// AtlasSelect is what renders every select: form-js's own, unless its options come from a
// remote data source.
export function AtlasSelect(props) {
  if (isRemoteSelect(props.field)) {
    return <RemoteSelect { ...props } />;
  }
  return <Select { ...props } />;
}

AtlasSelect.config = {
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

function hasRemoteSource(options) {
  const block = options[DATA_SOURCE_MEMBER];
  return !!block && typeof block === 'object' &&
    options.values === undefined && options.valuesKey === undefined && options.valuesExpression === undefined;
}

class AtlasRemoteFields {
  constructor(formFields) {
    formFields.register('select', AtlasSelect);
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

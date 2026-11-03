/** @jsx createElement */
// The boxed list editor (ADR-draft-form-options-from-a-decision): a decision whose logic
// is a dmn:List, edited entry by entry.
//
// The fork renders a list in the boxed-expression view and edits each entry with the
// literal-expression editor it gives every literal expression; what it has no control
// for is the list itself. This adds it — an entry added at the end, moved up or down,
// removed — as a component of higher priority than the fork's own list renderer, so the
// fork draws the entries and this draws the list around them. Every change is one
// `element.updateProperties` on the list: one step on the view's undo stack, and the same
// command the fork's own editors issue.
//
// The header of a list decision is the decision's name and nothing else. The fork's
// header for a decision adds an "Expression type" select that turns the decision into a
// For, Every, Some or Filter, which this Modeler does not open; choosing one would leave a
// decision nobody can open again. A decision's kind is changed where every other
// decision's is, in the requirements graph's replace menu, which offers List
// (drd-list.js). A business knowledge model keeps the fork's header as it is.
//
// Written with inferno-create-element rather than the fork's Babel JSX transform, because
// this file is bundled by esbuild straight from source; both produce Inferno vnodes for
// the one Inferno the fork resolves to.

import { createElement } from 'inferno-create-element';
import { Component } from 'inferno';

import { is } from 'dmn-js-shared/lib/util/ModelUtil';
import Input from 'dmn-js-shared/lib/components/Input';
import ElementPropertiesEditorComponent
  from 'dmn-js-boxed-expression/lib/features/element-properties/components/ElementPropertiesEditorComponent';

// Above the fork's list renderer, which registers at the default priority.
const LIST_PRIORITY = 1500;

const isListDecision = (element) => is(element, 'dmn:Decision') && is(element.decisionLogic, 'dmn:List');

// Changes re-renders a component when one of the elements it depends on changes — the
// fork's withChangeSupport, which it does not export from its package entry.
class Changes extends Component {
  constructor(props, context) {
    super(props, context);
    this._eventBus = context.injector.get('eventBus');
    this.onChange = ({ elements }) => {
      if (this.props.dependencies().some((d) => elements.includes(d))) {
        this.forceUpdate();
      }
    };
  }

  componentDidMount() {
    this._eventBus.on('elements.changed', this.onChange);
  }

  componentWillUnmount() {
    this._eventBus.off('elements.changed', this.onChange);
  }

  render() {
    return this.props.render();
  }
}

// ---------- the list -------------------------------------------------------------------

export class ListEditor {
  static $inject = [ 'modeling', 'dmnFactory', 'moddle' ];

  constructor(modeling, dmnFactory, moddle) {
    this._modeling = modeling;
    this._dmnFactory = dmnFactory;
    this._moddle = moddle;
  }

  entries(list) {
    return (list.get('elements') || []).slice();
  }

  // add appends an empty FEEL literal expression and returns it.
  add(list) {
    const entry = this._dmnFactory.create('dmn:LiteralExpression', { text: '' });
    const ids = this._moddle && this._moddle.ids;
    if (ids && typeof ids.nextPrefixed === 'function') {
      entry.id = ids.nextPrefixed('LiteralExpression_', entry);
    }
    entry.$parent = list;
    this._set(list, [ ...this.entries(list), entry ]);
    return entry;
  }

  remove(list, index) {
    this._set(list, this.entries(list).filter((_, i) => i !== index));
  }

  move(list, index, by) {
    const next = this.entries(list);
    const to = index + by;
    if (to < 0 || to >= next.length) return;
    [ next[index], next[to] ] = [ next[to], next[index] ];
    this._set(list, next);
  }

  _set(list, elements) {
    this._modeling.updateProperties(list, { elements });
  }
}

function Entry({ expression }, { injector }) {
  if (!expression) return null;
  const Expression = injector.get('components').getComponent('expression', { expression });
  return Expression ? <Expression expression={ expression } /> : null;
}

function ListEditorComponent({ expression: list }, { injector }) {
  const listEditor = injector.get('listEditor');
  const translate = injector.get('translate');
  return (
    <Changes
      dependencies={ () => [ list ] }
      render={ () => {
        const entries = listEditor.entries(list);
        const last = entries.length - 1;
        return (
          <div className="list-expression atlas-list-editor">
            { entries.length ? null : <p className="atlas-list-empty">{ translate('This list has no entries yet.') }</p> }
            { entries.map((entry, index) => (
              <div className="list-entry atlas-list-entry" key={ entry.id || index } data-index={ index }>
                <span className="atlas-list-index" aria-hidden="true">{ index + 1 }</span>
                <div className="atlas-list-value"><Entry expression={ entry } /></div>
                <div className="atlas-list-actions">
                  <button
                    type="button"
                    className="btn ghost small atlas-list-up"
                    title={ translate('Move entry up') }
                    aria-label={ translate('Move entry {index} up', { index: index + 1 }) }
                    disabled={ index === 0 }
                    onClick={ () => listEditor.move(list, index, -1) }>↑</button>
                  <button
                    type="button"
                    className="btn ghost small atlas-list-down"
                    title={ translate('Move entry down') }
                    aria-label={ translate('Move entry {index} down', { index: index + 1 }) }
                    disabled={ index === last }
                    onClick={ () => listEditor.move(list, index, 1) }>↓</button>
                  <button
                    type="button"
                    className="btn ghost small atlas-list-remove"
                    title={ translate('Remove entry') }
                    aria-label={ translate('Remove entry {index}', { index: index + 1 }) }
                    onClick={ () => listEditor.remove(list, index) }>×</button>
                </div>
              </div>
            )) }
            <button type="button" className="btn ghost small atlas-list-add" onClick={ () => listEditor.add(list) }>
              { translate('Add entry') }
            </button>
          </div>
        );
      } } />
  );
}

class ListEditorComponentProvider {
  static $inject = [ 'components' ];

  constructor(components) {
    components.onGetComponent('expression', LIST_PRIORITY, ({ expression }) => {
      if (is(expression, 'dmn:List')) {
        return ListEditorComponent;
      }
    });
  }
}

// ---------- undo and redo -----------------------------------------------------------------

const isEditable = (node) => !!node && node.nodeType === 1 &&
  (node.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(node.tagName));

// ListUndoKeys gives a list decision Ctrl+Z and Ctrl+Y (Ctrl+Shift+Z): what adding,
// moving and removing entries does is on the view's command stack, but diagram-js binds
// its keyboard to a canvas, and the boxed-expression view has none. The keys are taken
// while the view is on the page and its root is a list decision, from anywhere in it —
// or from the page itself, where focus lands when the button that was pressed went with
// the entry it removed. Inside an entry's FEEL editor they stay the editor's own, which
// undoes the typing.
class ListUndoKeys {
  static $inject = [ 'eventBus', 'commandStack', 'viewer', 'config.renderer' ];

  constructor(eventBus, commandStack, viewer, renderer) {
    const container = renderer && renderer.container;
    if (!container || typeof document === 'undefined') return;
    const onKey = (event) => {
      if (!(event.ctrlKey || event.metaKey) || !container.isConnected) return;
      const key = String(event.key || '').toLowerCase();
      if (key !== 'z' && key !== 'y') return;
      const target = event.target;
      if (isEditable(target) || !(target === document.body || container.contains(target))) return;
      if (!isListDecision(viewer.getRootElement())) return;
      event.preventDefault();
      if (key === 'y' || event.shiftKey) {
        commandStack.redo();
      } else {
        commandStack.undo();
      }
    };
    document.addEventListener('keydown', onKey);
    eventBus.on('viewer.destroy', () => document.removeEventListener('keydown', onKey));
  }
}

// ---------- the header -------------------------------------------------------------------

function ListDecisionName(_, { injector }) {
  const viewer = injector.get('viewer');
  const modeling = injector.get('modeling');
  const translate = injector.get('translate');
  const decision = viewer.getRootElement();
  return (
    <Changes
      dependencies={ () => [ decision ] }
      render={ () => (
        <div className="element-properties">
          <Input
            label={ translate('Element name') }
            className="element-name editor"
            value={ decision.get('name') }
            onChange={ (name) => modeling.updateProperties(decision, { name }) } />
        </div>
      ) } />
  );
}

function Header(props, context) {
  const root = context.injector.get('viewer').getRootElement();
  if (isListDecision(root)) {
    return <ListDecisionName />;
  }
  return <ElementPropertiesEditorComponent { ...props } />;
}

// AtlasElementProperties takes the place of the fork's element-properties editor, whose
// only work is registering the header above.
class AtlasElementProperties {
  static $inject = [ 'components' ];

  constructor(components) {
    components.onGetComponent('header', () => Header);
  }
}

export default {
  __init__: [ 'listEditorComponentProvider', 'elementProperties', 'atlasListUndoKeys' ],
  listEditor: [ 'type', ListEditor ],
  listEditorComponentProvider: [ 'type', ListEditorComponentProvider ],
  elementProperties: [ 'type', AtlasElementProperties ],
  atlasListUndoKeys: [ 'type', ListUndoKeys ]
};

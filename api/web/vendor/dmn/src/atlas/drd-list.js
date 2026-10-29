// The requirements graph's half of the boxed list (ADR-draft-form-options-from-a-decision):
// a decision's replace menu offers List beside Decision table and Literal expression,
// and a decision whose logic is a list carries the overlay that opens it, as one with a
// table or a literal expression does.
//
// The replace builds the new decision the way the fork's DrdReplace builds one — a new
// business object with the old one's name and variable — and hands it to the same
// `shape.replace`, so the fork's ReplaceElementBehaviour gives it back its id and the
// requirements stay attached: one command, undone as one. The list starts with one empty
// entry, so the editor opens on something to type into.

import { is } from 'dmn-js-shared/lib/util/ModelUtil';

const LIST_ICON = 'atlas-icon-list';

const isListDecision = (bo) => is(bo, 'dmn:Decision') && is(bo.decisionLogic, 'dmn:List');

class ListReplaceMenuProvider {
  static $inject = [ 'popupMenu', 'drdFactory', 'replace', 'rules', 'translate' ];

  constructor(popupMenu, drdFactory, replace, rules, translate) {
    this._drdFactory = drdFactory;
    this._replace = replace;
    this._rules = rules;
    this._translate = translate;
    popupMenu.registerProvider('dmn-replace', this);
  }

  getPopupMenuEntries(element) {
    const bo = element && element.businessObject;
    if (!is(bo, 'dmn:Decision') || isListDecision(bo) || !this._rules.allowed('shape.replace', { element })) {
      return {};
    }
    return {
      'replace-with-list': {
        label: this._translate('List'),
        className: LIST_ICON,
        action: () => this.replaceWithList(element)
      }
    };
  }

  replaceWithList(element) {
    const factory = this._drdFactory;
    const old = element.businessObject;
    const decision = factory.create('dmn:Decision');
    decision.name = old.name;
    if (old.variable) {
      decision.variable = old.variable;
      old.variable.$parent = decision;
    }
    const list = factory.create('dmn:List');
    const entry = factory.create('dmn:LiteralExpression', { text: '' });
    entry.$parent = list;
    list.get('elements').push(entry);
    list.$parent = decision;
    decision.decisionLogic = list;
    return this._replace.replaceElement(element, {
      type: 'dmn:Decision',
      businessObject: decision,
      width: element.width,
      height: element.height
    });
  }
}

// ListDrillDown adds the overlay that opens a list decision, beside the fork's two for a
// table and a literal expression.
class ListDrillDown {
  static $inject = [ 'eventBus', 'drillDown', 'translate' ];

  constructor(eventBus, drillDown, translate) {
    eventBus.on('shape.added', ({ element }) => {
      if (isListDecision(element && element.businessObject)) {
        drillDown.addOverlay(element, LIST_ICON, translate('Open list'));
      }
    });
  }
}

export default {
  __init__: [ 'atlasListReplaceMenuProvider', 'atlasListDrillDown' ],
  atlasListReplaceMenuProvider: [ 'type', ListReplaceMenuProvider ],
  atlasListDrillDown: [ 'type', ListDrillDown ]
};

// The dmn-js Modeler Atlas ships: the fork's, with a decision whose logic is a boxed
// list (DMN 1.3+ §10.3.2.7) opened and edited like a table or a literal expression
// (ADR-draft-form-options-from-a-decision).
//
// The fork's boxed-expression editor already edits a decision as its root and already
// renders a dmn:List, but the Modeler opens that editor for a business knowledge model
// only, so a list could be written in the XML source and nowhere else. Two things change
// here and nothing in dmn-js does:
//
//   - the boxedExpression view also opens a decision whose logic is a list;
//   - each view that needs it is given Atlas's module for it — the list editor for the
//     boxed-expression view (list-editor.jsx), and for the requirements graph the List
//     entry in a decision's replace menu and the overlay that drills down into one
//     (drd-list.js).
//
// The modules are added to whatever the caller passes for those views, so a page keeps
// passing its own (the translate service, the properties panel) as it did.

import Modeler from 'dmn-js/lib/Modeler';
import { is } from 'dmn-js-shared/lib/util/ModelUtil';

import ListEditorModule from './list-editor.jsx';
import DrdListModule from './drd-list.js';

// isListDecision reports whether an element is a decision whose logic is a boxed list.
export function isListDecision(element) {
  return is(element, 'dmn:Decision') && is(element.decisionLogic, 'dmn:List');
}

function withModule(options, view, module) {
  const own = options[view] || {};
  return { ...own, additionalModules: [ ...(own.additionalModules || []), module ] };
}

export default class AtlasModeler extends Modeler {
  constructor(options = {}) {
    super({
      ...options,
      drd: withModule(options, 'drd', DrdListModule),
      boxedExpression: withModule(options, 'boxedExpression', ListEditorModule)
    });
  }

  _getViewProviders() {
    return super._getViewProviders().map((provider) => {
      if (provider.id !== 'boxedExpression') return provider;
      const opens = provider.opens;
      return {
        ...provider,
        opens(element) {
          return isListDecision(element) || opens(element);
        }
      };
    });
  }
}

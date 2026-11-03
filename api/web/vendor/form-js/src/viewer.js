// The entry of form-viewer.js: form-js's viewer, with Atlas's select (ADR-0473) and its
// e-mail view (ADR-draft-mail-html-view).
//
// Every form this bundle renders can show a select whose options come from a remote
// data source. The page provides the search as the `atlasRemote` option — an adapter the
// field mounts into its element (api/web/remote-select.js); without one such a select is
// a box for the id. `atlasLocale` ("de" or "en") is the language the e-mail view speaks;
// German without one.
//
// A field whose options come from a decision (ADR-draft-form-options-from-a-decision)
// asks the `atlasDecisions` option for them: an object whose options(decisionId) answers
// a promise of the decision's list as [{label, value}]. Without one such a field says its
// options could not be loaded — which a public page never sees, because the server
// writes the options into the form it serves there.

import './atlas/remote-source.js';
import './atlas/decision-source.js';

import { Form as BaseForm } from '@bpmn-io/form-js-viewer';

import { AtlasViewerModule } from './atlas/viewer.jsx';

export class Form extends BaseForm {
  constructor(options = {}) {
    const { atlasRemote, atlasDecisions, atlasLocale, additionalModules = [], ...rest } = options;
    super({
      ...rest,
      additionalModules: [
        AtlasViewerModule,
        ...(atlasRemote ? [ { atlasRemote: [ 'value', atlasRemote ] } ] : []),
        ...(atlasDecisions ? [ { atlasDecisions: [ 'value', atlasDecisions ] } ] : []),
        ...(atlasLocale ? [ { atlasLocale: [ 'value', atlasLocale ] } ] : []),
        ...additionalModules
      ]
    });
  }
}

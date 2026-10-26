// The entry of form-viewer.js: form-js's viewer, with Atlas's select (ADR-0473) and its
// e-mail view (ADR-draft-mail-html-view).
//
// Every form this bundle renders can show a select whose options come from a remote
// data source. The page provides the search as the `atlasRemote` option — an adapter the
// field mounts into its element (api/web/remote-select.js); without one such a select is
// a box for the id. `atlasLocale` ("de" or "en") is the language the e-mail view speaks;
// German without one.

import './atlas/remote-source.js';

import { Form as BaseForm } from '@bpmn-io/form-js-viewer';

import { AtlasViewerModule } from './atlas/viewer.jsx';

export class Form extends BaseForm {
  constructor(options = {}) {
    const { atlasRemote, atlasLocale, additionalModules = [], ...rest } = options;
    super({
      ...rest,
      additionalModules: [
        AtlasViewerModule,
        ...(atlasRemote ? [ { atlasRemote: [ 'value', atlasRemote ] } ] : []),
        ...(atlasLocale ? [ { atlasLocale: [ 'value', atlasLocale ] } ] : []),
        ...additionalModules
      ]
    });
  }
}

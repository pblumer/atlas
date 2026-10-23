// The entry of form-viewer.js: form-js's viewer, with Atlas's select (ADR-0473).
//
// Every form this bundle renders can show a select whose options come from a remote
// data source. The page provides the search as the `atlasRemote` option — an adapter the
// field mounts into its element (api/web/remote-select.js); without one such a select is
// a box for the id.

import './atlas/remote-source.js';

import { Form as BaseForm } from '@bpmn-io/form-js-viewer';

import { AtlasViewerModule } from './atlas/viewer.jsx';

export class Form extends BaseForm {
  constructor(options = {}) {
    const { atlasRemote, additionalModules = [], ...rest } = options;
    super({
      ...rest,
      additionalModules: [
        AtlasViewerModule,
        ...(atlasRemote ? [ { atlasRemote: [ 'value', atlasRemote ] } ] : []),
        ...additionalModules
      ]
    });
  }
}

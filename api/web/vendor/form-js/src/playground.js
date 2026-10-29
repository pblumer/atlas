// The entry of form-playground.js: form-js's playground — the editor with its live
// preview — with Atlas's select (ADR-0473).
//
// options.atlas carries what only the page knows:
//
//   sources  the answer of GET /api/v1/form-data/sources: the remote data sources this
//            server offers and the Workers a form may name. The properties panel and
//            the palette offer what it lists.
//   remote   the adapter the preview's remote selects search through. A design-time
//            preview is given made-up entries, never a live directory.
//   locale   the language the e-mail view speaks ("de" or "en"); German without one.
//   decisions the answer of GET /api/v1/form-data/decisions: the decisions a field's
//            options can come from, which the properties panel offers.
//   decisionOptions the service the canvas, the preview and the panel ask a decision's
//            list of (viewer.js says what it answers).

import './atlas/remote-source.js';
import './atlas/decision-source.js';

import { Playground as BasePlayground } from '@bpmn-io/form-js-playground';

import { AtlasViewerModule } from './atlas/viewer.jsx';
import { AtlasEditorModule } from './atlas/editor.jsx';

export class Playground extends BasePlayground {
  constructor(options = {}) {
    const { atlas = {}, viewerAdditionalModules = [], editorAdditionalModules = [], ...rest } = options;
    super({
      ...rest,
      viewerAdditionalModules: [
        AtlasViewerModule,
        ...(atlas.remote ? [ { atlasRemote: [ 'value', atlas.remote ] } ] : []),
        ...(atlas.decisionOptions ? [ { atlasDecisions: [ 'value', atlas.decisionOptions ] } ] : []),
        ...(atlas.locale ? [ { atlasLocale: [ 'value', atlas.locale ] } ] : []),
        ...viewerAdditionalModules
      ],
      editorAdditionalModules: [
        AtlasViewerModule,
        AtlasEditorModule,
        { atlasRemoteSources: [ 'value', atlas.sources || null ] },
        { atlasDecisionList: [ 'value', atlas.decisions || null ] },
        ...(atlas.decisionOptions ? [ { atlasDecisions: [ 'value', atlas.decisionOptions ] } ] : []),
        ...(atlas.locale ? [ { atlasLocale: [ 'value', atlas.locale ] } ] : []),
        ...editorAdditionalModules
      ]
    });
  }
}

@bpmn-io/form-js 1.24.0 (viewer, editor, playground)
Source: https://registry.npmjs.org/@bpmn-io/form-js-viewer

Vendored as self-contained ESM bundles, because the published dist/index.es.js
externalizes its dependencies (preact, luxon, flatpickr, min-dash, big.js,
lodash/isEqual, ids, classnames). The bundles inline them so they load buildless
(ADR-0012), the same way bpmn-js is vendored (ADR-0013).

  - form-viewer.js renders a form: form-js's Form, with Atlas's select whose
    options can come from a remote data source (ADR-0473) and Atlas's e-mail view
    (ADR-draft-mail-html-view).
  - form-playground.js is the form editor: form-js's Playground — the split
    editor, preview, input and output surface of the reference modeler — with the
    same select and the properties that edit its remote source.

What is in them, the exact recipe that builds them, and the checksums the build
produces are in ATLAS-VENDORED.txt; the build inputs, Atlas's own modules among
them, are in src/. Never edit a bundle by hand.

form-js.css is dist/assets/form-js.css verbatim (no external url()/@import,
so it is CSP-safe). form-playground.css concatenates form-js.css,
form-js-editor-base.css, form-js-editor.css, draggle.css, properties-panel.css,
and form-js-playground.css — all verbatim, all CSP-safe. License: see LICENSE
(MIT-style; Camunda Services GmbH).

@bpmn-io/form-js-viewer 1.24.0
Source: https://registry.npmjs.org/@bpmn-io/form-js-viewer

Vendored as a single self-contained ESM bundle, because the published
dist/index.es.js externalizes its dependencies (preact, luxon, flatpickr,
min-dash, big.js, lodash/isEqual, ids, classnames). The bundle here inlines
them so it loads buildless (ADR-0012), the same way bpmn-js is vendored
(ADR-0013). It was produced with:

  echo "export { Form } from '@bpmn-io/form-js-viewer';" > entry.js
  esbuild entry.js --bundle --format=esm --minify \
    --outfile=form-viewer.js --legal-comments=none

form-js.css is dist/assets/form-js.css verbatim (no external url()/@import,
so it is CSP-safe). License: see LICENSE (MIT-style; Camunda Services GmbH).

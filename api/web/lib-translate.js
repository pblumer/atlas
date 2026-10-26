// The translate service the vendored modelers call (bpmn-js, dmn-js), fed from the
// console's catalogue (ADR-0267, ADR-draft-console-locales-one-file-each).
//
// diagram-js and table-js ask a `translate` service for every word they draw — the
// palette, the context pad, the popup menus, the properties panel — passing their
// own English template and its replacements: translate("Append {type}", { type }).
// The default service returns the template. This one looks the template up in the
// catalogue as `lib.<template>`, so a library's words sit in the same locale files
// as Atlas's own and are held to the same completeness test.
//
// A template the catalogue does not carry is shown as the library wrote it. That is
// the one place a missing message does not render as its key: the key would be the
// English sentence with `lib.` in front of it, which tells a reader nothing the
// sentence does not. Leaving a library word untranslated is a deliberate choice for
// some — FEEL's keywords, the DMN hit policies' standard names — and simply not yet
// done for the rest.

import { t } from "./i18n.js";

// libraryTranslate is the service: template plus replacements in, words out.
export function libraryTranslate(template, replacements) {
  const key = "lib." + template;
  let text = t(key);
  if (text === key) text = template;
  const values = replacements || {};
  return text.replace(/{([^}]+)}/g, (whole, name) => (name in values ? values[name] : whole));
}

// translateModule is the didi module a modeler takes in its additionalModules: it
// overrides the library's own `translate` service.
export const translateModule = { translate: ["value", libraryTranslate] };

// PanelGroupLabels translates the headings of a properties panel's groups. The
// panels (dmn-js-properties-panel, bpmn-js-properties-panel) pass each entry's label
// through translate, but write a group's heading — General, Documentation — as a
// constant. A provider registered at priority 1 runs after every other one, so it
// sees all their groups and only relabels them; it adds and removes nothing.
function PanelGroupLabels(propertiesPanel) {
  propertiesPanel.registerProvider(1, this);
}
PanelGroupLabels.$inject = ["propertiesPanel"];
PanelGroupLabels.prototype.getGroups = function () {
  return (groups) => groups.map((g) => (g && typeof g.label === "string" ? { ...g, label: libraryTranslate(g.label) } : g));
};

// panelGroupsModule is that provider as a didi module, for a modeler that shows a
// properties panel.
export const panelGroupsModule = { __init__: ["atlasPanelGroupLabels"], atlasPanelGroupLabels: ["type", PanelGroupLabels] };

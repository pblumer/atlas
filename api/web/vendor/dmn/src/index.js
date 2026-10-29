// The DmnJS Atlas ships is the fork's Modeler with Atlas's modules added
// (atlas/modeler.js): the boxed list editor (ADR-draft-form-options-from-a-decision).
export { default as DmnJS } from './atlas/modeler.js';

export {
  DmnPropertiesPanelModule,
  DmnPropertiesProviderModule,
  CamundaPropertiesProviderModule
} from 'dmn-js-properties-panel';

export { default as CamundaModdleDescriptor }
  from 'camunda-dmn-moddle/resources/camunda.json';

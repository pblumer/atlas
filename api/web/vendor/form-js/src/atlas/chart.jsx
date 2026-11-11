// The chart: a form component that shows one dataset of a report variable (ADR-0555 §4).
//
// A report is a snapshot a service task computed into a variable in the contract
// `atlas.report/1` (chart-layout.js). The chart names that variable by its key, like the
// e-mail view does, so the server lets the task's holder read it; it is not keyed for
// form-js, so the form never submits it. It draws what chart-layout.js lays out, and
// nothing else: a report that is missing, of another schema, or past the contract's
// limits is a framed message in the chart's place, never an empty box.

import { useContext, useLayoutEffect, useRef, useState } from 'preact/hooks';
import { FormContext, Label } from '@bpmn-io/form-js-viewer';

import { layoutChart, readDataset, readReport } from './chart-layout.js';

export const CHART_TYPE = 'chart';

const STRINGS = {
  de: {
    asTable: 'Als Tabelle', asChart: 'Als Diagramm',
    missing: 'Kein Bericht in «{key}».',
    notReport: '«{key}» ist kein Bericht.',
    schema: '«{key}» ist ein Bericht im Format «{detail}»; dieses Diagramm liest atlas.report/1.',
    noDatasets: 'Der Bericht in «{key}» hat keine Datensätze.',
    tooManyDatasets: 'Der Bericht in «{key}» hat mehr als {detail} Datensätze.',
    noDataset: 'Der Bericht hat keinen Datensatz «{detail}».',
    noDatasetName: 'Dem Diagramm ist kein Datensatz zugeordnet.',
    badValue: 'Der Datensatz «{detail}» enthält einen Wert, der keine Zahl ist.',
    badSeries: 'Der Datensatz «{detail}» ist keine gültige Reihe: Jede Reihe braucht so viele Werte, wie es Kategorien gibt.',
    tooManyCategories: 'Der Datensatz hat mehr als {detail} Kategorien. Fassen Sie ihn im Bericht zusammen.',
    tooManySeries: 'Der Datensatz hat mehr als {detail} Reihen. Fassen Sie ihn im Bericht zusammen.',
    needsSeries: 'Dieser Diagrammtyp braucht einen Datensatz mit Kategorien und Reihen.',
    generated: 'Stand {when}'
  },
  en: {
    asTable: 'As table', asChart: 'As chart',
    missing: 'No report in “{key}”.',
    notReport: '“{key}” is not a report.',
    schema: '“{key}” is a report in the format “{detail}”; this chart reads atlas.report/1.',
    noDatasets: 'The report in “{key}” has no datasets.',
    tooManyDatasets: 'The report in “{key}” has more than {detail} datasets.',
    noDataset: 'The report has no dataset “{detail}”.',
    noDatasetName: 'No dataset is chosen for this chart.',
    badValue: 'The dataset “{detail}” holds a value that is not a number.',
    badSeries: 'The dataset “{detail}” is not a valid table: every series needs as many values as there are categories.',
    tooManyCategories: 'The dataset has more than {detail} categories. Aggregate it in the report.',
    tooManySeries: 'The dataset has more than {detail} series. Aggregate it in the report.',
    needsSeries: 'This chart type needs a dataset with categories and series.',
    generated: 'As of {when}'
  }
};

function say(t, code, vars) {
  return (t[code] || code).replace('{key}', vars.key || '').replace('{detail}', vars.detail || '');
}

function fmtWhen(iso, locale) {
  const d = new Date(iso);
  if (!iso || Number.isNaN(d.getTime())) return '';
  return d.toLocaleString(locale === 'en' ? 'en-GB' : 'de-CH', { dateStyle: 'medium', timeStyle: 'short' });
}

// Prim draws one primitive of the layout. A mark with a `tip` carries it as an SVG
// <title>, the browser's own tooltip, so every value can be read by pointing at it.
function Prim({ p }) {
  const cls = 'atlas-chart-' + p.cls;
  if (p.t === 'rect') {
    return (
      <rect class={ cls } x={ p.x } y={ p.y } width={ p.w } height={ p.h } rx={ p.r || 0 }>
        { p.tip ? <title>{ p.tip }</title> : null }
      </rect>
    );
  }
  if (p.t === 'hit') {
    return (
      <rect class="atlas-chart-hit" x={ p.x } y={ p.y } width={ p.w } height={ p.h }>
        <title>{ p.tip }</title>
      </rect>
    );
  }
  if (p.t === 'line') {
    return <line class={ cls } x1={ p.x1 } y1={ p.y1 } x2={ p.x2 } y2={ p.y2 } stroke-width={ p.w || 1 } />;
  }
  if (p.t === 'poly') {
    const pts = p.pts.map(([ x, y ]) => `${x.toFixed(1)},${y.toFixed(1)}`).join(' ');
    return p.fill
      ? <polygon class={ cls } points={ pts } />
      : <polyline class={ cls } points={ pts } fill="none" stroke-width={ p.w || 2 } stroke-linejoin="round" stroke-linecap="round" />;
  }
  if (p.t === 'text') {
    return (
      <text class={ cls } x={ p.x } y={ p.y } font-size={ p.size } font-weight={ p.weight || 400 } text-anchor={ p.anchor || 'start' }>
        { p.s }
      </text>
    );
  }
  return null;
}

function ChartTable({ table, caption }) {
  return (
    <table class="atlas-chart-table">
      { caption ? <caption>{ caption }</caption> : null }
      <thead><tr>{ table.head.map((h, i) => <th key={ i } scope="col">{ h }</th>) }</tr></thead>
      <tbody>
        { table.rows.map((r, i) => (
          <tr key={ i }>{ r.map((c, j) => (j === 0 ? <th key={ j } scope="row">{ c }</th> : <td key={ j }>{ c }</td>)) }</tr>
        )) }
      </tbody>
    </table>
  );
}

// useWidth follows the width the chart is given, so it lays out to its column rather
// than to a fixed size.
function useWidth(ref) {
  const [ w, setW ] = useState(560);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return undefined;
    const measure = () => { const cw = el.clientWidth; if (cw > 0) setW(Math.round(cw)); };
    measure();
    if (typeof ResizeObserver === 'undefined') return undefined;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [ ref ]);
  return w;
}

export function Chart(props) {
  const { field, domId, indexes } = props;
  const { getService } = useContext(FormContext);
  const form = getService('form', false);
  const locale = getService('atlasLocale', false) || 'de';
  const marks = getService('atlasNumberMarks', false) || undefined;
  const t = STRINGS[locale] || STRINGS.de;
  const box = useRef(null);
  const width = useWidth(box);
  const [ asTable, setAsTable ] = useState(false);
  const key = (field.key || '').trim();

  // The variable is read where a field's value is, below the paths of its groups.
  const raw = readRaw(form, getService('pathRegistry', false), field, indexes);
  // The report's notes and its date are said once, under the first chart of the form
  // that draws this report: they belong to the report, and a truncation note is not
  // lost by being said once, only by being said nowhere.
  const firstOfReport = firstChartFor(form, key) === field.id;
  let content;
  let notes = [];
  let generatedAt = '';
  let title = field.title || '';
  const r = readReport(raw);
  let laid = null;
  let error = r.error;
  if (!error) {
    const rep = r.report;
    notes = Array.isArray(rep.notes) ? rep.notes.filter((n) => typeof n === 'string') : [];
    generatedAt = typeof rep.generatedAt === 'string' ? rep.generatedAt : '';
    if (!field.dataset) {
      error = { code: 'noDatasetName' };
    } else {
      const d = readDataset(rep, field.dataset);
      error = d.error;
      if (!error) {
        laid = layoutChart(field, d.dataset, { width, locale, marks });
        error = laid.error;
      }
    }
  }
  if (error) {
    content = <div class="atlas-chart-error" role="note">{ say(t, error.code, { key, detail: error.detail }) }</div>;
  } else {
    content = asTable
      ? <ChartTable table={ laid.table } caption={ title } />
      : (
        <svg class="atlas-chart-svg" role="img" width={ laid.width } height={ laid.height } viewBox={ `0 0 ${laid.width} ${laid.height}` }>
          <title>{ title || field.dataset }</title>
          { laid.prims.map((p, i) => <Prim key={ i } p={ p } />) }
        </svg>
      );
  }

  return (
    <div class="fjs-form-field atlas-chart" id={ domId } data-chart-type={ field.chartType || 'bar' }>
      <div class="atlas-chart-head">
        { title ? <Label label={ title } /> : null }
        { !error
          ? (
            <button type="button" class="atlas-chart-toggle" aria-pressed={ asTable ? 'true' : 'false' } onClick={ () => setAsTable(!asTable) }>
              { asTable ? t.asChart : t.asTable }
            </button>
          )
          : null }
      </div>
      <div class="atlas-chart-box" ref={ box }>{ content }</div>
      { !error && !asTable && laid.legend.length
        ? (
          <ul class="atlas-chart-legend">
            { laid.legend.map((l, i) => <li key={ i }><i class={ 'atlas-chart-swatch atlas-chart-' + l.cls } />{ l.name }</li>) }
          </ul>
        )
        : null }
      { !error && firstOfReport && (notes.length || generatedAt)
        ? (
          <div class="atlas-chart-notes">
            { notes.map((n, i) => <p key={ i }>{ n }</p>) }
            { generatedAt ? <p>{ t.generated.replace('{when}', fmtWhen(generatedAt, locale)) }</p> : null }
          </div>
        )
        : null }
    </div>
  );
}

// readRaw answers the value at the field's key as the form holds it: an object, or a
// JSON string a variable may arrive as, which readReport parses.
function readRaw(form, pathRegistry, field, indexes) {
  const key = (field.key || '').trim();
  if (!form || !key) return undefined;
  let path = key.split('.');
  if (pathRegistry && typeof pathRegistry.getValuePath === 'function') {
    path = pathRegistry.getValuePath(field, { replacements: { [field.id]: key }, indexes }) || path;
  }
  let v = form._getState().data;
  for (const seg of path) {
    if (v === null || typeof v !== 'object') return undefined;
    v = v[seg];
  }
  return v;
}

// firstChartFor answers the id of the first chart in the form's schema that draws the
// report under key, in reading order.
function firstChartFor(form, key) {
  const schema = form && form._getState ? form._getState().schema : null;
  const walk = (list) => {
    for (const c of Array.isArray(list) ? list : []) {
      if (c && c.type === CHART_TYPE && (c.key || '').trim() === key) return c.id;
      const inner = c && walk(c.components);
      if (inner) return inner;
    }
    return null;
  };
  return schema ? walk(schema.components) : null;
}

function ChartIcon(props) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" width="54" height="54" viewBox="0 0 54 54" fill="none" { ...props }>
      <path d="M12 41h30" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
      <rect x="15" y="27" width="5" height="12" rx="1" fill="currentColor" />
      <rect x="24" y="19" width="5" height="20" rx="1" fill="currentColor" />
      <rect x="33" y="23" width="5" height="16" rx="1" fill="currentColor" />
    </svg>
  );
}

Chart.config = {
  type: CHART_TYPE,
  // Not keyed: form-js submits keyed fields only, and a report is shown, never answered.
  keyed: false,
  name: 'Chart',
  group: 'presentation',
  icon: ChartIcon,
  create: (options = {}) => ({ key: 'report', dataset: '', chartType: 'bar', title: '', ...options })
};


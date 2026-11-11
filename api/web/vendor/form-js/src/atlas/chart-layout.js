// The chart component's geometry (ADR-0555 §2, §4): the report contract, read and
// checked, and the five chart types laid out as a list of primitives — rectangle, line,
// polyline, text — in points. It imports nothing: the form component draws the list as
// SVG, and the PDF writer will draw the same list, so print and screen cannot disagree.
//
// A primitive names its colour by role (`cls`), never by value: `s0`…`s7` are the
// categorical slots in fixed order, `accent` the single series, and `axis`, `grid`,
// `ink`, `muted`, `onfill`, `track`, `target`, `area` the chrome. The stylesheet maps the
// roles to the theme's colours, in light and in dark.

export const REPORT_SCHEMA = 'atlas.report/1';
export const CHART_TYPES = [ 'kpi', 'bar', 'stackedBar', 'line', 'bullet' ];
export const LIMITS = { categories: 50, series: 8, datasets: 20 };

const UNIT_RE = /^(count|percent|ms|days|currency:[A-Z]{3})$/;

// ---------- reading the report -------------------------------------------------------

function isNum(v) {
  return typeof v === 'number' && Number.isFinite(v);
}

function fail(code, detail) {
  return { error: { code, detail: detail || '' } };
}

// readReport checks a report variable against the contract. A JSON string is read as
// the object it holds, the way a variable may arrive.
export function readReport(value) {
  let v = value;
  if (typeof v === 'string') {
    try { v = JSON.parse(v); } catch { return fail('notReport'); }
  }
  if (v === null || v === undefined) return fail('missing');
  if (typeof v !== 'object' || Array.isArray(v)) return fail('notReport');
  if (v.schema !== REPORT_SCHEMA) return fail('schema', String(v.schema || ''));
  const ds = v.datasets;
  if (!ds || typeof ds !== 'object' || Array.isArray(ds)) return fail('noDatasets');
  if (Object.keys(ds).length > LIMITS.datasets) return fail('tooManyDatasets', String(LIMITS.datasets));
  return { report: v };
}

// readDataset checks one dataset: a value ({value, previous?, target?}) or a series
// table ({categories, series: [{name, values}]}), each with a unit.
export function readDataset(report, name) {
  const d = report.datasets[name];
  if (!d || typeof d !== 'object' || Array.isArray(d)) return fail('noDataset', name || '');
  const unit = typeof d.unit === 'string' && UNIT_RE.test(d.unit) ? d.unit : 'count';
  if ('value' in d) {
    if (!(d.value === null || isNum(d.value))) return fail('badValue', name);
    for (const k of [ 'previous', 'target' ]) {
      if (k in d && !(d[k] === null || isNum(d[k]))) return fail('badValue', name);
    }
    return { dataset: { kind: 'value', unit, value: d.value, previous: d.previous ?? null, target: d.target ?? null } };
  }
  const cats = d.categories;
  const series = d.series;
  if (!Array.isArray(cats) || !Array.isArray(series) || !series.length) return fail('badSeries', name);
  if (cats.length > LIMITS.categories) return fail('tooManyCategories', String(LIMITS.categories));
  if (series.length > LIMITS.series) return fail('tooManySeries', String(LIMITS.series));
  const out = [];
  for (const s of series) {
    if (!s || !Array.isArray(s.values) || s.values.length !== cats.length) return fail('badSeries', name);
    if (!s.values.every((x) => x === null || isNum(x))) return fail('badValue', name);
    out.push({ name: String(s.name ?? ''), values: s.values });
  }
  return { dataset: { kind: 'series', unit, categories: cats.map((c) => String(c ?? '')), series: out } };
}

// ---------- formatting -----------------------------------------------------------------

// NEUTRAL_MARKS are the marks a number is written with when the page names none: the
// narrow no-break space between thousands and a point before the decimals (ISO 31-0),
// the Console's own default (api/web/numfmt.js, ADR-0459). A page passes the reader's
// region as the form's `atlasNumberMarks`; the browser's locale is never asked, so a
// chart and the badge beside it write one number one way.
export const NEUTRAL_MARKS = { group: '\u202F', decimal: '.' };

// num writes a number with at most `digits` decimals in the given marks.
export function num(v, digits = 2, marks = NEUTRAL_MARKS) {
  const m = marks && typeof marks.group === 'string' && typeof marks.decimal === 'string' ? marks : NEUTRAL_MARKS;
  const fixed = Math.abs(v).toFixed(digits).replace(/\.?0+$/, '');
  const [ whole, frac ] = fixed.split('.');
  const grouped = whole.replace(/\B(?=(\d{3})+$)/g, m.group);
  return (v < 0 && Number(fixed) !== 0 ? '-' : '') + grouped + (frac ? m.decimal + frac : '');
}

// formatValue renders a value in its unit. Units are written as symbols, the same in
// either language, except days.
export function formatValue(v, unit, locale, marks) {
  if (!isNum(v)) return '–';
  if (unit === 'percent') return num(v, 1, marks) + ' %';
  if (unit === 'days') return num(v, 1, marks) + (locale === 'en' ? ' d' : ' T');
  if (unit === 'ms') {
    const a = Math.abs(v);
    if (a < 1000) return num(v, 0, marks) + ' ms';
    if (a < 60000) return num(v / 1000, 1, marks) + ' s';
    if (a < 3600000) return num(v / 60000, 1, marks) + ' min';
    return num(v / 3600000, 1, marks) + ' h';
  }
  if (unit && unit.startsWith('currency:')) return unit.slice(9) + '\u00A0' + num(v, 2, marks);
  return num(v, 2, marks);
}

// niceTicks answers about count round steps covering [lo, hi].
export function niceTicks(lo, hi, count = 4) {
  if (!(hi > lo)) { hi = lo + 1; }
  const raw = (hi - lo) / count;
  const mag = Math.pow(10, Math.floor(Math.log10(raw)));
  const step = [ 1, 2, 2.5, 5, 10 ].map((m) => m * mag).find((s) => s >= raw) || 10 * mag;
  const start = Math.floor(lo / step) * step;
  const end = Math.ceil(hi / step) * step;
  const ticks = [];
  for (let t = start; t <= end + step / 2; t += step) ticks.push(Math.round(t / step) * step);
  return { lo: start, hi: end, ticks };
}

// textWidth estimates a label's width in points: system sans at ~0.56 em per glyph.
export function textWidth(s, size) {
  return String(s).length * size * 0.56;
}

function clip(s, maxW, size) {
  const str = String(s);
  if (textWidth(str, size) <= maxW) return str;
  const n = Math.max(1, Math.floor(maxW / (size * 0.56)) - 1);
  return str.slice(0, n) + '…';
}

// ---------- layout -------------------------------------------------------------------

// pushTicks writes a value axis's labels under it; the first and the last are set
// flush with their tick, so neither runs past the chart's edge.
function pushTicks(prims, ticks, sx, y, fmt) {
  ticks.forEach((t, i) => {
    const anchor = i === 0 ? 'start' : i === ticks.length - 1 ? 'end' : 'middle';
    prims.push({ t: 'text', x: sx(t), y, s: fmt(t), size: SMALL, cls: 'muted', anchor });
  });
}

const FONT = 12;
const SMALL = 11;

// seriesClass is a series' colour role: one series is the accent; several take the
// categorical slots in their order, never cycled (a ninth is refused by the limits).
function seriesClass(i, n) {
  return n === 1 ? 'accent' : 's' + i;
}

function pick(dataset, names) {
  if (!Array.isArray(names) || !names.length) return dataset.series;
  const by = new Map(dataset.series.map((s) => [ s.name, s ]));
  const out = names.map((n) => by.get(n)).filter(Boolean);
  return out.length ? out : dataset.series;
}

function tableOf(dataset, series, unit, locale, marks) {
  if (dataset.kind === 'value') {
    const rows = [ [ 'value', formatValue(dataset.value, unit, locale, marks) ] ];
    if (isNum(dataset.previous)) rows.push([ 'previous', formatValue(dataset.previous, unit, locale, marks) ]);
    if (isNum(dataset.target)) rows.push([ 'target', formatValue(dataset.target, unit, locale, marks) ]);
    return { head: [ '', '' ], rows };
  }
  return {
    head: [ '', ...series.map((s) => s.name) ],
    rows: dataset.categories.map((c, i) => [ c, ...series.map((s) => formatValue(s.values[i], unit, locale, marks)) ])
  };
}

// layoutChart lays out one chart: the field's chartType over a checked dataset, in a
// box `width` points wide. It answers {width, height, prims, legend, table} or an error.
export function layoutChart(field, dataset, opts = {}) {
  const width = Math.max(240, Math.round(opts.width || 560));
  const locale = opts.locale || 'de';
  const marks = opts.marks || NEUTRAL_MARKS;
  const type = CHART_TYPES.includes(field.chartType) ? field.chartType : 'bar';
  const unit = typeof field.unit === 'string' && UNIT_RE.test(field.unit) ? field.unit : dataset.unit;
  const fmt = (v) => formatValue(v, unit, locale, marks);
  const series = dataset.kind === 'series' ? pick(dataset, field.series) : [];
  const legend = series.length > 1 ? series.map((s, i) => ({ name: s.name, cls: seriesClass(i, series.length) })) : [];
  const table = tableOf(dataset, series, unit, locale, marks);
  const base = { legend, table, type };

  if (type === 'kpi') return { ...base, ...kpi(dataset, series, width, fmt) };
  if (dataset.kind !== 'series' && type !== 'bullet') return fail('needsSeries', type);
  if (type === 'line') return { ...base, ...line(dataset, series, width, fmt, field) };
  const pct = (v) => formatValue(v, 'percent', locale, marks);
  if (type === 'stackedBar') return { ...base, ...stacked(dataset, series, width, fmt, pct, !!field.stack100, field.showValues) };
  if (type === 'bullet') return { ...base, ...bullet(dataset, series, width, fmt, field) };
  if (field.orientation === 'vertical') return { ...base, ...columns(dataset, series, width, fmt, field.showValues) };
  return { ...base, ...bars(dataset, series, width, fmt, field.showValues) };
}

// kpi: the figure, its change against the previous value, and a sparkline when a
// series carries the history.
function kpi(dataset, series, width, fmt) {
  let value = null;
  let previous = null;
  let spark = null;
  if (dataset.kind === 'value') {
    value = dataset.value;
    previous = dataset.previous;
  } else {
    const vals = series[0].values;
    const known = vals.map((v, i) => [ v, i ]).filter(([ v ]) => isNum(v));
    if (known.length) value = known[known.length - 1][0];
    if (known.length > 1) previous = known[known.length - 2][0];
    if (known.length > 1) spark = vals;
  }
  const prims = [ { t: 'text', x: 0, y: 38, s: fmt(value), size: 34, weight: 600, cls: 'ink', anchor: 'start' } ];
  if (isNum(value) && isNum(previous)) {
    const d = value - previous;
    const pct = previous !== 0 ? (d / Math.abs(previous)) * 100 : null;
    const sign = d > 0 ? '▲ +' : d < 0 ? '▼ −' : '■ ';
    const txt = sign + fmt(Math.abs(d)).replace(/^-/, '') + (pct !== null && d !== 0 ? ` (${Math.abs(pct).toFixed(1)} %)` : '');
    // Neutral ink: whether fewer is better (incidents) or worse (orders) the chart
    // cannot know, and the status colours are not a series' to borrow.
    prims.push({ t: 'text', x: 0, y: 62, s: txt, size: FONT, cls: 'muted', anchor: 'start' });
  }
  let height = 72;
  if (spark) {
    const w = Math.min(width, 220);
    const x0 = width - w;
    const known = spark.filter(isNum);
    const lo = Math.min(...known);
    const hi = Math.max(...known);
    const span = hi - lo || 1;
    const step = spark.length > 1 ? w / (spark.length - 1) : 0;
    const pts = [];
    spark.forEach((v, i) => { if (isNum(v)) pts.push([ x0 + i * step, 52 - ((v - lo) / span) * 40 ]); });
    prims.push({ t: 'poly', pts, cls: 'accent', w: 2 });
    height = 72;
  }
  return { width, height, prims };
}

// bars: horizontal bars, a band per category, one bar per series within it.
function bars(dataset, series, width, fmt, showValues) {
  const n = series.length;
  const all = series.flatMap((s) => s.values.filter(isNum));
  const lo = Math.min(0, ...all);
  const hi = Math.max(0, ...all);
  const scale = niceTicks(lo, hi, 4);
  const labelW = Math.min(width * 0.32, Math.max(...dataset.categories.map((c) => textWidth(c, FONT)), 24) + 14);
  const valueW = showValues ? Math.min(80, Math.max(...all.map((v) => textWidth(fmt(v), SMALL)), 0) + 6) : 0;
  const x0 = labelW;
  const plotW = Math.max(60, width - labelW - valueW - 4);
  const sx = (v) => x0 + ((v - scale.lo) / (scale.hi - scale.lo)) * plotW;
  const barH = Math.max(6, Math.min(18, 22 - n * 2));
  const bandH = n * barH + (n - 1) * 2 + 10;
  const top = 4;
  const prims = [];
  for (const t of scale.ticks) {
    prims.push({ t: 'line', x1: sx(t), y1: top, x2: sx(t), y2: top + dataset.categories.length * bandH, cls: t === 0 ? 'axis' : 'grid', w: 1 });
  }
  dataset.categories.forEach((c, i) => {
    const y = top + i * bandH + 5;
    prims.push({ t: 'text', x: labelW - 8, y: y + (n * barH + (n - 1) * 2) / 2 + 4, s: clip(c, labelW - 8, FONT), size: FONT, cls: 'ink', anchor: 'end' });
    series.forEach((s, j) => {
      const v = s.values[i];
      if (!isNum(v)) return;
      const a = sx(Math.min(0, v));
      const b = sx(Math.max(0, v));
      const by = y + j * (barH + 2);
      prims.push({ t: 'rect', x: a, y: by, w: Math.max(1, b - a), h: barH, r: Math.min(3, barH / 2), cls: seriesClass(j, n), tip: `${c} · ${s.name}: ${fmt(v)}` });
      if (showValues) prims.push({ t: 'text', x: b + 4, y: by + barH / 2 + 4, s: fmt(v), size: SMALL, cls: 'muted', anchor: 'start' });
    });
  });
  const axisY = top + dataset.categories.length * bandH + 14;
  pushTicks(prims, scale.ticks, sx, axisY, fmt);
  return { width, height: axisY + 6, prims };
}

// columns: vertical bars, for few categories with short labels.
function columns(dataset, series, width, fmt, showValues) {
  const n = series.length;
  const all = series.flatMap((s) => s.values.filter(isNum));
  const scale = niceTicks(Math.min(0, ...all), Math.max(0, ...all), 4);
  const axisW = Math.max(...scale.ticks.map((t) => textWidth(fmt(t), SMALL))) + 8;
  const plotH = 180;
  const top = showValues ? 16 : 6;
  const x0 = axisW;
  const plotW = width - axisW - 4;
  const bandW = plotW / dataset.categories.length;
  const colW = Math.max(4, Math.min(28, (bandW - 10 - (n - 1) * 2) / n));
  const sy = (v) => top + plotH - ((v - scale.lo) / (scale.hi - scale.lo)) * plotH;
  const prims = [];
  for (const t of scale.ticks) {
    prims.push({ t: 'line', x1: x0, y1: sy(t), x2: x0 + plotW, y2: sy(t), cls: t === 0 ? 'axis' : 'grid', w: 1 });
    prims.push({ t: 'text', x: x0 - 6, y: sy(t) + 4, s: fmt(t), size: SMALL, cls: 'muted', anchor: 'end' });
  }
  dataset.categories.forEach((c, i) => {
    const groupW = n * colW + (n - 1) * 2;
    const gx = x0 + i * bandW + (bandW - groupW) / 2;
    series.forEach((s, j) => {
      const v = s.values[i];
      if (!isNum(v)) return;
      const a = sy(Math.max(0, v));
      const b = sy(Math.min(0, v));
      const x = gx + j * (colW + 2);
      prims.push({ t: 'rect', x, y: a, w: colW, h: Math.max(1, b - a), r: Math.min(3, colW / 2), cls: seriesClass(j, n), tip: `${c} · ${s.name}: ${fmt(v)}` });
      if (showValues) prims.push({ t: 'text', x: x + colW / 2, y: a - 4, s: fmt(v), size: SMALL, cls: 'muted', anchor: 'middle' });
    });
    prims.push({ t: 'text', x: x0 + i * bandW + bandW / 2, y: top + plotH + 16, s: clip(c, bandW - 4, SMALL), size: SMALL, cls: 'ink', anchor: 'middle' });
  });
  return { width, height: top + plotH + 24, prims };
}

// stacked: horizontal bars of parts; with stack100 each category is its shares.
function stacked(dataset, series, width, fmt, pct, stack100, showValues) {
  const n = series.length;
  const totals = dataset.categories.map((_, i) => series.reduce((a, s) => a + (isNum(s.values[i]) && s.values[i] > 0 ? s.values[i] : 0), 0));
  const hi = stack100 ? 100 : Math.max(...totals, 0);
  const scale = stack100 ? { lo: 0, hi: 100, ticks: [ 0, 25, 50, 75, 100 ] } : niceTicks(0, hi, 4);
  const tickFmt = stack100 ? pct : fmt;
  const labelW = Math.min(width * 0.32, Math.max(...dataset.categories.map((c) => textWidth(c, FONT)), 24) + 14);
  const x0 = labelW;
  const plotW = Math.max(60, width - labelW - 8);
  const sx = (v) => x0 + ((v - scale.lo) / (scale.hi - scale.lo)) * plotW;
  const barH = 18;
  const bandH = 28;
  const top = 4;
  const prims = [];
  for (const t of scale.ticks) {
    prims.push({ t: 'line', x1: sx(t), y1: top, x2: sx(t), y2: top + dataset.categories.length * bandH, cls: t === 0 ? 'axis' : 'grid', w: 1 });
  }
  dataset.categories.forEach((c, i) => {
    const y = top + i * bandH + 5;
    prims.push({ t: 'text', x: labelW - 8, y: y + barH / 2 + 4, s: clip(c, labelW - 8, FONT), size: FONT, cls: 'ink', anchor: 'end' });
    let acc = 0;
    series.forEach((s, j) => {
      const raw = s.values[i];
      if (!isNum(raw) || raw <= 0) return;
      const v = stack100 ? (totals[i] ? (raw / totals[i]) * 100 : 0) : raw;
      const a = sx(acc);
      const b = sx(acc + v);
      acc += v;
      // A 2-point gap of surface between segments, so adjacent parts never merge.
      const w = Math.max(1, b - a - 2);
      const tip = `${c} · ${s.name}: ${fmt(raw)}${stack100 ? ` (${pct(v)})` : ''}`;
      prims.push({ t: 'rect', x: a, y, w, h: barH, r: 2, cls: seriesClass(j, n), tip });
      const shown = stack100 ? pct(v) : fmt(raw);
      if (showValues && w > textWidth(shown, SMALL) + 6) {
        prims.push({ t: 'text', x: a + w / 2, y: y + barH / 2 + 4, s: shown, size: SMALL, cls: 'onfill', anchor: 'middle' });
      }
    });
  });
  const axisY = top + dataset.categories.length * bandH + 14;
  pushTicks(prims, scale.ticks, sx, axisY, tickFmt);
  return { width, height: axisY + 6, prims };
}

// line: a value over the categories, one line per series; an area only for one.
function line(dataset, series, width, fmt) {
  const n = series.length;
  const all = series.flatMap((s) => s.values.filter(isNum));
  const scale = niceTicks(Math.min(...all, all.length ? all[0] : 0), Math.max(...all, all.length ? all[0] : 1), 4);
  const axisW = Math.max(...scale.ticks.map((t) => textWidth(fmt(t), SMALL))) + 8;
  // Room on the right for the series' names at their line ends, up to four of them.
  const endW = n > 1 && n <= 4 ? Math.min(110, Math.max(...series.map((s) => textWidth(s.name, SMALL))) + 8) : 8;
  const top = 8;
  const plotH = 170;
  const x0 = axisW;
  const plotW = Math.max(60, width - axisW - endW);
  const cats = dataset.categories;
  const step = cats.length > 1 ? plotW / (cats.length - 1) : 0;
  const sx = (i) => x0 + (cats.length > 1 ? i * step : plotW / 2);
  const sy = (v) => top + plotH - ((v - scale.lo) / (scale.hi - scale.lo)) * plotH;
  const prims = [];
  for (const t of scale.ticks) {
    prims.push({ t: 'line', x1: x0, y1: sy(t), x2: x0 + plotW, y2: sy(t), cls: 'grid', w: 1 });
    prims.push({ t: 'text', x: x0 - 6, y: sy(t) + 4, s: fmt(t), size: SMALL, cls: 'muted', anchor: 'end' });
  }
  prims.push({ t: 'line', x1: x0, y1: top + plotH, x2: x0 + plotW, y2: top + plotH, cls: 'axis', w: 1 });
  // Category labels thinned out so they never overlap.
  const maxLabel = Math.max(...cats.map((c) => textWidth(c, SMALL)), 1);
  const every = Math.max(1, Math.ceil((maxLabel + 8) / Math.max(step, 1)));
  cats.forEach((c, i) => {
    if (i % every === 0 || i === cats.length - 1) {
      prims.push({ t: 'text', x: sx(i), y: top + plotH + 16, s: c, size: SMALL, cls: 'muted', anchor: i === 0 ? 'start' : i === cats.length - 1 ? 'end' : 'middle' });
    }
  });
  series.forEach((s, j) => {
    const cls = seriesClass(j, n);
    // A gap in the values breaks the line rather than drawing across it.
    let run = [];
    const runs = [];
    s.values.forEach((v, i) => {
      if (isNum(v)) run.push([ sx(i), sy(v) ]);
      else if (run.length) { runs.push(run); run = []; }
    });
    if (run.length) runs.push(run);
    for (const pts of runs) {
      // An area only when the axis starts at zero: filled from 25 it would show the
      // values larger than they are.
      if (n === 1 && pts.length > 1 && scale.lo <= 0) {
        prims.push({ t: 'poly', pts: [ [ pts[0][0], sy(0) ], ...pts, [ pts[pts.length - 1][0], sy(0) ] ], cls: 'area', fill: true });
      }
      if (pts.length === 1) prims.push({ t: 'rect', x: pts[0][0] - 4, y: pts[0][1] - 4, w: 8, h: 8, r: 4, cls });
      else prims.push({ t: 'poly', pts, cls, w: 2 });
    }
    s.values.forEach((v, i) => {
      if (isNum(v)) prims.push({ t: 'hit', x: sx(i) - Math.max(step / 2, 6), y: top, w: Math.max(step, 12), h: plotH, tip: `${cats[i]} · ${s.name}: ${fmt(v)}` });
    });
    if (n > 1 && n <= 4) {
      const last = [ ...s.values ].reverse().findIndex(isNum);
      if (last >= 0) {
        const i = s.values.length - 1 - last;
        prims.push({ t: 'text', x: sx(i) + 6, y: sy(s.values[i]) + 4, s: clip(s.name, endW - 6, SMALL), size: SMALL, cls: 'ink', anchor: 'start' });
      }
    }
  });
  return { width, height: top + plotH + 24, prims };
}

// bullet: actual against a target, per category (first series) or for one value.
function bullet(dataset, series, width, fmt, field) {
  const rows = dataset.kind === 'value'
    ? [ { label: '', value: dataset.value, target: isNum(Number(field.target)) && field.target !== '' && field.target !== undefined ? Number(field.target) : dataset.target } ]
    : dataset.categories.map((c, i) => ({ label: c, value: series[0].values[i], target: isNum(Number(field.target)) && field.target !== '' && field.target !== undefined ? Number(field.target) : (series[1] ? series[1].values[i] : null) }));
  const all = rows.flatMap((r) => [ r.value, r.target ]).filter(isNum);
  const scale = niceTicks(Math.min(0, ...all), Math.max(0, ...all) * 1.1 || 1, 4);
  const labelW = rows.some((r) => r.label) ? Math.min(width * 0.32, Math.max(...rows.map((r) => textWidth(r.label, FONT))) + 14) : 0;
  const x0 = labelW;
  const plotW = Math.max(60, width - labelW - 8);
  const sx = (v) => x0 + ((v - scale.lo) / (scale.hi - scale.lo)) * plotW;
  const bandH = 30;
  const prims = [];
  rows.forEach((r, i) => {
    const y = 4 + i * bandH;
    if (r.label) prims.push({ t: 'text', x: labelW - 8, y: y + 15, s: clip(r.label, labelW - 8, FONT), size: FONT, cls: 'ink', anchor: 'end' });
    prims.push({ t: 'rect', x: x0, y: y + 3, w: plotW, h: 18, r: 2, cls: 'track' });
    if (isNum(r.value)) {
      prims.push({ t: 'rect', x: sx(Math.min(0, r.value)), y: y + 7, w: Math.max(1, sx(Math.max(0, r.value)) - sx(Math.min(0, r.value))), h: 10, r: 2, cls: 'accent', tip: `${r.label ? r.label + ': ' : ''}${fmt(r.value)}${isNum(r.target) ? ' / ' + fmt(r.target) : ''}` });
    }
    if (isNum(r.target)) prims.push({ t: 'line', x1: sx(r.target), y1: y, x2: sx(r.target), y2: y + 24, cls: 'target', w: 2 });
  });
  const axisY = 4 + rows.length * bandH + 10;
  pushTicks(prims, scale.ticks, sx, axisY, fmt);
  return { width, height: axisY + 6, prims };
}

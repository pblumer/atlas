// table.js — a shared, buildless enhancer that gives any already-rendered <table>
// click-to-sort column headers and an Excel-style per-column filter row beneath the
// header (ADR-0012: vanilla JS, no framework, no third-party table library). It
// replaces the one-off sort/filter code each view used to hand-roll, so every table
// in the UI behaves the same way.
//
// It is DOM-driven on purpose: a view keeps rendering its own rich row markup (links,
// badges, buttons) and simply calls enhanceTable(tableEl, opts) once after building
// the table. The helper reads each row's cells to sort and filter them. When a cell's
// visible text isn't what you want to sort or filter on — a formatted date, a number
// wrapped in a badge — put the raw value in a `data-sort` (and/or `data-filter`)
// attribute on that <td>, and the helper uses it instead of the text.
//
// opts:
//   key      — stable id; when set, the chosen sort column/direction persists in
//              localStorage so it survives a refresh or navigating away and back.
//   columns  — optional array aligned to the header cells. Each entry may set
//              { sort:false } to make a column unsortable, { filter:false } to drop
//              its filter input, or { type:"number"|"text" } to force the comparator.
//              A header cell with no text (an actions column) is skipped automatically.
//
// A view that renders a collapsible detail row beneath a data row — an expanded JSON
// value, a worker's log — marks it `data-dt-detail`. Such a row is not data: the helper
// never sorts it on its own, never opens it, and only hides it along with the row it
// belongs to when a filter removes that row. Whether it is open stays the view's to say.
export function enhanceTable(table, opts = {}) {
  if (!table || table.dataset.dtEnhanced === "1") return;
  const thead = table.tHead;
  const tbody = table.tBodies[0];
  if (!thead || !thead.rows.length || !tbody) return;
  const headRow = thead.rows[0];
  const ths = [...headRow.cells];
  const cols = ths.map((th, i) => {
    const c = (opts.columns && opts.columns[i]) || {};
    const empty = th.textContent.trim() === "" && c.sort === undefined && c.filter === undefined;
    return {
      sortable: c.sort !== false && !empty,
      filterable: c.filter !== false && !empty,
      type: c.type || null,
      label: th.textContent.trim(),
    };
  });
  table.dataset.dtEnhanced = "1";

  const KEY = opts.key ? "atlas.dt." + opts.key : null;
  let sort = { col: -1, dir: "asc" };
  if (KEY) {
    try { const s = JSON.parse(localStorage.getItem(KEY)); if (s && typeof s.col === "number") sort = s; } catch { /* ignore */ }
  }
  const saveSort = () => { if (KEY) { try { localStorage.setItem(KEY, JSON.stringify(sort)); } catch { /* ignore */ } } };

  // Clickable sort headers: a small indicator shows the active column/direction. Text
  // columns default to A→Z, numeric columns to high→low, and clicking again toggles.
  ths.forEach((th, i) => {
    if (!cols[i].sortable) return;
    th.classList.add("dt-sortable");
    if (!th.querySelector(".dt-ind")) {
      const ind = document.createElement("span");
      ind.className = "dt-ind";
      th.appendChild(ind);
    }
    th.addEventListener("click", () => {
      if (sort.col === i) sort.dir = sort.dir === "asc" ? "desc" : "asc";
      else sort = { col: i, dir: cols[i].type === "number" ? "desc" : "asc" };
      saveSort();
      apply();
    });
  });

  // A filter row under the header, one text input per filterable column. The inputs
  // live in the (view-owned-but-stable) header, so their values survive a tbody
  // refresh; the helper re-applies them whenever the rows are rebuilt.
  const frow = document.createElement("tr");
  frow.className = "dt-filter-row";
  ths.forEach((th, i) => {
    const cell = document.createElement("th");
    cell.className = "dt-filter-cell";
    if (cols[i].filterable) {
      const inp = document.createElement("input");
      inp.type = "text";
      inp.className = "dt-filter";
      inp.placeholder = "Filter";
      inp.setAttribute("aria-label", "Filter by " + (cols[i].label || ("column " + (i + 1))));
      inp.addEventListener("input", apply);
      cell.appendChild(inp);
    }
    frow.appendChild(cell);
  });
  thead.appendChild(frow);

  // The filter row is collapsed by default so it costs no vertical space until wanted.
  // A small funnel toggle in the header reveals it; the funnel stays highlighted while
  // any filter is set, so a collapsed-but-active filter is never a mystery. It sits in
  // the trailing actions column when there is one (right-aligned, out of the way), else
  // before the first header's label.
  const funnel = document.createElement("button");
  funnel.type = "button";
  funnel.className = "dt-filter-toggle";
  funnel.title = "Filter columns";
  funnel.setAttribute("aria-label", "Toggle column filters");
  funnel.setAttribute("aria-expanded", "false");
  funnel.innerHTML = '<svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M1.8 2.5h12.4L9.3 8.6v4.4l-2.6 1.3V8.6L1.8 2.5Z"/></svg>';
  funnel.addEventListener("click", (e) => {
    e.stopPropagation();
    const open = table.classList.toggle("dt-filters-open");
    funnel.setAttribute("aria-expanded", open ? "true" : "false");
    if (open) { const first = frow.querySelector(".dt-filter"); if (first) first.focus(); }
  });
  const lastI = ths.length - 1;
  if (ths[lastI] && ths[lastI].textContent.trim() === "" && !cols[lastI].sortable) {
    ths[lastI].classList.add("dt-toggle-cell");
    ths[lastI].appendChild(funnel);
  } else {
    ths[0].classList.add("dt-toggle-host");
    ths[0].insertBefore(funnel, ths[0].firstChild);
  }

  // The comparable/filterable value of a cell: a data-sort/data-filter override wins,
  // else the rendered text. forSort prefers data-sort; filtering prefers data-filter.
  const cellVal = (row, i, forSort) => {
    const cell = row.cells[i];
    if (!cell) return "";
    const raw = forSort
      ? (cell.dataset.sort != null ? cell.dataset.sort : (cell.dataset.filter != null ? cell.dataset.filter : cell.textContent))
      : (cell.dataset.filter != null ? cell.dataset.filter : cell.textContent);
    return (raw || "").trim();
  };
  const isNumeric = (s) => s !== "" && !isNaN(parseFloat(s)) && /^[-+]?[\d.,]+([eE][-+]?\d+)?$/.test(s.replace(/\s/g, ""));

  let obs;
  function apply() {
    if (obs) obs.disconnect(); // our own reordering must not re-trigger apply
    try {
      const filters = [...frow.cells].map((c) => {
        const inp = c.querySelector(".dt-filter");
        return inp ? inp.value.trim().toLowerCase() : "";
      });
      const active = filters.some(Boolean);
      funnel.classList.toggle("dt-has-filters", active);
      // A detail row (data-dt-detail) is not a row of the table's own data: it belongs to
      // the data row above it, and whether it is open is the *view's* business, not ours.
      // Owning its `hidden` would force every collapsible detail open — this ran on every
      // navigation and again on every tbody rewrite, so a view could not keep one closed.
      const detailsOf = new Map();
      let owner = null;
      for (const r of tbody.rows) {
        if (r.dataset.dtEmpty) continue;
        if (r.hasAttribute("data-dt-detail")) { if (owner) detailsOf.get(owner).push(r); }
        else { owner = r; detailsOf.set(r, []); }
      }
      const rows = [...detailsOf.keys()];
      let visible = 0;
      for (const r of rows) {
        let show = true;
        if (active) {
          for (let i = 0; i < filters.length; i++) {
            if (filters[i] && !cellVal(r, i, false).toLowerCase().includes(filters[i])) { show = false; break; }
          }
        }
        r.hidden = !show;
        // A detail of a row the filter removed goes with it; one whose row stays is left
        // exactly as the view has it, open or closed.
        if (!show) for (const d of detailsOf.get(r)) d.hidden = true;
        if (show) visible++;
      }

      if (sort.col >= 0 && cols[sort.col] && cols[sort.col].sortable) {
        const dir = sort.dir === "asc" ? 1 : -1;
        const forceNum = cols[sort.col].type === "number";
        const sorted = rows.slice().sort((a, b) => {
          const va = cellVal(a, sort.col, true), vb = cellVal(b, sort.col, true);
          let c;
          if (forceNum || (isNumeric(va) && isNumeric(vb))) c = parseFloat(va) - parseFloat(vb);
          else c = va.localeCompare(vb, undefined, { numeric: true, sensitivity: "base" });
          return c * dir;
        });
        // A detail row travels with the row it details, or sorting scatters it under a
        // stranger.
        for (const r of sorted) {
          tbody.appendChild(r);
          for (const d of detailsOf.get(r)) tbody.appendChild(d);
        }
      }

      ths.forEach((th, i) => {
        const ind = th.querySelector(".dt-ind");
        if (!ind) return;
        if (i === sort.col) {
          ind.textContent = sort.dir === "asc" ? "▲" : "▼";
          th.setAttribute("aria-sort", sort.dir === "asc" ? "ascending" : "descending");
        } else {
          ind.textContent = "";
          th.removeAttribute("aria-sort");
        }
      });

      // A "no matches" row when active filters hide everything (but there were rows).
      let empty = tbody.querySelector("tr[data-dt-empty]");
      if (active && visible === 0 && rows.length > 0) {
        if (!empty) {
          empty = document.createElement("tr");
          empty.dataset.dtEmpty = "1";
          const td = document.createElement("td");
          td.colSpan = ths.length;
          td.className = "empty";
          td.textContent = "No rows match the filters.";
          empty.appendChild(td);
        }
        empty.hidden = false;
        tbody.appendChild(empty);
      } else if (empty) {
        empty.hidden = true;
      }
    } finally {
      if (obs) obs.observe(tbody, { childList: true });
    }
  }

  // Re-apply the current sort and filters whenever the view rebuilds its rows (a
  // refresh or a live poll replaces the tbody contents).
  obs = new MutationObserver(() => apply());
  apply();
}

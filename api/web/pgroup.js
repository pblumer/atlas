// The collapsible property groups both diagram panels are made of.
//
// The BPMN Modeler's properties panel grew this shape first: a <h3> per section, a
// header that toggles it, a chevron, and a filled dot when the section carries
// content. The class canvas's panel needs the same thing, and the same thing is worth
// having exactly once — a lookalike drifts from its model the first time either side
// is touched, and then two panels a person uses in one session disagree about what a
// group looks like.
//
// It works on already-rendered markup, which is what makes it shareable: a panel emits
// plain sections and this turns them into groups, so no renderer has to know about
// grouping at all.

// groupifyPanel turns each <h3> section of the properties panel into a collapsible
// group (Camunda-style): the heading becomes a toggle with a chevron and a filled
// dot when the group has content, and everything up to the next <h3> becomes its
// collapsible body. It works on the already-rendered panel, so every element type's
// markup is grouped by one function instead of each branch knowing about grouping.
// Nodes are moved as whole subtrees, so field listeners and rich editors survive.
export function groupifyPanel(body, ctl) {
  const heads = [...body.children].filter((n) => n.tagName === "H3");
  if (!heads.length) return;
  // A section absorbs everything up to the next <h3>, but a standalone group (e.g.
  // the I/O mapping list groups, which render their own header) must stay a
  // top-level sibling rather than being folded into the preceding section's body.
  const isStop = (n) => n.nodeType === 1 && (n.tagName === "H3" || n.dataset.standaloneGroup === "1");
  for (const h3 of heads) {
    const title = h3.textContent.trim();
    const group = document.createElement("div");
    group.className = "pgroup" + (ctl.isCollapsed(title) ? " collapsed" : "");
    group.dataset.group = title;
    const bodyWrap = document.createElement("div");
    bodyWrap.className = "pgroup-body";
    let n = h3.nextSibling;
    while (n && !isStop(n)) {
      const next = n.nextSibling;
      bodyWrap.appendChild(n);
      n = next;
    }
    const fields = [...bodyWrap.querySelectorAll("input, textarea, select")];
    const hasVal = fields.some((el) =>
      el.tagName === "SELECT" ? el.selectedIndex > 0
        : (el.type !== "button" && el.type !== "submit" && (el.value || "").trim() !== ""));
    const hasRows = !!bodyWrap.querySelector(".dmn-input-row, .sv-row, .msg-row, tr, li");
    const head = document.createElement("button");
    head.type = "button";
    head.className = "pgroup-head";
    head.innerHTML = `<span class="pgroup-chevron">▸</span><span class="pgroup-title"></span>`;
    head.querySelector(".pgroup-title").textContent = title;
    if (hasVal || hasRows) {
      const dot = document.createElement("span");
      dot.className = "pgroup-dot";
      dot.title = "has content";
      head.appendChild(dot);
    }
    head.addEventListener("click", () => ctl.onToggle(title, group.classList.toggle("collapsed")));
    body.insertBefore(group, h3);
    group.appendChild(head);
    group.appendChild(bodyWrap);
    body.removeChild(h3);
  }
  // A subtle expand-all / collapse-all control, added once there is more than one
  // collapsible group (the <h3> sections plus any standalone I/O-mapping groups), so
  // the author can open or clear the whole panel in one click.
  const total = body.querySelectorAll(".pgroup, .io-group").length;
  if (total >= 2 && !body.querySelector(".pgroup-tools")) {
    const tools = document.createElement("div");
    tools.className = "pgroup-tools";
    tools.innerHTML = `<button type="button" class="pgroup-all" data-all="expand" title="Expand all groups">Expand all</button>`
      + `<span class="pgroup-all-sep" aria-hidden="true">·</span>`
      + `<button type="button" class="pgroup-all" data-all="collapse" title="Collapse all groups">Collapse all</button>`;
    tools.querySelector('[data-all="expand"]').addEventListener("click", () => ctl.setAll(false));
    tools.querySelector('[data-all="collapse"]').addEventListener("click", () => ctl.setAll(true));
    body.insertBefore(tools, body.firstChild);
  }
}

// groupController holds which groups are open. It is the half of the behaviour that is
// not markup: which sections start open, which the author has since toggled, and what
// "expand all" does to both.
//
// A group is addressed by its title rather than by an index, so it keeps its state
// across a re-render that emits a different set of sections — selecting another
// element, or a kind that has literals where the last one had attributes. Explicit
// toggles win over the default and are remembered for the editing session; they reset
// when the editor remounts, so reopening a file starts from the default again.
//
// `defaultOpen` is the list of titles that start open, or "all". Which of the two a
// panel wants follows from how many groups it has: a BPMN element has a dozen sections
// and opening one of them is the point, while a class has three and its attributes
// *are* the class — hiding them behind a click on every selection would make the panel
// worse than one with no groups at all.
export function groupController(body, defaultOpen = ["General"]) {
  const everything = defaultOpen === "all";
  const open = new Set(everything ? [] : defaultOpen);
  const choice = new Map(); // title -> true (collapsed) / false (open), only once toggled
  return {
    isCollapsed: (title) => choice.has(title) ? choice.get(title) : !(everything || open.has(title)),
    onToggle: (title, col) => choice.set(title, col),
    // Expand or collapse every group now on screen — the <h3> sections and any
    // standalone group that renders its own header — and record each, so a re-render
    // keeps what was chosen.
    setAll: (col) => {
      for (const g of body.querySelectorAll(".pgroup, .io-group")) {
        g.classList.toggle("collapsed", col);
        const t = (g.dataset.group || "").trim();
        if (t) choice.set(t, col);
      }
    },
  };
}

// Language modules for the polyglot script fields (ADR-0047), plugged into the
// buildless code editor (code-editor.js). Each is a bag of pure functions —
// tokenize / completions / prefix / tooltip / replaceStart — carrying no DOM state
// so it can be reasoned about (and, should a JS test runner land, tested) in
// isolation.
//
// PowerShell gets a first-class module (its own tokeniser, a curated cmdlet
// catalogue, and $variable / $env: awareness). Python and JavaScript share a
// smaller "clike" module — enough colour and keyword/variable completion to feel
// like code, without pretending to be a full parser.

// ---------- shared helpers ----------

// rankByPrefix scores items the way the FEEL editor does: exact match beats a
// prefix match beats a word-start beats a substring; an empty prefix keeps all.
// `text(it)` yields the string to match against.
function rankByPrefix(items, prefix, text) {
  const p = (prefix || "").toLowerCase();
  const scored = [];
  for (const it of items) {
    const label = text(it).toLowerCase();
    let score;
    if (p === "") score = 0;
    else if (label === p) score = 4;
    else if (label.startsWith(p)) score = 3;
    else if (label.split(/[\s-]+/).some((w) => w.startsWith(p))) score = 2;
    else if (label.includes(p)) score = 1;
    else continue;
    scored.push({ it, score });
  }
  scored.sort((a, b) => b.score - a.score || text(a.it).localeCompare(text(b.it)));
  return scored.map((s) => s.it);
}

const isWord = (ch) => ch !== undefined && /[A-Za-z0-9_]/.test(ch);

// ---------- PowerShell ----------

// PS_KEYWORDS are statement/flow keywords (not operators — PowerShell's logical
// and comparison operators are dashed words handled as operators below).
export const PS_KEYWORDS = [
  "if", "elseif", "else", "switch", "foreach", "for", "while", "do", "until",
  "break", "continue", "return", "function", "filter", "param", "begin", "process",
  "end", "try", "catch", "finally", "throw", "trap", "class", "enum", "using",
  "data", "dynamicparam", "exit",
];

// PS_OPERATORS are the dashed operators, each with a one-line doc for the tooltip
// and the completion detail. These are what a `-` prefix completes to.
export const PS_OPERATORS = [
  { name: "-eq", doc: "Equal to." },
  { name: "-ne", doc: "Not equal to." },
  { name: "-gt", doc: "Greater than." },
  { name: "-ge", doc: "Greater than or equal." },
  { name: "-lt", doc: "Less than." },
  { name: "-le", doc: "Less than or equal." },
  { name: "-like", doc: "Wildcard match." },
  { name: "-notlike", doc: "Wildcard non-match." },
  { name: "-match", doc: "Regex match (sets $Matches)." },
  { name: "-notmatch", doc: "Regex non-match." },
  { name: "-replace", doc: "Regex replace." },
  { name: "-split", doc: "Split a string into an array." },
  { name: "-join", doc: "Join an array into a string." },
  { name: "-contains", doc: "Collection contains a value." },
  { name: "-notcontains", doc: "Collection lacks a value." },
  { name: "-in", doc: "Value is in a collection." },
  { name: "-notin", doc: "Value is not in a collection." },
  { name: "-is", doc: "Is of a type." },
  { name: "-isnot", doc: "Is not of a type." },
  { name: "-as", doc: "Convert to a type (null on failure)." },
  { name: "-and", doc: "Logical and." },
  { name: "-or", doc: "Logical or." },
  { name: "-not", doc: "Logical not." },
  { name: "-xor", doc: "Logical exclusive or." },
  { name: "-band", doc: "Bitwise and." },
  { name: "-bor", doc: "Bitwise or." },
  { name: "-f", doc: "Format operator (string format)." },
];

// PS_CMDLETS is a curated slice of common cmdlets, each with a call signature and
// one-line doc shown in completion and on hover. It is intentionally a starter set
// (the catalogue is huge); it covers the verbs a script author reaches for first,
// plus the JSON/REST and Microsoft.Graph cmdlets an Atlas job script typically uses.
export const PS_CMDLETS = [
  // Output / streams
  { name: "Write-Output", sig: "Write-Output <obj>", doc: "Emit to the output stream — this becomes the task result." },
  { name: "Write-Host", sig: "Write-Host <msg>", doc: "Write to the host (not the output/result stream)." },
  { name: "Write-Error", sig: "Write-Error <msg>", doc: "Write to the error stream (stops when ErrorAction=Stop)." },
  { name: "Write-Warning", sig: "Write-Warning <msg>", doc: "Write to the warning stream." },
  { name: "Write-Verbose", sig: "Write-Verbose <msg>", doc: "Write to the verbose stream." },
  { name: "Out-Null", sig: "... | Out-Null", doc: "Discard pipeline output." },
  // JSON / REST
  { name: "ConvertTo-Json", sig: "ConvertTo-Json <obj> [-Depth n] [-Compress]", doc: "Serialize an object to JSON." },
  { name: "ConvertFrom-Json", sig: "ConvertFrom-Json <string> [-AsHashtable]", doc: "Parse JSON into objects." },
  { name: "Invoke-RestMethod", sig: "Invoke-RestMethod -Uri <url> [-Method] [-Body]", doc: "Call a REST endpoint, parsed result." },
  { name: "Invoke-WebRequest", sig: "Invoke-WebRequest -Uri <url>", doc: "HTTP request, full response object." },
  { name: "ConvertTo-SecureString", sig: "ConvertTo-SecureString <s> -AsPlainText -Force", doc: "Wrap a string as a SecureString." },
  // Pipeline shaping
  { name: "Where-Object", sig: "Where-Object { $_ ... }", doc: "Filter pipeline objects by a predicate." },
  { name: "ForEach-Object", sig: "ForEach-Object { $_ ... }", doc: "Run a block for each pipeline object." },
  { name: "Select-Object", sig: "Select-Object <props> [-First n]", doc: "Pick properties / first N objects." },
  { name: "Sort-Object", sig: "Sort-Object <prop> [-Descending]", doc: "Sort pipeline objects." },
  { name: "Group-Object", sig: "Group-Object <prop>", doc: "Group objects by a property." },
  { name: "Measure-Object", sig: "Measure-Object <prop> -Sum -Average", doc: "Aggregate numbers/strings." },
  { name: "Select-String", sig: "Select-String -Pattern <re>", doc: "Grep-style regex search." },
  { name: "Compare-Object", sig: "Compare-Object <ref> <diff>", doc: "Diff two collections." },
  // Objects / members
  { name: "New-Object", sig: "New-Object <type> <args>", doc: "Construct a .NET object." },
  { name: "Get-Member", sig: "<obj> | Get-Member", doc: "Inspect an object's members." },
  { name: "Add-Member", sig: "Add-Member -NotePropertyName <n> -NotePropertyValue <v>", doc: "Attach a property to an object." },
  { name: "Set-Variable", sig: "Set-Variable -Name <n> -Value <v>", doc: "Set a variable by name." },
  { name: "Get-Variable", sig: "Get-Variable <n>", doc: "Read a variable by name." },
  // Files / paths
  { name: "Get-ChildItem", sig: "Get-ChildItem <path>", doc: "List items in a location (alias: ls, dir)." },
  { name: "Get-Content", sig: "Get-Content <path>", doc: "Read a file's contents." },
  { name: "Set-Content", sig: "Set-Content <path> <value>", doc: "Write a file's contents." },
  { name: "Test-Path", sig: "Test-Path <path>", doc: "True if a path exists." },
  { name: "Join-Path", sig: "Join-Path <a> <b>", doc: "Combine path segments." },
  { name: "Split-Path", sig: "Split-Path <path> [-Leaf|-Parent]", doc: "Split a path." },
  // Misc
  { name: "Get-Date", sig: "Get-Date [-Format <f>]", doc: "The current date/time." },
  { name: "Start-Sleep", sig: "Start-Sleep -Seconds <n>", doc: "Pause execution." },
  { name: "Get-Random", sig: "Get-Random [-Minimum m -Maximum n]", doc: "A random number/element." },
  { name: "Import-Module", sig: "Import-Module <name>", doc: "Load a module." },
  { name: "Get-Command", sig: "Get-Command <name>", doc: "Discover commands." },
  { name: "Get-Help", sig: "Get-Help <name>", doc: "Show help for a command." },
  { name: "Read-Host", sig: "Read-Host <prompt>", doc: "Read a line of input." },
  // Microsoft.Graph (common in Atlas identity jobs)
  { name: "Connect-MgGraph", sig: "Connect-MgGraph -TenantId <id> -ClientSecretCredential <cred>", doc: "Authenticate to Microsoft Graph." },
  { name: "Get-MgUser", sig: "Get-MgUser -Filter <feel> -ConsistencyLevel eventual", doc: "Query users in Microsoft Graph." },
  { name: "New-MgUser", sig: "New-MgUser -BodyParameter <hash>", doc: "Create a user in Microsoft Graph." },
];

const PS_KEYWORD_SET = new Set(PS_KEYWORDS);
const PS_OP_BY_NAME = new Map(PS_OPERATORS.map((o) => [o.name, o]));
const PS_CMDLET_BY_NAME = new Map(PS_CMDLETS.map((c) => [c.name.toLowerCase(), c]));

// tokenizePowerShell splits PowerShell source into { type, value } spans covering
// the whole input (whitespace included). Types: comment, string, variable, number,
// type, cmdlet, keyword, operator, parameter, name, punct, ws. Cmdlet/operator
// spans carry tip:true + a canonical `name` so the editor can show a tooltip.
export function tokenizePowerShell(src) {
  const out = [];
  const n = src.length;
  let i = 0;
  const push = (type, value, extra) => out.push(Object.assign({ type, value }, extra));

  while (i < n) {
    const ch = src[i];

    if (/\s/.test(ch)) {
      let j = i + 1;
      while (j < n && /\s/.test(src[j])) j++;
      push("ws", src.slice(i, j)); i = j; continue;
    }

    // Block comment <# ... #>
    if (ch === "<" && src[i + 1] === "#") {
      const end = src.indexOf("#>", i + 2);
      const j = end === -1 ? n : end + 2;
      push("comment", src.slice(i, j)); i = j; continue;
    }
    // Line comment #... (a bare #, not the start of <# handled above).
    if (ch === "#") {
      let j = i + 1;
      while (j < n && src[j] !== "\n") j++;
      push("comment", src.slice(i, j)); i = j; continue;
    }
    // Here-strings @"  ...  "@  and  @' ... '@ (terminator at line start).
    if (ch === "@" && (src[i + 1] === '"' || src[i + 1] === "'")) {
      const q = src[i + 1];
      const term = "\n" + q + "@";
      const end = src.indexOf(term, i + 2);
      const j = end === -1 ? n : end + term.length;
      push("string", src.slice(i, j)); i = j; continue;
    }
    // Single- and double-quoted strings. Double-quoted honours the backtick
    // escape; single-quoted only doubles the quote to escape it.
    if (ch === '"') {
      let j = i + 1;
      while (j < n) {
        if (src[j] === "`") { j += 2; continue; }
        if (src[j] === '"') { j++; break; }
        j++;
      }
      push("string", src.slice(i, j)); i = j; continue;
    }
    if (ch === "'") {
      let j = i + 1;
      while (j < n) {
        if (src[j] === "'" && src[j + 1] === "'") { j += 2; continue; }
        if (src[j] === "'") { j++; break; }
        j++;
      }
      push("string", src.slice(i, j)); i = j; continue;
    }
    // Variables: $name, ${any}, $env:NAME, $script:x, and $_ / $? / $$ etc.
    if (ch === "$") {
      let j = i + 1;
      if (src[j] === "{") { const e = src.indexOf("}", j); j = e === -1 ? n : e + 1; }
      else if ("_?^$".includes(src[j] || "")) j += 1;
      else while (j < n && /[A-Za-z0-9_:]/.test(src[j])) j++;
      push("variable", src.slice(i, j)); i = j; continue;
    }
    // Numbers (incl. hex).
    if (/[0-9]/.test(ch) || (ch === "." && /[0-9]/.test(src[i + 1] || ""))) {
      let j = i;
      if (src[i] === "0" && (src[i + 1] === "x" || src[i + 1] === "X")) {
        j = i + 2; while (j < n && /[0-9a-fA-F]/.test(src[j])) j++;
      } else {
        while (j < n && /[0-9]/.test(src[j])) j++;
        if (src[j] === ".") { j++; while (j < n && /[0-9]/.test(src[j])) j++; }
      }
      push("number", src.slice(i, j)); i = j; continue;
    }
    // Type literal [Namespace.Type] or [Type[]] / [List[string]] — must start with a
    // letter (so an index like $a[0] stays punctuation) and hold only type-name
    // chars, tracking bracket depth so a nested [] closes correctly.
    if (ch === "[" && /[A-Za-z_]/.test(src[i + 1] || "")) {
      let j = i + 1, depth = 1, ok = true;
      while (j < n && depth > 0) {
        const c = src[j];
        if (c === "[") depth++;
        else if (c === "]") depth--;
        else if (!/[A-Za-z0-9_.,\s]/.test(c)) { ok = false; break; }
        j++;
      }
      if (ok && depth === 0) { push("type", src.slice(i, j)); i = j; continue; }
    }
    // Dashed operators / parameters: -eq, -Path. A known operator colours as one
    // (with a tooltip); any other -word is a parameter.
    if (ch === "-" && /[A-Za-z]/.test(src[i + 1] || "")) {
      let j = i + 1;
      while (j < n && /[A-Za-z0-9]/.test(src[j])) j++;
      const word = src.slice(i, j);
      if (PS_OP_BY_NAME.has(word.toLowerCase())) push("operator", word, { tip: true, name: word.toLowerCase() });
      else push("parameter", word);
      i = j; continue;
    }
    // Identifiers: a Verb-Noun cmdlet, a keyword, or a plain name/command.
    if (/[A-Za-z_]/.test(ch)) {
      let j = i + 1;
      while (j < n && (isWord(src[j]) || (src[j] === "-" && /[A-Za-z]/.test(src[j + 1] || "")))) j++;
      const word = src.slice(i, j);
      const lower = word.toLowerCase();
      if (PS_KEYWORD_SET.has(lower)) push("keyword", word);
      else if (/^[A-Za-z][A-Za-z0-9]*-[A-Za-z][A-Za-z0-9]*$/.test(word)) {
        push("cmdlet", word, PS_CMDLET_BY_NAME.has(lower) ? { tip: true, name: lower } : undefined);
      } else push("name", word);
      i = j; continue;
    }
    push("punct", ch); i++;
  }
  return out;
}

// psPrefix decides what the caret is completing: an $env: reference, a $variable,
// a -operator/parameter, or a bare command word. Returns { text, start, mode }.
function psPrefix(before) {
  let m = /\$env:([A-Za-z0-9_]*)$/i.exec(before);
  if (m) return { text: m[0], start: before.length - m[0].length, mode: "env" };
  m = /\$[A-Za-z_][A-Za-z0-9_]*$/.exec(before);
  if (m) return { text: m[0], start: before.length - m[0].length, mode: "var" };
  m = /-[A-Za-z]*$/.exec(before);
  if (m) return { text: m[0], start: before.length - m[0].length, mode: "op" };
  m = /[A-Za-z][A-Za-z0-9]*(-[A-Za-z0-9]*)?$/.exec(before);
  if (m) return { text: m[0], start: before.length - m[0].length, mode: "cmd" };
  return { text: "", start: before.length, mode: "cmd" };
}

// envNamesIn harvests the $env:NAME keys the script already references, so the
// editor can complete environment variables the author has used without the server
// having to enumerate the worker's secrets (ADR-0041).
function envNamesIn(source) {
  const set = new Set();
  const re = /\$env:([A-Za-z_][A-Za-z0-9_]*)/gi;
  let m;
  while ((m = re.exec(source || ""))) set.add(m[1]);
  return [...set];
}

export const powershell = {
  tokenize: tokenizePowerShell,
  prefix: psPrefix,
  replaceStart(before) { return psPrefix(before).start; },
  tooltip(name) {
    const c = PS_CMDLET_BY_NAME.get(name);
    if (c) return { sig: c.sig, doc: c.doc };
    const o = PS_OP_BY_NAME.get(name);
    if (o) return { sig: o.name, doc: o.doc };
    return null;
  },
  completions(prefix, ctx) {
    const p = psPrefix(ctx.before || "");
    if (p.mode === "env") {
      const rest = p.text.slice(5); // after "$env:"
      const items = envNamesIn(ctx.source).map((name) => ({
        label: "$env:" + name, kind: "env", insert: "$env:" + name, detail: "environment",
      }));
      return rankByPrefix(items, rest, (it) => it.label.slice(5));
    }
    if (p.mode === "var") {
      const rest = p.text.slice(1);
      const items = (ctx.variables || []).map((name) => ({
        label: "$" + name, kind: "variable", insert: "$" + name, detail: "instance variable",
      }));
      return rankByPrefix(items, rest, (it) => it.label.slice(1));
    }
    if (p.mode === "op") {
      const rest = p.text.slice(1);
      const items = PS_OPERATORS.map((o) => ({
        label: o.name, kind: "operator", insert: o.name, detail: o.doc,
      }));
      return rankByPrefix(items, rest, (it) => it.label.slice(1));
    }
    const items = [
      ...PS_CMDLETS.map((c) => ({ label: c.name, kind: "cmdlet", insert: c.name, detail: c.sig, doc: c.doc })),
      ...PS_KEYWORDS.map((k) => ({ label: k, kind: "keyword", insert: k, detail: "keyword" })),
    ];
    return rankByPrefix(items, p.text, (it) => it.label);
  },
};

// ---------- clike (Python / JavaScript) ----------

const CLIKE_KEYWORDS = {
  python: [
    "def", "return", "if", "elif", "else", "for", "while", "in", "not", "and", "or",
    "is", "import", "from", "as", "class", "try", "except", "finally", "raise", "with",
    "lambda", "yield", "pass", "break", "continue", "global", "nonlocal", "del", "assert",
    "True", "False", "None",
  ],
  javascript: [
    "const", "let", "var", "function", "return", "if", "else", "for", "while", "do",
    "switch", "case", "default", "break", "continue", "new", "typeof", "instanceof",
    "class", "extends", "try", "catch", "finally", "throw", "await", "async", "yield",
    "of", "in", "delete", "void", "this", "true", "false", "null", "undefined",
  ],
};

// tokenizeClike is a compact tokeniser shared by Python and JavaScript: `#` (Python)
// or `//` and `/* */` (JS) comments, single/double-quoted (and JS template) strings,
// numbers, keywords, and identifiers.
export function tokenizeClike(src, kind) {
  const out = [];
  const n = src.length;
  let i = 0;
  const kw = new Set(CLIKE_KEYWORDS[kind] || []);
  const push = (type, value) => out.push({ type, value });
  const strQuotes = kind === "javascript" ? "\"'`" : "\"'";

  while (i < n) {
    const ch = src[i];
    if (/\s/.test(ch)) {
      let j = i + 1; while (j < n && /\s/.test(src[j])) j++;
      push("ws", src.slice(i, j)); i = j; continue;
    }
    if (kind === "python" && ch === "#") {
      let j = i + 1; while (j < n && src[j] !== "\n") j++;
      push("comment", src.slice(i, j)); i = j; continue;
    }
    if (kind === "javascript" && ch === "/" && src[i + 1] === "/") {
      let j = i + 2; while (j < n && src[j] !== "\n") j++;
      push("comment", src.slice(i, j)); i = j; continue;
    }
    if (kind === "javascript" && ch === "/" && src[i + 1] === "*") {
      const e = src.indexOf("*/", i + 2); const j = e === -1 ? n : e + 2;
      push("comment", src.slice(i, j)); i = j; continue;
    }
    if (strQuotes.includes(ch)) {
      let j = i + 1;
      while (j < n) {
        if (src[j] === "\\") { j += 2; continue; }
        if (src[j] === ch) { j++; break; }
        j++;
      }
      push("string", src.slice(i, j)); i = j; continue;
    }
    if (/[0-9]/.test(ch) || (ch === "." && /[0-9]/.test(src[i + 1] || ""))) {
      let j = i; while (j < n && /[0-9]/.test(src[j])) j++;
      if (src[j] === ".") { j++; while (j < n && /[0-9]/.test(src[j])) j++; }
      push("number", src.slice(i, j)); i = j; continue;
    }
    if (/[A-Za-z_$]/.test(ch)) {
      let j = i + 1; while (j < n && /[A-Za-z0-9_$]/.test(src[j])) j++;
      const word = src.slice(i, j);
      push(kw.has(word) ? "keyword" : "name", word);
      i = j; continue;
    }
    push("punct", ch); i++;
  }
  return out;
}

function clikeModule(kind) {
  const keywords = CLIKE_KEYWORDS[kind] || [];
  return {
    tokenize: (src) => tokenizeClike(src, kind),
    completions(prefix, ctx) {
      const items = [
        ...(ctx.variables || []).map((name) => ({ label: name, kind: "variable", insert: name, detail: "instance variable" })),
        ...keywords.map((k) => ({ label: k, kind: "keyword", insert: k, detail: "keyword" })),
      ];
      return rankByPrefix(items, prefix, (it) => it.label);
    },
  };
}

export const python = clikeModule("python");
export const javascript = clikeModule("javascript");

// moduleFor returns the language module for a script language name, defaulting to
// PowerShell (the modeler's default script language).
export function moduleFor(language) {
  if (language === "python") return python;
  if (language === "javascript") return javascript;
  return powershell;
}

// The console's message catalogue (ADR-0267).
//
// Atlas's console was written in English, hard-coded at every call site. The
// Tasks app is where that stopped being tenable: its folders are built and read
// by the people doing the work, not by the people who deploy the models, and in
// this installation those people work in German.
//
// So the answer is not a switch that flips the whole console — that is a
// translation project, and doing it badly is worse than not doing it. It is a
// boundary: text that a person reads lives in a catalogue keyed by id, German is
// the language it is written in first, and a second locale is a second object in
// the same file. Nothing else in the console has to change until it is translated,
// and a screen that has been translated never has to be untangled again.
//
// Three rules keep it honest:
//
//   - The server sends ids and model data, never interface text. A process is
//     named by its model; "Prozess" is a word this file owns.
//   - A missing key renders as the key, visibly. A catalogue with a hole in it
//     should look broken in review, not fall back to a language nobody chose.
//   - The default is German, not the browser's language. The rest of the console
//     is still English, and guessing would give somebody half a translated screen
//     because of a setting they never made.

// The default. It is the language the strings below were written in, and the one
// a viewer gets unless they have deliberately chosen otherwise.
const DEFAULT_LOCALE = "de";

// CATALOGS holds one object per locale, keyed by message id. Ids are dotted paths
// naming the screen and then the thing — `tasks.folders.save` — so a screen's
// strings sit together and an unused one is findable.
const CATALOGS = {
  de: {
    "common.cancel": "Abbrechen",
    "common.save": "Speichern",
    "common.delete": "Löschen",
    "common.edit": "Bearbeiten",
    "common.close": "Schließen",

    "tasks.folders.mine": "Meine Ordner",
    "tasks.folders.shared": "Geteilt",
    "tasks.folders.new": "Neuer Ordner",
    "tasks.opened": "Eröffnet",
    "tasks.filter": "Tasks filtern (Name, Person, Inhalt)…",
    "tasks.folders.newTitle": "Neuer Ordner",
    "tasks.folders.editTitle": "Ordner bearbeiten",
    "tasks.folders.rule": "Regel",
    "tasks.folders.nameLabel": "Name des Ordners",
    "tasks.folders.namePlaceholder": "z. B. Kunden Anfragen",
    "tasks.folders.visibility": "Sichtbar für",
    "tasks.folders.visibility.private": "Nur ich",
    "tasks.folders.visibility.org": "Alle in der Organisation",
    "tasks.folders.visibility.group": "Gruppe: {name}",
    "tasks.folders.conditions": "Bedingungen",
    "tasks.folders.matchPrefix": "Aufgaben anzeigen, die",
    "tasks.folders.match.all": "alle",
    "tasks.folders.match.any": "mindestens eine",
    "tasks.folders.matchSuffix": "der folgenden Bedingungen erfüllen.",
    "tasks.folders.joiner.all": "und",
    "tasks.folders.joiner.any": "oder",
    "tasks.folders.addCondition": "Bedingung hinzufügen",
    "tasks.folders.removeCondition": "Bedingung entfernen",
    "tasks.folders.noValue": "kein Wert nötig",
    "tasks.folders.preview": "Vorschau",
    "tasks.folders.generatedFeel": "erzeugtes FEEL",
    "tasks.folders.matchCount.one": "1 Aufgabe",
    "tasks.folders.matchCount.other": "{n} Aufgaben",
    "tasks.folders.ofTotal": "von {n} offenen",
    "tasks.folders.ofTotalMore": "von über {n} offenen",
    "tasks.folders.countPending": "wird gezählt …",
    "tasks.folders.incomplete": "Bedingung noch unvollständig",
    "tasks.folders.save": "Ordner speichern",
    "tasks.folders.saved": "Ordner gespeichert",
    "tasks.folders.deleted": "Ordner gelöscht",
    "tasks.folders.saveFailed": "Speichern fehlgeschlagen: {error}",
    "tasks.folders.deleteFailed": "Löschen fehlgeschlagen: {error}",
    "tasks.folders.loadFailed": "Ordner konnten nicht geladen werden: {error}",
    "tasks.folders.confirmDelete": "Ordner „{name}“ löschen?",
    "tasks.folders.empty": "Keine Aufgabe passt zu diesem Ordner.",
    "tasks.folders.readOnly": "Dieser Ordner gehört {owner} und kann hier nur gelesen werden.",
    "tasks.folders.countsTruncated": "Zählung bei {n} Aufgaben abgebrochen — die Zahlen sind Mindestwerte.",

    "tasks.folders.field.process": "Prozess",
    "tasks.folders.field.taskName": "Aufgabe",
    "tasks.folders.field.assignee": "Zuständig",
    "tasks.folders.field.group": "Gruppe",
    "tasks.folders.field.lane": "Rolle (Lane)",
    "tasks.folders.field.priority": "Priorität",
    "tasks.folders.field.due": "Fälligkeit",
    "tasks.folders.field.instanceAge": "Vorgang läuft seit",
    "tasks.folders.field.form": "Formular",

    "tasks.folders.op.is": "ist",
    "tasks.folders.op.isNot": "ist nicht",
    "tasks.folders.op.isOneOf": "ist eines von",
    "tasks.folders.op.contains": "enthält",
    "tasks.folders.op.startsWith": "beginnt mit",
    "tasks.folders.op.isMe": "bin ich",
    "tasks.folders.op.isEmpty": "ist niemand",
    "tasks.folders.op.under": "liegt unter",
    "tasks.folders.op.atLeast": "ist mindestens",
    "tasks.folders.op.atMost": "ist höchstens",
    "tasks.folders.op.overdue": "ist überfällig",
    "tasks.folders.op.within": "fällig innerhalb",
    "tasks.folders.op.none": "ist nicht gesetzt",
    "tasks.folders.op.any": "ist gesetzt",
    "tasks.folders.op.olderThan": "mehr als",
    "tasks.folders.op.newerThan": "weniger als",
    "tasks.folders.op.has": "vorhanden",
    "tasks.folders.op.hasNot": "nicht vorhanden",

    "tasks.folders.priority.high": "Hoch (70)",
    "tasks.folders.priority.normal": "Normal (50)",
    "tasks.folders.priority.low": "Niedrig (30)",
    "tasks.folders.within.PT8H": "8 Stunden",
    "tasks.folders.within.P1D": "1 Tag",
    "tasks.folders.within.P3D": "3 Tagen",
    "tasks.folders.within.P7D": "7 Tagen",
    "tasks.folders.unit.h": "Stunden",
    "tasks.folders.unit.d": "Tagen",

    // The FEEL assistant (feel-assistant.js, ADR-draft-feel-assistant).
    "feel.title": "FEEL-Assistent",
    "feel.open": "FEEL-Assistent öffnen (Ctrl/⌘ ⇧ E)",
    "feel.for": "Für: {name}",
    "feel.close": "Schliessen (Esc)",
    "feel.new": "Neues Gespräch",
    "feel.chat.placeholder": "Was soll die Expression berechnen, und welche Variablen gibt es? (Ctrl+Enter sendet)",
    "feel.chat.send": "Senden",
    "feel.chat.intro": "Beschreibe, was die Expression berechnen soll. Jede Antwort wird vor der Anzeige mit der FEEL-Engine von Atlas kompiliert und gegen ein Beispiel ausgewertet.",
    "feel.chat.unavailable": "Kein KI-Worker eingerichtet. Editor, Test, Verlauf und Favoriten funktionieren trotzdem; für den Chat muss unter Workers ein KI-Worker angelegt sein.",
    "feel.chat.thinking": "Der KI-Worker schreibt und prüft die Expression …",
    "feel.chat.failed": "Anfrage fehlgeschlagen: {error}",
    "feel.chat.worker": "KI-Worker",
    "feel.chat.load": "In den Editor laden",
    "feel.chat.attempts": "nach {n} Versuchen",
    "feel.chat.warning": "Hinweis: {warning}",
    "feel.check.ok": "Geprüft: {result} ({kind})",
    "feel.check.matches": "wie erwartet",
    "feel.check.mismatch": "erwartet war {expected}",
    "feel.check.missing": "nicht gesetzt: {names}",
    "feel.check.error": "Fehler: {error}",
    "feel.editor.label": "Expression",
    "feel.vars.label": "Testvariablen (JSON)",
    "feel.vars.invalid": "Die Testvariablen sind kein gültiges JSON-Objekt.",
    "feel.empty": "Die Expression ist leer.",
    "feel.run": "Ausführen",
    "feel.copy": "Kopieren",
    "feel.copied": "Expression kopiert",
    "feel.copyFailed": "Kopieren nicht möglich",
    "feel.apply": "In Feld übernehmen",
    "feel.applied": "In „{name}“ übernommen",
    "feel.apply.gone": "Das Feld ist nicht mehr vorhanden. Bitte die Expression kopieren.",
    "feel.apply.unaryTests": "Eingabezellen einer Entscheidungstabelle erwarten einen Unary Test wie > 10, keine Expression. Bitte kopieren und anpassen.",
    "feel.target.field": "FEEL-Feld",
    "feel.target.fieldPrompt": "Feld „{name}“ eines BPMN-Elements im Modeler",
    "feel.target.dmnOutput": "Ausgabezelle (DMN)",
    "feel.target.dmnOutputPrompt": "Ausgabezelle einer DMN-Entscheidungstabelle",
    "feel.target.dmnExpression": "Input Expression (DMN)",
    "feel.target.dmnExpressionPrompt": "Input Expression einer Spalte einer DMN-Entscheidungstabelle",
    "feel.target.dmnLiteral": "Literal Expression (DMN)",
    "feel.target.dmnLiteralPrompt": "Literal Expression einer DMN-Entscheidung",
    "feel.target.dmnInput": "Eingabezelle (DMN)",
    "feel.target.dmnInputPrompt": "Eingabezelle einer DMN-Entscheidungstabelle (erwartet einen Unary Test, keine Expression)",
    "feel.favourite.add": "Als Favorit speichern",
    "feel.favourite.remove": "Favorit entfernen",
    "feel.history": "Verlauf",
    "feel.favourites": "Favoriten",
    "feel.list.emptyHistory": "Noch keine Expressions. Kopierte, übernommene, ausgeführte und vorgeschlagene Expressions erscheinen hier.",
    "feel.list.emptyFavourites": "Noch keine Favoriten. ☆ speichert die Expression im Editor.",
    "feel.list.rename": "Umbenennen",
    "feel.list.remove": "Entfernen",
    "feel.list.clear": "Verlauf leeren",
    "feel.list.local": "Nur in diesem Browser gespeichert.",
  },

  // English is here to prove the mechanism carries a second language rather than
  // to be selected by default: a viewer gets it only by asking for it.
  en: {
    "common.cancel": "Cancel",
    "common.save": "Save",
    "common.delete": "Delete",
    "common.edit": "Edit",
    "common.close": "Close",

    "tasks.folders.mine": "My folders",
    "tasks.folders.shared": "Shared",
    "tasks.folders.new": "New folder",
    "tasks.opened": "Opened",
    "tasks.filter": "Filter tasks (name, person, content)…",
    "tasks.folders.newTitle": "New folder",
    "tasks.folders.editTitle": "Edit folder",
    "tasks.folders.rule": "Rule",
    "tasks.folders.nameLabel": "Folder name",
    "tasks.folders.namePlaceholder": "e.g. Customer enquiries",
    "tasks.folders.visibility": "Visible to",
    "tasks.folders.visibility.private": "Only me",
    "tasks.folders.visibility.org": "Everyone in the organisation",
    "tasks.folders.visibility.group": "Group: {name}",
    "tasks.folders.conditions": "Conditions",
    "tasks.folders.matchPrefix": "Show tasks matching",
    "tasks.folders.match.all": "all",
    "tasks.folders.match.any": "at least one",
    "tasks.folders.matchSuffix": "of the following conditions.",
    "tasks.folders.joiner.all": "and",
    "tasks.folders.joiner.any": "or",
    "tasks.folders.addCondition": "Add condition",
    "tasks.folders.removeCondition": "Remove condition",
    "tasks.folders.noValue": "no value needed",
    "tasks.folders.preview": "Preview",
    "tasks.folders.generatedFeel": "generated FEEL",
    "tasks.folders.matchCount.one": "1 task",
    "tasks.folders.matchCount.other": "{n} tasks",
    "tasks.folders.ofTotal": "of {n} open",
    "tasks.folders.ofTotalMore": "of more than {n} open",
    "tasks.folders.countPending": "counting…",
    "tasks.folders.incomplete": "This condition is not finished yet",
    "tasks.folders.save": "Save folder",
    "tasks.folders.saved": "Folder saved",
    "tasks.folders.deleted": "Folder deleted",
    "tasks.folders.saveFailed": "Save failed: {error}",
    "tasks.folders.deleteFailed": "Delete failed: {error}",
    "tasks.folders.loadFailed": "Could not load folders: {error}",
    "tasks.folders.confirmDelete": "Delete the folder “{name}”?",
    "tasks.folders.empty": "No task matches this folder.",
    "tasks.folders.readOnly": "This folder belongs to {owner} and is read-only here.",
    "tasks.folders.countsTruncated": "Counting stopped at {n} tasks — the numbers are a floor.",

    "tasks.folders.field.process": "Process",
    "tasks.folders.field.taskName": "Task",
    "tasks.folders.field.assignee": "Assignee",
    "tasks.folders.field.group": "Group",
    "tasks.folders.field.lane": "Role (lane)",
    "tasks.folders.field.priority": "Priority",
    "tasks.folders.field.due": "Due date",
    "tasks.folders.field.instanceAge": "Instance running for",
    "tasks.folders.field.form": "Form",

    "tasks.folders.op.is": "is",
    "tasks.folders.op.isNot": "is not",
    "tasks.folders.op.isOneOf": "is one of",
    "tasks.folders.op.contains": "contains",
    "tasks.folders.op.startsWith": "starts with",
    "tasks.folders.op.isMe": "is me",
    "tasks.folders.op.isEmpty": "is nobody",
    "tasks.folders.op.under": "is under",
    "tasks.folders.op.atLeast": "is at least",
    "tasks.folders.op.atMost": "is at most",
    "tasks.folders.op.overdue": "is overdue",
    "tasks.folders.op.within": "is due within",
    "tasks.folders.op.none": "is not set",
    "tasks.folders.op.any": "is set",
    "tasks.folders.op.olderThan": "more than",
    "tasks.folders.op.newerThan": "less than",
    "tasks.folders.op.has": "present",
    "tasks.folders.op.hasNot": "absent",

    "tasks.folders.priority.high": "High (70)",
    "tasks.folders.priority.normal": "Normal (50)",
    "tasks.folders.priority.low": "Low (30)",
    "tasks.folders.within.PT8H": "8 hours",
    "tasks.folders.within.P1D": "1 day",
    "tasks.folders.within.P3D": "3 days",
    "tasks.folders.within.P7D": "7 days",
    "tasks.folders.unit.h": "hours",
    "tasks.folders.unit.d": "days",

    "feel.title": "FEEL assistant",
    "feel.open": "Open the FEEL assistant (Ctrl/⌘ ⇧ E)",
    "feel.for": "For: {name}",
    "feel.close": "Close (Esc)",
    "feel.new": "New conversation",
    "feel.chat.placeholder": "What should the expression compute, and which variables are there? (Ctrl+Enter sends)",
    "feel.chat.send": "Send",
    "feel.chat.intro": "Describe what the expression should compute. Every answer is compiled by Atlas's FEEL engine and evaluated against an example before you see it.",
    "feel.chat.unavailable": "No AI Worker is configured. The editor, the test, the history and the favourites still work; the chat needs an AI Worker under Workers.",
    "feel.chat.thinking": "The AI Worker is writing and checking the expression…",
    "feel.chat.failed": "Request failed: {error}",
    "feel.chat.worker": "AI Worker",
    "feel.chat.load": "Load into the editor",
    "feel.chat.attempts": "after {n} attempts",
    "feel.chat.warning": "Note: {warning}",
    "feel.check.ok": "Checked: {result} ({kind})",
    "feel.check.matches": "as expected",
    "feel.check.mismatch": "expected {expected}",
    "feel.check.missing": "not set: {names}",
    "feel.check.error": "Error: {error}",
    "feel.editor.label": "Expression",
    "feel.vars.label": "Test variables (JSON)",
    "feel.vars.invalid": "The test variables are not a valid JSON object.",
    "feel.empty": "The expression is empty.",
    "feel.run": "Run",
    "feel.copy": "Copy",
    "feel.copied": "Expression copied",
    "feel.copyFailed": "Could not copy",
    "feel.apply": "Apply to field",
    "feel.applied": "Applied to “{name}”",
    "feel.apply.gone": "The field is no longer there. Copy the expression instead.",
    "feel.apply.unaryTests": "Input cells of a decision table take a unary test such as > 10, not an expression. Copy it and adapt it.",
    "feel.target.field": "FEEL field",
    "feel.target.fieldPrompt": "the field “{name}” of a BPMN element in the Modeler",
    "feel.target.dmnOutput": "Output cell (DMN)",
    "feel.target.dmnOutputPrompt": "an output cell of a DMN decision table",
    "feel.target.dmnExpression": "Input expression (DMN)",
    "feel.target.dmnExpressionPrompt": "the input expression of a column of a DMN decision table",
    "feel.target.dmnLiteral": "Literal expression (DMN)",
    "feel.target.dmnLiteralPrompt": "the literal expression of a DMN decision",
    "feel.target.dmnInput": "Input cell (DMN)",
    "feel.target.dmnInputPrompt": "an input cell of a DMN decision table (takes a unary test, not an expression)",
    "feel.favourite.add": "Save as favourite",
    "feel.favourite.remove": "Remove favourite",
    "feel.history": "History",
    "feel.favourites": "Favourites",
    "feel.list.emptyHistory": "No expressions yet. Expressions you copy, apply, run or are proposed appear here.",
    "feel.list.emptyFavourites": "No favourites yet. ☆ saves the expression in the editor.",
    "feel.list.rename": "Rename",
    "feel.list.remove": "Remove",
    "feel.list.clear": "Clear history",
    "feel.list.local": "Stored in this browser only.",
  },
};

// LOCALE_KEY is where a deliberate choice is remembered. It is per browser, like
// the other per-viewer preferences the console keeps (the task list width, the
// sort order): a language is how one person reads, not how the instance is
// configured.
const LOCALE_KEY = "atlas.locale";

// pick resolves the locale once, in the order a choice was made: an explicit
// ?lang= in the URL (which also records itself, so a shared link keeps working
// after a reload), then a remembered choice, then the default. The browser's own
// language is deliberately not consulted — see the note at the top.
function pick() {
  try {
    const url = new URLSearchParams(location.search).get("lang");
    if (url && CATALOGS[url]) {
      localStorage.setItem(LOCALE_KEY, url);
      return url;
    }
    const saved = localStorage.getItem(LOCALE_KEY);
    if (saved && CATALOGS[saved]) return saved;
  } catch { /* private mode, or no storage — the default is a fine answer */ }
  return DEFAULT_LOCALE;
}

let current = pick();

// locale returns the active locale id.
export function locale() { return current; }

// locales returns the locale ids that have a catalogue.
export function locales() { return Object.keys(CATALOGS); }

// setLocale switches language and remembers the choice. It does not re-render
// anything: the caller knows what is on screen.
export function setLocale(id) {
  if (!CATALOGS[id]) return false;
  current = id;
  try { localStorage.setItem(LOCALE_KEY, id); } catch { /* nothing to remember it with */ }
  return true;
}

// t looks up a message and fills its {placeholders}.
//
// An unknown key renders as the key itself. That is deliberate: a hole in the
// catalogue then shows up as `tasks.folders.save` on the screen, where somebody
// notices it, instead of silently falling through to a language the viewer did
// not choose.
export function t(key, params) {
  const table = CATALOGS[current] || CATALOGS[DEFAULT_LOCALE];
  let s = table[key];
  if (s === undefined) s = key;
  if (!params) return s;
  return s.replace(/\{(\w+)\}/g, (m, name) => (name in params ? String(params[name]) : m));
}

// plural picks between the one and other forms of a counted message. German and
// English agree on the shape (one / other), which is as much as this needs;
// a locale that needs more forms brings its own rule rather than bending this one.
export function plural(keyBase, n, params) {
  const form = n === 1 ? ".one" : ".other";
  return t(keyBase + form, Object.assign({ n }, params));
}

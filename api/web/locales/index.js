// The console's locales: one module per language, each an object of messages keyed
// by id (ADR-0267, ADR-0497).
//
// Adding a language is adding its file beside these and naming it here — two lines.
// Everything else follows from this list: the language picker offers it under the
// name its file gives itself (`locale.name`), the server accepts it as a person's
// choice (it reads the same directory), and the Go tests hold the new file to every
// key the German default carries, with the same {placeholders}. A language that is
// not complete cannot ship: a missing message renders as its key, by design, so the
// test is what keeps a screen from showing ids.
//
// German is first because it is the default; the order is otherwise the picker's.
import de from "./de.js";
import en from "./en.js";

export const CATALOGS = { de, en };

package formgen

import (
	"fmt"
	"strings"
)

// What the model is told.
//
// Two prompts, and the split is the same one the runtime agent makes: the *system*
// prompt is the contract — what to answer with, and out of what vocabulary — and it is
// Atlas's, fixed, the same for every generation. The *goal* is this request: what the
// author typed, what the process says about itself, and what the form already looks like
// when this is a refinement rather than a first draft.
//
// The contract is written against the gate in schema.go, and the two are held together
// by a test rather than by discipline: every type the gate accepts is offered here, and
// nothing the gate refuses is. A prompt that offers a component the gate then rejects
// would fail generations for a reason nobody could see from either file alone.

// systemPrompt is the contract. It replaces what the adapters would otherwise say about
// being one step inside a running instance (agent.Request.System) — there is no instance
// here, no token and no variable to answer into, and a model told otherwise asks which
// case it is working on.
func systemPrompt() string {
	return `You write forms for Atlas, a BPMN workflow engine. A form is filled in by a person: it either starts a process or completes one of its human steps. What they type lands in the process's variables.

Answer with one JSON document and nothing else — no explanation before it, no offer to adjust it after, no markdown fence.

The document is a form-js schema:

  {"type":"default","components":[ … ]}

A component is an object with a "type". Anything that asks a question also has a "key" — the process variable the answer lands in — and a "label". Use only these types:

  text        markdown prose, not a question: {"type":"text","text":"# Urlaubsantrag\n\nWofür dieses Formular da ist."}
  textfield   one line
  textarea    several lines
  number      {"type":"number","key":"betrag","label":"Betrag","decimalDigits":2}
  checkbox    one yes/no
  select      one of many; needs "values":[{"label":"Erholung","value":"erholung"}]
  radio       one of a few; needs "values"
  checklist   several of many; needs "values"
  taglist     several, entered freely, out of "values"
  datetime    {"type":"datetime","key":"von","label":"Von","subtype":"date"} — subtype is "date", "time" or "datetime"
  group       {"type":"group","label":"Adresse","components":[ … ]} — fields that belong together
  dynamiclist a group the person can repeat
  separator   a horizontal rule
  spacer      vertical room

Any component may carry "description" (a hint shown under the field) and "validate":
{"required":true}; a textfield also takes {"validationType":"email"} or {"validationType":"phone"}; a number takes {"min":0,"max":30}.

How to write it:

- Write labels, descriptions and prose in the language of the request. Do not translate the domain's own words.
- A key is a technical name: ASCII letters and digits, lowerCamelCase, no spaces or umlauts. Where a field means a datum the process already names, use that name as the key.
- Open with one "text" component: a "# " heading and one sentence saying what the form is for and what happens after it is submitted.
- Ask for what the process needs and nothing more. Every extra field is somebody's time, every day.
- Mark a field required only where the process genuinely cannot continue without it.
- Prefer a select or radio with real options over a free-text field whenever the set of answers is known.`
}

// goalPrompt is this request: the author's brief, the process's own account of itself,
// and the form as it stands when there is one.
//
// The brief leads because it is the only part that says what this particular form is
// for. Everything after it is material — and material a model reads before the question
// is material competing with it.
func goalPrompt(req Request, p Process, current string) string {
	var b strings.Builder
	if brief := strings.TrimSpace(req.Description); brief != "" {
		b.WriteString("What the form is for\n--------------------\n")
		b.WriteString(brief)
	} else {
		// No prose is a complete request in its own right — "a start form for this
		// process" says everything — so it gets a sentence rather than an empty
		// heading.
		b.WriteString("What the form is for\n--------------------\n")
		if req.ElementID != "" {
			b.WriteString("No further brief was given: write the form the step named below needs.")
		} else {
			b.WriteString("No further brief was given: write the form that starts this process.")
		}
	}
	if outline := p.Describe(req.ElementID); outline != "" {
		fmt.Fprintf(&b, "\n\nThe process\n-----------\n%s", outline)
	}
	if current = strings.TrimSpace(current); current != "" {
		fmt.Fprintf(&b, "\n\nThe form as it stands\n---------------------\n%s\n\n"+
			"Change it as the brief asks and return the whole document, not only the change.", current)
	}
	return b.String()
}

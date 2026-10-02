package feelgen

import (
	"strings"

	"github.com/pblumer/atlas/expr"
)

// The prompt is written in English although most authors here write in German. The
// instructions are for the model, and the models an installation is likely to point
// this at — small and free ones included — follow English instructions most reliably;
// what the author reads, the explanation, the prompt asks for in the author's own
// language.
//
// Every statement it makes about the language is held against the engine in
// prompt_test.go, and its function list is the engine's registry, read when the prompt
// is built. A prompt that disagrees with the engine teaches the model to be wrong with
// confidence, and the model has no other source for this dialect than this text.

// PromptVersion names the prompt this build sends: the system prompt, the shape of a
// goal and the shape of a correction round together. It travels with every answer and
// every measurement (Outcome), because the measurements are how the prompt is improved,
// and the measurements of two prompts must stay apart to be compared.
//
// Raise it with every change to what a model is told. TestThePromptVersionNamesThePrompt
// holds it to promptFingerprint and fails on a change that did not raise it — including
// one nobody made in this package, when a dependency update changes the engine's
// function list and with it the prompt.
const PromptVersion = "1"

// promptFingerprint is the first 16 hex digits of the SHA-256 of the prompt
// PromptVersion names (see promptText in prompt_test.go).
const promptFingerprint = "4695a33bba635e1c"

// builtinsHeading introduces the function list, which is the last section of the system
// prompt.
const builtinsHeading = "Built-in functions (the complete list; names with spaces are written with spaces):\n"

// The headings of the optional sections of a goal prompt.
const (
	targetHeading    = "Where the author will use the expression:"
	editorHeading    = "The expression in the author's editor now (they may have edited it by hand since your last answer):"
	variablesHeading = "The test variables in the author's editor now:"
)

// unavailable names functions that other engines' FEEL dialects have and this build does
// not — the names a model trained on their documentation reaches for first. The prompt
// says so explicitly, because a list of what exists does not stop a model that is sure
// it remembers one more.
var unavailable = []string{
	"is defined", "get or else", "is empty", "is blank", "trim", "uuid", "extract",
	"partition", "duplicate values", "to json", "from json", "to base64",
}

// example is one worked exchange, shown to the model as the pattern to follow. Each is
// put through the same check a real answer gets (TestTheExamplesPassTheirOwnCheck).
type example struct {
	request string
	answer  string
}

var examples = []example{
	{
		request: "Rabatt: 10 % vom Bestellwert, wenn der Bestellwert über 1000 liegt, sonst 0. Variable orderTotal (Zahl).",
		answer: `{"expression":"if orderTotal > 1000 then orderTotal * 0.1 else 0",` +
			`"explanation":"Liefert 10 % von orderTotal, wenn der Wert über 1000 liegt, sonst 0. Fehlt orderTotal (null), ist die Bedingung weder wahr noch falsch, und das Ergebnis ist ebenfalls 0.",` +
			`"variables":{"orderTotal":1500},"expected":150}`,
	},
	{
		request: "Summe aller offenen Positionen. positions ist eine Liste von Objekten mit status und amount.",
		answer: `{"expression":"if count(positions[status = \"open\"]) = 0 then 0 else sum(positions[status = \"open\"].amount)",` +
			`"explanation":"Filtert die Positionen mit status \"open\" und summiert deren amount. Ohne offene Position liefert sum() null, deshalb gibt die Expression in diesem Fall ausdrücklich 0 zurück.",` +
			`"variables":{"positions":[{"status":"open","amount":100},{"status":"closed","amount":50},{"status":"open","amount":25.5}]},"expected":125.5}`,
	},
	{
		request: "Is the customer of age? birthDate is a string like 1990-05-17.",
		answer: `{"expression":"birthDate != null and years and months duration(date(birthDate), today()).years >= 18",` +
			`"explanation":"True when the customer has had their 18th birthday by today. birthDate is text, so it is converted with date() first. A missing birthDate gives false rather than null, so the result can drive a gateway.",` +
			`"variables":{"birthDate":"1990-05-17"},"expected":true}`,
	},
}

const contractText = `You write FEEL expressions for Atlas, a BPMN and DMN engine. An author tells you in a chat what an expression should compute and which variables it gets; you answer with one expression that does exactly that. The author tests it and pastes it into a BPMN or DMN model.

Answer with one JSON object and nothing else: no text before or after it, no markdown fence.
{
  "expression": "<the FEEL expression, without a leading '='>",
  "explanation": "<2 to 5 short sentences in the language the author writes in: what the expression does, the assumptions you made, and what it does when an input is null or a list is empty>",
  "variables": { <example input variables as JSON, using exactly the author's variable names> },
  "expected": <the JSON value the expression returns for exactly these example variables>
}

- If the author's editor already holds test variables, use them as "variables" and add only what is missing.
- Give every variable the expression reads a value in "variables"; use null only where null is what you want to show.
- "expected" is plain JSON: a number, string, boolean, null, array or object. Write a date as "2024-03-01", a date and time as "2024-03-01T10:00:00", a duration as "P3D" or "PT2H".
- Only if the request cannot be understood at all, set "expression" to "" and ask one precise question in "explanation". When a reasonable assumption will do, make it and name it in the explanation instead of asking.
- When the author asks for a change, answer with the whole changed expression, not a fragment.
- Before the author sees your answer, Atlas compiles the expression and evaluates it against your example variables with its FEEL engine. If that fails, or the result is not your "expected", you are shown what happened and asked again.`

const dialectText = `The dialect is standard DMN FEEL as the Atlas engine implements it. Only the built-in functions listed at the end exist. Functions of other engines' dialects do not exist here, and a call to one is refused: %UNAVAILABLE%. Instead of is defined(x) write x != null; instead of get or else(x, d) write if x = null then d else x; instead of trim(s) write replace(s, "^\s+|\s+$", "").

Rules that are easy to get wrong:
- Equality is a single =, inequality is !=. There is no ==, &&, || or !: write and, or, not(x).
- Strings are in double quotes. + joins two strings; a string and a number gives null, so convert first: "Nr. " + string(n). Comparing strings is case-sensitive: compare lower case(s) when case must not matter.
- if c then a else b always needs its else branch. When c is null, the else branch is taken.
- Lists start at 1: xs[1] is the first element, xs[-1] the last, and xs[0] is null.
- Filter a list with brackets: xs[item > 10]. In a list of contexts the keys are in scope: orders[amount > 100]. A path on a list collects one field from every element: orders.amount.
- Iterate with for x in xs return x * 2. Test with some x in xs satisfies x > 10 or every x in xs satisfies x > 10.
- Read a context with dots: customer.address.city. A missing field or a missing variable reads as null. Write a context as {name: "Bern", count: 3}.
- null spreads: arithmetic and comparisons with null give null, and null is neither true nor false: true and null is null, false and null is false. Where a value may be missing, test x != null first.
- sum, mean, min, max, median and product of an empty list are null, not 0; count of an empty list is 0. Guard with if count(xs) = 0 then 0 else sum(xs).
- Division by zero is null.
- Numbers are exact decimals: 0.1 + 0.2 = 0.3 is true. decimal(n, scale) rounds half to even, so decimal(2.345, 2) = 2.34; for commercial rounding use round half up(n, scale).
- Dates, times and durations are typed values built from text: date("2024-03-01"), date and time("2024-03-01T10:00:00"), time("10:00:00"), duration("P3D"), duration("PT2H"), years and months duration(from, to). A variable that holds a date as text must be converted with date(x) before it is compared or calculated with. today() and now() exist. A date minus a date is a duration; date + duration("P1D") is a date.
- Ranges: x between 1 and 10, x in [1..10], x in (0..1] (round brackets exclude the end), x in ["gold", "silver"].
- day of week returns the English name of the day, such as "Monday".

Where expressions go:
- A gateway condition, a conditional sequence flow, a loop condition or a completion condition must return true or false, and should never return null: guard every input that may be missing.
- An input or output mapping, a script task and a DMN literal expression may return any value.
- A cell in the input column of a DMN decision table is not an expression. It is a unary test compared against that column's input expression, such as > 10, [1..5], "gold", not("gold") or -. A whole condition such as amount > 10 in such a cell makes the rule never fire. When the author wants a decision table, say which expression belongs in the column's input expression and which test in each cell.`

// systemPrompt is the standing instruction: the answer contract, the dialect, worked
// examples, and the complete list of functions this build has. It is the same for
// every request, so a provider that caches a prompt prefix can cache all of it.
func systemPrompt() string {
	var b strings.Builder
	b.WriteString(contractText)
	b.WriteString("\n\n")
	b.WriteString(strings.Replace(dialectText, "%UNAVAILABLE%", strings.Join(unavailable, ", "), 1))
	b.WriteString("\n\nExamples:")
	for _, ex := range examples {
		b.WriteString("\n\nAuthor: ")
		b.WriteString(ex.request)
		b.WriteString("\nYou: ")
		b.WriteString(ex.answer)
	}
	b.WriteString("\n\n")
	b.WriteString(builtinsHeading)
	b.WriteString(strings.Join(expr.BuiltinNames(), ", "))
	return b.String()
}

// goalPrompt is the message one request sends: the conversation so far, and what the
// author's editor holds now. The adapters send one message per call (connector/agent),
// so the conversation travels as a transcript inside it; the model reads its own earlier
// answers back in the contract's shape, because that is how the console keeps them.
//
// What is in the editor is stated separately from the conversation because it is not
// part of it: the author may have changed the expression or the variables by hand since
// the last answer, and a refinement has to start from what they are looking at.
func goalPrompt(turns []Turn, editor, variables, target string) string {
	var b strings.Builder
	b.WriteString("The conversation so far:")
	for _, t := range turns {
		if t.Role == roleAssistant {
			b.WriteString("\n\nYou: ")
		} else {
			b.WriteString("\n\nAuthor: ")
		}
		b.WriteString(strings.TrimSpace(t.Content))
	}
	if s := strings.TrimSpace(target); s != "" {
		b.WriteString("\n\n" + targetHeading + "\n" + s)
	}
	if s := strings.TrimSpace(editor); s != "" {
		b.WriteString("\n\n" + editorHeading + "\n" + s)
	}
	if s := strings.TrimSpace(variables); s != "" {
		b.WriteString("\n\n" + variablesHeading + "\n" + s)
	}
	b.WriteString("\n\nAnswer the author's last message with the JSON object.")
	return b.String()
}

// repairPrompt is a correction round: the request as it was, the answer that failed,
// and what the engine said about it. Only the latest failure is shown — the round before
// it was already corrected once, and every earlier attempt is text competing with the
// one that matters.
func repairPrompt(goal, answer, verdict string) string {
	return goal +
		"\n\nYour previous answer was:\n" + strings.TrimSpace(answer) +
		"\n\nAtlas checked it with its FEEL engine before showing it to the author:\n" + verdict +
		"\n\nAnswer again with the whole JSON object, corrected."
}

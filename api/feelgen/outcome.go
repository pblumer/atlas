package feelgen

import "time"

// What a request came to, for whoever is improving the prompt.
//
// The prompt is the one part of this feature that is tuned rather than designed, and
// tuning it by impression is how a prompt grows sentences nobody can justify. So every
// request that reached a model reports how it went: how many rounds it took, what each
// round failed on, whether the model kept to the contract, and which foreign functions
// it reached for. The server turns that into a log line and, where metrics are on, into
// counters (ADR-0142) — and the open question of ADR-0445, whether a
// small free model is good enough, becomes a number somebody can read.
//
// What it deliberately does not carry is what the author wrote or what the model
// wrote: a conversation may hold anything, and an expression may hold a literal the
// author typed. The engine's verdicts and the categories are enough to tell a prompt
// that teaches the dialect from one that does not.

// The results a request can come to. Each is a label value of the server's request
// counter, so the set is closed (ADR-0142's bounded-cardinality rule).
const (
	// ResultSettled: an expression that runs, binds its inputs, and returns what the
	// model said it returns.
	ResultSettled = "settled"
	// ResultQuestion: the model asked back instead of writing an expression.
	ResultQuestion = "question"
	// ResultUnsettled: every round was used and the last answer still failed its check.
	ResultUnsettled = "unsettled"
	// ResultCutShort: a correction round could not run — the endpoint refused it, or
	// answered with nothing usable — so the author got an earlier, failed answer.
	ResultCutShort = "cut_short"
	// ResultUnanswered: the first round produced nothing usable.
	ResultUnanswered = "unanswered"
)

// The faults a round can have, in the order the check looks for them.
const (
	FaultNone     = "none"
	FaultUnusable = "unusable" // no answer, an endpoint error, or an answer with nothing in it
	FaultEmpty    = "empty"    // an answer with neither an expression nor an explanation
	FaultCompile  = "compile"
	FaultCalls    = "calls"    // a call the deploy would refuse (ADR-0388)
	FaultEvaluate = "evaluate" // compiled, and failed while evaluating
	FaultMissing  = "missing"  // reads an input the example does not bind
	FaultMismatch = "mismatch" // returns something other than what the model said
)

// The forms an answer can take (see ParseAnswer). How often a model keeps to the
// contract is the first thing a change to the prompt's wording moves.
const (
	FormatContract  = "contract"
	FormatCodeBlock = "code_block"
	FormatProse     = "prose"
	FormatUnusable  = "unusable"
)

// Results, Faults and Formats are the closed lists the server pre-resolves its metrics
// from. TestTheLabelsAreClosed holds them to the constants above.
var (
	Results = []string{ResultSettled, ResultQuestion, ResultUnsettled, ResultCutShort, ResultUnanswered}
	Faults  = []string{FaultNone, FaultUnusable, FaultEmpty, FaultCompile, FaultCalls, FaultEvaluate, FaultMissing, FaultMismatch}
	Formats = []string{FormatContract, FormatCodeBlock, FormatProse, FormatUnusable}
)

// Outcome is one request that reached a model.
type Outcome struct {
	Worker string
	// Model is the language model that was asked: the Worker's own, or the one the
	// request chose. Comparing models is the second thing the data is for.
	Model string
	// Prompt is the PromptVersion that was sent.
	Prompt   string
	Result   string
	Attempts []Attempt
	Duration time.Duration
}

// Attempt is one round.
type Attempt struct {
	Format string
	Fault  string
	// Error is the engine's verdict, or the reason the round produced nothing usable.
	// Empty for a round without a fault.
	Error string
	// Calls are the callees the check refused, by name — the foreign functions a
	// model reached for.
	Calls []string
}

// attemptOf classifies one round from what it produced.
func attemptOf(p Proposal, c Check) Attempt {
	a := Attempt{Format: p.Format, Fault: FaultNone}
	switch {
	case settled(p, c):
	case p.Expression == "":
		a.Fault = FaultEmpty
	case !c.OK:
		a.Fault, a.Error, a.Calls = c.Fault, c.Error, c.Calls
	case len(c.Missing) > 0:
		a.Fault, a.Error = FaultMissing, problem(p, c)
	default:
		a.Fault, a.Error = FaultMismatch, problem(p, c)
	}
	return a
}

// unusable is a round that produced nothing to check.
func unusable(err error) Attempt {
	return Attempt{Format: FormatUnusable, Fault: FaultUnusable, Error: err.Error()}
}

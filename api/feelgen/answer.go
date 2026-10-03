package feelgen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Proposal is one answer of the model, read into the contract the system prompt asks
// for: an expression, an explanation, the example it was written against and what it
// returns for that example. Any part may be missing — a question back has no
// expression, a bare code block has no example — and the check decides what that means.
type Proposal struct {
	Expression  string
	Explanation string
	// Variables is the model's example, decoded with exact numbers. Nil when it gave
	// none, or gave something that is not an object and so cannot bind by name.
	Variables map[string]any
	// Expected is what the model says the expression returns for Variables, and
	// HasExpected whether it said anything at all — "expected": null is a claim too.
	Expected    any
	HasExpected bool
	// Format is which reading of the answer succeeded: the contract, a code block, or
	// plain prose (FormatContract, FormatCodeBlock, FormatProse).
	Format string
}

// ParseAnswer reads what a model wrote into a [Proposal]. It forgives as much as can be
// forgiven without guessing, because the models an installation points this at are not
// all disciplined about a format:
//
//   - the contract as a JSON object, bare, in a fence, or with prose around it;
//   - failing that, an expression in a code block, with the prose around it as the
//     explanation;
//   - failing that, plain prose, which is the model talking — a question back, or a
//     refusal — and is shown as that, with no expression.
//
// What it does not forgive is an answer longer than max or one with nothing in it.
func ParseAnswer(answer string, max int64) (Proposal, error) {
	if int64(len(answer)) > max {
		return Proposal{}, fmt.Errorf("the answer is %d bytes, more than the %d an expression could need", len(answer), max)
	}
	text := strings.TrimSpace(answer)
	if text == "" {
		return Proposal{}, errors.New("the answer is empty")
	}
	if p, ok := contract(text); ok {
		p.Format = FormatContract
		return p, nil
	}
	if code, rest, ok := codeBlock(text); ok {
		return Proposal{Expression: bareExpression(code), Explanation: rest, Format: FormatCodeBlock}, nil
	}
	return Proposal{Explanation: text, Format: FormatProse}, nil
}

// contract reads the JSON object the system prompt asks for. It is found the way form
// generation finds its document (ADR-0260): inside the first fence if there is one, and
// from the first brace to the last. A FEEL context literal has braces too and is not
// JSON, which is why a failed decode is "not the contract" rather than an error — the
// code-block reading gets its turn.
func contract(text string) (Proposal, bool) {
	s := text
	if code, _, ok := codeBlock(text); ok {
		s = code
	}
	start, end := strings.IndexByte(s, '{'), strings.LastIndexByte(s, '}')
	if start < 0 || end <= start {
		return Proposal{}, false
	}
	var raw struct {
		Expression  *string         `json:"expression"`
		Explanation string          `json:"explanation"`
		Variables   json.RawMessage `json:"variables"`
		Expected    json.RawMessage `json:"expected"`
	}
	if err := json.Unmarshal([]byte(s[start:end+1]), &raw); err != nil || raw.Expression == nil {
		return Proposal{}, false
	}
	p := Proposal{Expression: bareExpression(*raw.Expression), Explanation: strings.TrimSpace(raw.Explanation)}
	if vars, ok := decodeExact(raw.Variables).(map[string]any); ok {
		p.Variables = vars
	}
	if len(raw.Expected) > 0 {
		p.Expected, p.HasExpected = decodeExact(raw.Expected), true
	}
	return p, true
}

// decodeExact decodes JSON keeping numbers as json.Number, which is what the FEEL
// bridge reads exactly (expr.FromJSON). Undecodable input is nil.
func decodeExact(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil
	}
	return v
}

// codeBlock returns the content of the first fenced block in text, without its
// language tag, and the prose around it.
func codeBlock(text string) (code, rest string, ok bool) {
	open := strings.Index(text, "```")
	if open < 0 {
		return "", "", false
	}
	body := text[open+3:]
	// ```feel — the language tag is on the fence line, not in the code.
	if nl := strings.IndexByte(body, '\n'); nl >= 0 {
		body = body[nl+1:]
	}
	closing := strings.Index(body, "```")
	after := ""
	if closing >= 0 {
		after = body[closing+3:]
		body = body[:closing]
	}
	code = strings.TrimSpace(body)
	if code == "" {
		return "", "", false
	}
	rest = strings.TrimSpace(strings.TrimSpace(text[:open]) + "\n\n" + strings.TrimSpace(after))
	return code, rest, true
}

// bareExpression strips the "=" Zeebe puts in front of an expression field. It belongs
// to the field and not to the expression: the Modeler's fx fields add their own when an
// expression is applied to them, and the engine reads one leading "=" as a syntax error.
func bareExpression(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "=") && !strings.HasPrefix(s, "==") {
		s = strings.TrimSpace(s[1:])
	}
	return s
}

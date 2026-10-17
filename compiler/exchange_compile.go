package compiler

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/connector/exchange/catalog"
)

// The listing bounds an Exchange task inherits when it authors none.
//
// maxResults defaults on for the reason Entra's maxUsers does: an unbounded mailbox
// listing into a process variable is the failure this hardens against, and a truncated
// one would be a wrong answer rather than a partial one. The ceiling is a deploy check
// rather than an Exchange limit: a hundred thousand recipients is a directory export,
// not a process variable.
const (
	defaultExchangeMaxResults int32 = 1000
	maxExchangeMaxResults     int32 = 100_000
)

// compileExchangeConnectorTask compiles an <atlas:exchangeConnector> task: one Exchange
// administration operation through a Worker an operator configured
// (ADR-draft-exchange-worker).
//
// The operation table is connector/exchange/catalog's, read here directly rather than
// restated: unlike the other Worker Types' tables it imports nothing of Atlas's, so the
// compiler can, and the rules a deploy enforces are then the very rows the worker
// builds its calls from.
func compileExchangeConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.Exchange
	fail := func(format string, args ...any) (int32, error) {
		return 0, fmt.Errorf("compiler: exchange task %q "+format, append([]any{st.Id}, args...)...)
	}
	if strings.TrimSpace(cn.Connector) == "" {
		return fail("needs a connector attribute naming the Exchange Worker that holds the organization's credential")
	}
	// The Worker is normally a static name, and may be FEEL so one process can
	// administer more than one organization — an Online tenant and the Exchange Server
	// beside it in a hybrid one — choosing from its own variables at call time.
	workerExpr, err := connectorValue(b.gate(), st.Id, "exchange worker", "connector", cn.Connector)
	if err != nil {
		return 0, err
	}
	name := strings.ToLower(strings.TrimSpace(cn.Operation))
	if name == "" {
		return fail("needs an operation (%s)", strings.Join(catalog.Names(), ", "))
	}
	op, ok := catalog.Lookup(name)
	if !ok {
		return fail("has an unknown operation %q (want %s)", cn.Operation, strings.Join(catalog.Names(), ", "))
	}

	// The cmdlet is compiled structure, never FEEL: it is what decides whether a call
	// reads or changes, and that is a property a deploy must be able to see.
	cmdlet := strings.TrimSpace(cn.Cmdlet)
	switch {
	case op.Generic && cmdlet == "":
		return fail("operation %q needs a cmdlet (a Get- cmdlet)", name)
	case op.Generic && !catalog.ValidReadCmdlet(cmdlet):
		return fail("operation %q runs Get- cmdlets only, not %q; a change goes through the operation that names it", name, cmdlet)
	case !op.Generic && cmdlet != "":
		return fail("operation %q takes no cmdlet; remove it rather than leaving a value the Worker ignores", name)
	}

	inline := strings.TrimSpace(cn.Parameters) != ""
	fromVar := strings.TrimSpace(cn.ParametersVariable) != ""
	if inline && fromVar {
		return fail("sets both inline parameters and a parametersVariable; use one, not both")
	}
	var params RestExpr
	switch {
	case inline:
		template, err := exchangeTemplate(st.Id, cn.Parameters)
		if err != nil {
			return 0, err
		}
		if err := op.CheckAuthored(name, template); err != nil {
			return fail("%s", strings.TrimPrefix(err.Error(), "exchange: "))
		}
		feel, err := jsonTemplateToFeel(template)
		if err != nil {
			return fail("parameters: %v", err)
		}
		e, err := b.gate().compileFEEL(feel)
		if err != nil {
			return fail("parameters did not compile (a value's =expression may be malformed): %v", err)
		}
		params = RestExpr{Expr: e}
	case !fromVar:
		// No parameters at all: right for a listing that takes none, and a deploy
		// error for anything that needs one.
		if err := op.CheckAuthored(name, nil); err != nil {
			return fail("%s", strings.TrimPrefix(err.Error(), "exchange: "))
		}
	}

	result := strings.TrimSpace(cn.ResultVariable)
	if op.NeedsResult && result == "" {
		return fail("operation %q needs a resultVariable (a read that discards its result is one nothing asked for)", name)
	}
	if op.Result == catalog.ResultNone && result != "" {
		return fail("operation %q returns nothing, so a resultVariable would name a value that is never written; remove it", name)
	}
	maxResults, err := exchangeMaxResults(st.Id, name, op, cn.MaxResults)
	if err != nil {
		return 0, err
	}
	return b.AddExchangeConnectorTask(ExchangeConfig{
		Worker:        strings.TrimSpace(cn.Connector),
		WorkerExpr:    workerExpr,
		Op:            name,
		Cmdlet:        cmdlet,
		Parameters:    params,
		ParametersVar: strings.TrimSpace(cn.ParametersVariable),
		MaxResults:    maxResults,
		ResultVar:     result,
		Retries:       retries,
	}), nil
}

// exchangeTemplate decodes the authored parameters: a JSON object, its numbers kept as
// numbers.
func exchangeTemplate(taskID, raw string) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("compiler: exchange task %q has an invalid parameters JSON: %w", taskID, err)
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("compiler: exchange task %q parameters must be a JSON object of cmdlet parameters, not a %T", taskID, v)
	}
	return obj, nil
}

// exchangeMaxResults reads the listing bound: the default when absent, the authored
// number otherwise, and an error on an operation that returns no list — a bound
// authored there would be silently ignored, which from the author's side is
// indistinguishable from a Worker that disregarded it.
func exchangeMaxResults(taskID, name string, op catalog.Op, raw string) (int32, error) {
	raw = strings.TrimSpace(raw)
	isList := op.Result == catalog.ResultList
	if raw == "" {
		if isList {
			return defaultExchangeMaxResults, nil
		}
		return 0, nil
	}
	if !isList {
		return 0, fmt.Errorf("compiler: exchange task %q sets maxResults on operation %q, which is not a listing", taskID, name)
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("compiler: exchange task %q has a non-numeric maxResults %q", taskID, raw)
	}
	if n < 1 {
		return 0, fmt.Errorf("compiler: exchange task %q has a maxResults of %d; it must be at least 1", taskID, n)
	}
	if n > int(maxExchangeMaxResults) {
		return 0, fmt.Errorf("compiler: exchange task %q has a maxResults of %d, above the %d a process variable is meant to hold", taskID, n, maxExchangeMaxResults)
	}
	return int32(n), nil
}

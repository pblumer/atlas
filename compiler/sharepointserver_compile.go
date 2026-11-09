package compiler

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pblumer/atlas/connector/sharepointserver/catalog"
)

// compileSharePointServerConnectorTask compiles an <atlas:sharepointServerConnector>
// task: one site administration step through a Worker on the farm
// (ADR-draft-sharepoint-server-worker). The table is the catalog's, read directly, so
// a deploy enforces the very rows the worker applies.
func compileSharePointServerConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.SharePointServer
	fail := func(format string, args ...any) (int32, error) {
		return 0, fmt.Errorf("compiler: sharepointserver task %q "+format, append([]any{st.Id}, args...)...)
	}
	worker := strings.TrimSpace(cn.Connector)
	if worker == "" {
		return fail("needs a connector attribute naming the SharePoint Server Worker")
	}
	if strings.HasPrefix(worker, "=") {
		return fail("names its Worker by a FEEL expression; a SharePoint Server Worker is one farm, so name it literally")
	}
	name := strings.ToLower(strings.TrimSpace(cn.Operation))
	if name == "" {
		return fail("needs an operation (%s)", strings.Join(catalog.Names(), ", "))
	}
	op, ok := catalog.Lookup(name)
	if !ok {
		return fail("has an unknown operation %q (want %s)", cn.Operation, strings.Join(catalog.Names(), ", "))
	}
	var template map[string]any
	if raw := strings.TrimSpace(cn.Parameters); raw != "" {
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&template); err != nil {
			return fail("has an invalid parameters JSON: %v", err)
		}
		if template == nil {
			return fail("parameters must be a JSON object")
		}
	}
	if err := op.CheckAuthored(name, template); err != nil {
		return fail("%s", strings.TrimPrefix(err.Error(), "sharepointserver: "))
	}
	var params RestExpr
	if template != nil {
		feel, err := jsonTemplateToFeel(template)
		if err != nil {
			return fail("parameters: %v", err)
		}
		e, err := b.gate().compileFEEL(feel)
		if err != nil {
			return fail("parameters did not compile (a value's =expression may be malformed): %v", err)
		}
		params = RestExpr{Expr: e}
	}
	result := strings.TrimSpace(cn.ResultVariable)
	if op.NeedsResult && result == "" {
		return fail("operation %q needs a resultVariable (a read that discards its result is one nothing asked for)", name)
	}
	if op.Result == catalog.ResultNone && result != "" {
		return fail("operation %q returns nothing, so a resultVariable would name a value that is never written; remove it", name)
	}
	return b.AddSharePointServerConnectorTask(SharePointServerConfig{
		Worker:     worker,
		Op:         name,
		Parameters: params,
		ResultVar:  result,
		Retries:    retries,
	}), nil
}

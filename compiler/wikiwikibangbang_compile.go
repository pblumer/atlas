package compiler

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/connector/wikiwikibangbang/catalog"
)

// compileWikiConnectorTask compiles an <atlas:wikiwikibangbangConnector> task: one
// operation on a wikiwikibangbang wiki through a Worker an operator configured
// (ADR-0498).
//
// The operation table is connector/wikiwikibangbang/catalog's, read here directly so the
// rules a deploy enforces are the very rows the worker runs. Every authored value is
// required where the operation needs it and refused where it does not use it, so a value
// is never silently ignored.
func compileWikiConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.Wiki
	fail := func(format string, args ...any) (int32, error) {
		return 0, fmt.Errorf("compiler: wikiwikibangbang task %q "+format, append([]any{st.Id}, args...)...)
	}
	if strings.TrimSpace(cn.Connector) == "" {
		return fail("needs a connector attribute naming the wikiwikibangbang Worker it writes through")
	}
	// The Worker is normally a static name, and may be FEEL so one process can reach
	// more than one wiki, choosing from its own variables at call time.
	workerExpr, err := connectorValue(b.gate(), st.Id, "wikiwikibangbang worker", "connector", cn.Connector)
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

	set := func(s string) bool { return strings.TrimSpace(s) != "" }
	inline, fromVar := set(cn.Properties), set(cn.PropertiesVariable)
	hasText, hasBlocks := set(cn.Text), set(cn.Blocks)
	if inline && fromVar {
		return fail("sets both inline properties and a propertiesVariable; use one, not both")
	}
	if hasText && hasBlocks {
		return fail("sets both text and blocks; a body is one or the other")
	}
	authored := map[string]bool{
		catalog.AttrItem: set(cn.Item), catalog.AttrSpace: set(cn.Space), catalog.AttrItemType: set(cn.ItemType),
		catalog.AttrParent: set(cn.Parent), catalog.AttrTitle: set(cn.Title), catalog.AttrProperties: inline || fromVar,
		catalog.AttrMatchField: set(cn.MatchField), catalog.AttrLabels: set(cn.Labels), catalog.AttrBody: hasText || hasBlocks,
		catalog.AttrTransition: set(cn.Transition), catalog.AttrComment: set(cn.Comment), catalog.AttrQuery: set(cn.Query),
	}
	describe := map[string]string{
		catalog.AttrItem:       "item (the item it acts on, by its id or key, e.g. =aufgabe.id)",
		catalog.AttrSpace:      "space (the space's key, e.g. OPS)",
		catalog.AttrItemType:   "itemType (the type's name, e.g. task or page)",
		catalog.AttrParent:     "parent (the item it is placed under)",
		catalog.AttrTitle:      "title",
		catalog.AttrProperties: "properties (inline or in propertiesVariable)",
		catalog.AttrMatchField: "matchField (the property that finds an existing item)",
		catalog.AttrLabels:     "labels",
		catalog.AttrBody:       "text or blocks (the body it writes)",
		catalog.AttrTransition: "transition (the transition's name, e.g. finish)",
		catalog.AttrComment:    "comment (the comment's text)",
		catalog.AttrQuery:      "query (in the wiki's query language)",
	}
	for _, attr := range catalog.Attributes {
		switch {
		case op.NeedsAttr(attr) && !authored[attr]:
			return fail("operation %q needs %s", name, describe[attr])
		case authored[attr] && !op.Uses(attr):
			return fail("operation %q does not use %s; remove it rather than leaving a value the Worker ignores", name, describe[attr])
		}
	}
	if op.NeedsOneTaken && !authored[catalog.AttrTitle] && !authored[catalog.AttrProperties] && !authored[catalog.AttrLabels] {
		return fail("operation %q changes nothing: it needs a title, properties or labels", name)
	}
	if authored[catalog.AttrMatchField] && !authored[catalog.AttrProperties] {
		return fail("sets matchField %q but no properties; the matchField's value comes from the properties the task sets", cn.MatchField)
	}
	switch rv := set(cn.ResultVariable); {
	case op.NeedsResult && !rv:
		return fail("operation %q needs a resultVariable (a read that discards its result is one nothing asked for)", name)
	case rv && !op.TakesResult():
		return fail("operation %q returns nothing, so a resultVariable would name a value that is never written; remove it", name)
	}

	// Values written out literally are checked here, where it is still the author reading
	// the message; values read from variables are checked by the worker.
	literal := func(s string) (string, bool) {
		s = strings.TrimSpace(s)
		return s, s != "" && !strings.HasPrefix(s, "=")
	}
	for _, ref := range []struct{ what, raw string }{{"item", cn.Item}, {"parent", cn.Parent}} {
		if v, ok := literal(ref.raw); ok {
			if err := catalog.CheckRef(ref.what, v); err != nil {
				return fail("%s", strings.TrimPrefix(err.Error(), "wikiwikibangbang: "))
			}
		}
	}
	matchField := ""
	if v, ok := literal(cn.MatchField); ok {
		if err := catalog.CheckFieldName(v); err != nil {
			return fail("matchField: %s", strings.TrimPrefix(err.Error(), "wikiwikibangbang: "))
		}
		matchField = v
	}
	var properties RestExpr
	if inline {
		template, err := wikiObject(st.Id, cn.Properties)
		if err != nil {
			return 0, err
		}
		if err := catalog.CheckProperties(template, matchField); err != nil {
			return fail("%s", strings.TrimPrefix(err.Error(), "wikiwikibangbang: "))
		}
		feel, err := jsonTemplateToFeel(template)
		if err != nil {
			return fail("properties: %v", err)
		}
		e, err := b.gate().compileFEEL(feel)
		if err != nil {
			return fail("properties did not compile (a value's =expression may be malformed): %v", err)
		}
		properties = RestExpr{Expr: e}
	}
	if v, ok := literal(cn.Blocks); ok {
		dec := json.NewDecoder(strings.NewReader(v))
		dec.UseNumber()
		var blocks any
		if err := dec.Decode(&blocks); err != nil {
			return fail("has an invalid blocks JSON: %v", err)
		}
		if err := catalog.CheckBlocks(blocks); err != nil {
			return fail("%s", strings.TrimPrefix(err.Error(), "wikiwikibangbang: "))
		}
	}
	maxResults, err := wikiMaxResults(st.Id, name, op, cn.MaxResults)
	if err != nil {
		return 0, err
	}

	cfg := WikiConfig{
		Worker:        strings.TrimSpace(cn.Connector),
		WorkerExpr:    workerExpr,
		Op:            name,
		Properties:    properties,
		PropertiesVar: strings.TrimSpace(cn.PropertiesVariable),
		MaxResults:    maxResults,
		ResultVar:     strings.TrimSpace(cn.ResultVariable),
		Retries:       retries,
	}
	for _, v := range []struct {
		what string
		raw  string
		into *RestExpr
	}{
		{"item", cn.Item, &cfg.Item},
		{"space", cn.Space, &cfg.Space},
		{"itemType", cn.ItemType, &cfg.ItemType},
		{"parent", cn.Parent, &cfg.Parent},
		{"title", cn.Title, &cfg.Title},
		{"matchField", cn.MatchField, &cfg.MatchField},
		{"labels", cn.Labels, &cfg.Labels},
		{"text", cn.Text, &cfg.Text},
		{"blocks", cn.Blocks, &cfg.Blocks},
		{"transition", cn.Transition, &cfg.Transition},
		{"comment", cn.Comment, &cfg.Comment},
		{"query", cn.Query, &cfg.Query},
	} {
		if strings.TrimSpace(v.raw) == "" {
			continue
		}
		val, err := connectorValue(b.gate(), st.Id, "wikiwikibangbang", v.what, strings.TrimSpace(v.raw))
		if err != nil {
			return 0, err
		}
		*v.into = val
	}
	return b.AddWikiConnectorTask(cfg), nil
}

// wikiObject decodes the authored properties: a JSON object, its numbers kept as
// numbers.
func wikiObject(taskID, raw string) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("compiler: wikiwikibangbang task %q has an invalid properties JSON: %w", taskID, err)
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("compiler: wikiwikibangbang task %q properties must be a JSON object of property values, not a %T", taskID, v)
	}
	return obj, nil
}

// wikiMaxResults reads the query bound: the operation's default when absent, the authored
// number otherwise, within the operation's own ceiling.
func wikiMaxResults(taskID, name string, op catalog.Op, raw string) (int32, error) {
	raw = strings.TrimSpace(raw)
	if op.MaxResults.Max == 0 {
		if raw != "" {
			return 0, fmt.Errorf("compiler: wikiwikibangbang task %q operation %q does not use maxResults; remove it rather than leaving a value the Worker ignores", taskID, name)
		}
		return 0, nil
	}
	if raw == "" {
		return op.MaxResults.Default, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("compiler: wikiwikibangbang task %q has a non-numeric maxResults %q", taskID, raw)
	}
	if n < 1 {
		return 0, fmt.Errorf("compiler: wikiwikibangbang task %q has a maxResults of %d; it must be at least 1", taskID, n)
	}
	if n > int(op.MaxResults.Max) {
		return 0, fmt.Errorf("compiler: wikiwikibangbang task %q has a maxResults of %d on %s, above its ceiling of %d", taskID, n, name, op.MaxResults.Max)
	}
	return int32(n), nil
}

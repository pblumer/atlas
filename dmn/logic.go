package dmn

import (
	"context"
	"errors"

	tdmn "github.com/pblumer/temis/dmn"
)

// ElementLogic is one DRG element's logic, read for the Modeler's decision peek
// (ADR-draft-decision-peek): what a decision or a business knowledge model computes, as the typed
// boxed-expression view temis itself serves its editors. Kind names the boxed
// expression — literal, table, context, list, relation, invocation, conditional,
// filter or iterator, the DMN 1.5 kinds temis has a view for — and View carries that
// kind's shape. An element whose logic temis has no view for (a boxed function, or
// none at all) comes back with an empty Kind, and the viewer says so rather than
// guessing at it.
type ElementLogic struct {
	ID string `json:"id"`
	// Element is the DRG element type: "decision" or "businessKnowledgeModel".
	Element string `json:"element"`
	Name    string `json:"name"`
	Kind    string `json:"kind,omitempty"`
	View    any    `json:"view,omitempty"`
	// Params are a business knowledge model's formal parameters: its logic is a
	// function, and what it is called with is half of reading it.
	Params []tdmn.BKMParam `json:"params,omitempty"`
}

// ModelLogic is a model's logic, element by element, with the same resolve/valid
// status [ModelGraph] carries, so the viewer can explain a model it cannot show.
// Elements is empty (never null) unless the model resolved and compiled cleanly.
type ModelLogic struct {
	Resolved  bool           `json:"resolved"`
	Valid     bool           `json:"valid"`
	ModelName string         `json:"modelName,omitempty"`
	Message   string         `json:"message,omitempty"`
	Elements  []ElementLogic `json:"elements"`
}

// boxedKinds are the boxed-expression kinds temis's LogicView answers for, in the
// order they are asked. A decision's logic is exactly one of them (or a literal
// expression, asked first), so the order decides nothing but how soon the answer
// is found.
var boxedKinds = []string{"table", "context", "list", "relation", "invocation", "conditional", "filter", "iterator"}

// Logic resolves modelRef, compiles it, and returns every decision's and business
// knowledge model's logic, for the read-only decision peek. Like [Validator.Graph]
// it returns a non-nil error only for an infrastructure failure; an unresolved
// handle or an invalid model is a normal result carrying a Message.
//
// The views are temis's own (its LogicView, LiteralExpression and BKMFunction),
// so the peek reads a model the way the engine that evaluates it reads it, with
// every DMN 1.5 boxed expression temis knows — not a second parse of the XML in
// the browser that would know only the kinds somebody taught it.
func (v *Validator) Logic(ctx context.Context, modelRef string) (ModelLogic, error) {
	empty := ModelLogic{Elements: []ElementLogic{}}
	xml, err := v.resolver.Resolve(ctx, modelRef)
	if errors.Is(err, ErrNotFound) {
		empty.Message = "no temis model matches this reference"
		return empty, nil
	}
	if err != nil {
		return ModelLogic{}, err
	}
	defs, diags, err := v.engine.Compile(ctx, xml)
	if err != nil {
		empty.Resolved, empty.Message = true, err.Error()
		return empty, nil
	}
	if diags.HasErrors() {
		empty.Resolved, empty.Message = true, formatDiagnostics(diags)
		return empty, nil
	}
	return logicOf(defs), nil
}

// logicOf reads each element's logic off a compiled model, in the graph's order.
// Input data has no logic and is left out: the graph already says what it is.
func logicOf(defs *tdmn.Definitions) ModelLogic {
	ml := ModelLogic{Resolved: true, Valid: true, ModelName: defs.ModelName(), Elements: []ElementLogic{}}
	for _, n := range defs.Graph().Nodes {
		el := ElementLogic{ID: n.ID, Element: n.Type, Name: n.Name}
		switch n.Type {
		case "decision":
			el.Kind, el.View = decisionLogic(defs, n.ID)
		case "businessKnowledgeModel":
			el.Kind, el.View, el.Params = bkmLogic(defs, n.ID)
		default:
			continue
		}
		ml.Elements = append(ml.Elements, el)
	}
	return ml
}

// decisionLogic finds a decision's logic kind and view: a literal expression, or
// the one boxed kind LogicView answers for.
func decisionLogic(defs *tdmn.Definitions, id string) (string, any) {
	if lv, ok := defs.LiteralExpression(id); ok {
		return "literal", lv
	}
	anchor := tdmn.Anchor{Kind: "decision", ID: id}
	for _, k := range boxedKinds {
		if view, ok := defs.LogicView(anchor, "", k); ok {
			return k, view
		}
	}
	return "", nil
}

// bkmLogic reads a business knowledge model's function: its parameters and its
// body — a literal body as a literal view, a boxed one as that kind's view. A body
// kind temis names but has no view for (a nested function) keeps its kind and no
// view, so the viewer can say what it is without drawing it.
func bkmLogic(defs *tdmn.Definitions, id string) (string, any, []tdmn.BKMParam) {
	bkm, ok := defs.BKMFunction(id)
	if !ok {
		return "", nil, nil
	}
	if bkm.BodyKind == "" {
		if bkm.BodyText == "" {
			return "", nil, bkm.Params
		}
		return "literal", tdmn.LiteralView{DecisionID: bkm.BkmID, Name: bkm.Name, Text: bkm.BodyText, TypeRef: bkm.BodyTypeRef}, bkm.Params
	}
	if view, ok := defs.LogicView(tdmn.Anchor{Kind: "bkm", ID: id}, "", bkm.BodyKind); ok {
		return bkm.BodyKind, view, bkm.Params
	}
	return bkm.BodyKind, nil, bkm.Params
}

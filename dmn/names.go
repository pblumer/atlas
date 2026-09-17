package dmn

import tdmn "github.com/pblumer/temis/dmn"

// A DMN decision carries two names, and temis now tells them apart.
//
//   - Its `name` attribute is the free-form label the diagram shows. Atlas has
//     always used it as the decision's identity: it is what the picker writes into
//     a business rule task's `decisionId`, what a decision deployment records, and
//     what the version counter is kept under.
//   - Its `<variable name>` is the FEEL identifier the result is bound to and
//     other expressions reference (DMN §7.3.1). When the model declares none it
//     falls back to the label, which is why the two coincide in most models.
//
// temis binds, keys and indexes by the second (`RefName`), and resolves a decision
// by either. Atlas therefore publishes the first and accepts both: an already
// deployed task that names the label keeps evaluating, and a model authored
// against the identifier resolves too (ADR-draft-a-decision-is-addressed-by-both-of-its-names).

// decisionNames splits the evaluable decisions of a compiled model into the name
// Atlas publishes for each — its label — and the additional names they answer to,
// which is the FEEL identifier wherever it differs from the label.
//
// Evaluable is decided by temis's own index, not by the graph: a decision whose
// logic is present but failed to compile appears in the graph with HasLogic set
// and is absent from the index, and Atlas must not offer, record or bundle a
// decision that cannot run. The graph supplies the label and the identifier that
// the index, which lists identifiers only, cannot.
//
// Both lists come back in model order, so every caller — the registry, the deploy
// gate, a listing — sees the same order for the same model.
func decisionNames(defs *tdmn.Definitions) (names, aliases []string) {
	evaluable := map[string]bool{}
	for _, ref := range defs.Index().Decisions {
		evaluable[ref] = true
	}
	for _, n := range defs.Graph().Nodes {
		if n.Type != "decision" {
			continue
		}
		ref := n.VarName // the graph already falls back to the label
		if ref == "" {
			ref = n.Name
		}
		if ref == "" || !evaluable[ref] {
			continue
		}
		name := n.Name
		if name == "" {
			// A decision with no label at all is still evaluable under its identifier,
			// so that is the only name it can be published under.
			name = ref
		}
		names = append(names, name)
		if ref != name {
			aliases = append(aliases, ref)
		}
	}
	return names, aliases
}

// addressableDecisions returns every name that addresses a decision in this model
// — the labels and the identifiers together. It is what the registry indexes, so
// that a business rule task resolves whichever of the two its `decisionId` carries.
//
// Resolution itself stays temis's: Atlas only answers "this model provides that
// decision" and then hands the name straight to [tdmn.Definitions.Decision]. So a
// name appearing twice in one model (one decision's label equal to another's
// identifier) cannot make Atlas evaluate a different decision than temis would.
func addressableDecisions(defs *tdmn.Definitions) []string {
	names, aliases := decisionNames(defs)
	if len(aliases) == 0 {
		return names
	}
	out := make([]string, 0, len(names)+len(aliases))
	out = append(out, names...)
	out = append(out, aliases...)
	return out
}

// aliasedInputs fills in the FEEL identifier of every input the model names
// differently from the identifier it binds, where the caller supplied the label
// instead — the same accommodation decisions get, on the input side.
//
// It exists for artifacts already on disk. A business rule task records its input
// keys at deploy time, filled from what Atlas offered then: the input's label,
// which was also the name temis required. temis now requires the identifier, so
// without this a deployed task would stop finding its input and fail on a
// MISSING_REQUIRED_INPUT it did nothing to earn.
//
// A supplied identifier always wins — nothing here overwrites a value the caller
// meant — and a model whose inputs declare no separate identifier (the common one)
// allocates nothing and returns the map it was given. It reads the DRG nodes the
// caller already holds, so an evaluation builds that graph once, not twice.
func aliasedInputs(nodes []tdmn.GraphNode, in map[string]any) map[string]any {
	if len(in) == 0 {
		return in
	}
	out, copied := in, false
	for _, n := range nodes {
		// The graph surfaces VarName on an input only when it differs from the label.
		if n.Type != "inputData" || n.VarName == "" || n.Name == "" {
			continue
		}
		if _, supplied := in[n.VarName]; supplied {
			continue
		}
		v, labelled := in[n.Name]
		if !labelled {
			continue
		}
		if !copied {
			out = make(map[string]any, len(in)+1)
			for k, v := range in {
				out[k] = v
			}
			copied = true
		}
		out[n.VarName] = v
	}
	return out
}

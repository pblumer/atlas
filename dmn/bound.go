package dmn

import (
	"errors"

	"github.com/pblumer/atlas/compiler"
)

// ErrNotADecisionTask says an element of a deployed definition is not a business rule
// task that evaluates a decision in this engine — it does not exist, is another kind
// of element, or calls a decision through a temis Worker (ADR-0050), which has no
// local model to name.
var ErrNotADecisionTask = errors.New("dmn: not a business rule task with a local decision")

// TaskModel is which model a deployed business rule task evaluates its decision from:
// the binding the definition was deployed with, and the registry key it resolves to
// now. Resolved is false for a definition deployed before deploy-time pinning
// (ADR-0063), whose latest-bound task resolves the newest model of either kind when
// it is activated; the registry's latest pointer answers for it, and Key is zero.
type TaskModel struct {
	DecisionID string
	Binding    string // "latest", "version" or "deployment"
	Version    int32  // the version a version-bound task names; 0 otherwise
	Key        uint64
	Resolved   bool
}

// TaskModelOf answers, for one business rule task of a deployed definition, the
// question the worker answers when the task's job is worked: which deployment's model
// does it evaluate (ADR-0423)? It is decisionModelKey — the worker's own function —
// so the Modeler's decision peek and the engine cannot disagree about it
// (ADR-draft-decision-peek).
//
// It reads the definition's pins and the registry's version index. The compiled
// process belongs to the run loop, so the caller holds it there; the registry
// guards its own indexes.
func TaskModelOf(reg *Registry, cp *compiler.CompiledProcess, elementID string) (TaskModel, error) {
	if cp == nil {
		return TaskModel{}, ErrNotADecisionTask
	}
	idx, ok := cp.ElementIndexOf(elementID)
	if !ok || int(idx) >= cp.NodeCount() {
		return TaskModel{}, ErrNotADecisionTask
	}
	node := cp.Node(idx)
	if node.Type != compiler.TypeBusinessRuleTask || node.Detail < 0 {
		return TaskModel{}, ErrNotADecisionTask
	}
	detail := cp.BusinessRuleTask(node.Detail)
	decisionID := cp.Intern(detail.DecisionId)
	if detail.Connector >= 0 || decisionID == "" {
		return TaskModel{}, ErrNotADecisionTask
	}
	tm := TaskModel{DecisionID: decisionID, Binding: bindingName(detail.Binding), Version: detail.Version}
	key, resolved, err := decisionModelKey(reg, cp, detail, decisionID)
	if err != nil {
		return tm, err
	}
	tm.Key, tm.Resolved = key, resolved
	return tm, nil
}

// bindingName is a compiled binding as the Modeler spells it. Camunda's versionTag,
// which Atlas refuses at deploy (ADR-0423), reads as the latest it still evaluates on
// a definition deployed with it.
func bindingName(b compiler.DecisionBinding) string {
	switch b {
	case compiler.BindingDeployment:
		return "deployment"
	case compiler.BindingVersion:
		return "version"
	default:
		return "latest"
	}
}

// Logic returns the logic of the model the deployment under defKey evaluates
// decisionId from, element by element — [Registry.Graph]'s counterpart, with the
// same fallback to the newest model providing the decision for key 0 or a key that
// is gone. It reads the compiled model, which is immutable, so nothing is compiled
// here. The second return is false when nothing on this server provides the
// decision.
func (r *Registry) Logic(defKey uint64, decisionId string) (ModelLogic, bool) {
	r.mu.RLock()
	var reg *registered
	if found := regProviding(r.definitions[defKey], decisionId); found != nil {
		reg = found
	} else if latest, ok := r.latest[decisionId]; ok {
		reg = &latest
	}
	r.mu.RUnlock()
	if reg == nil {
		return ModelLogic{Elements: []ElementLogic{}}, false
	}
	return logicOf(reg.defs), true
}

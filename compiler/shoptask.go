package compiler

import (
	"fmt"
	"regexp"
	"strings"
)

// The shop send task (ADR-0429 §4).
//
// A product's process meets the shop at two kinds of point: where it takes a
// command (a receive task or catch on an action's message, which needs nothing new)
// and where it states how the command ended. The second has behaviour no existing
// element has, so it is a send-task kind of its own, declared on the element and
// compiled at deploy (I5) to a reserved job type the server serves itself. It holds
// no credential and builds no URL: the order, the position and the command are read
// from the instance's scope at runtime, where every act seeds them.

// ShopModeOutcome states how the command this instance carries ended.
const ShopModeOutcome = "outcome"

// shopActionKey is the shape of an action key (ADR-0305's rules), repeated here
// because the compiler does not import the catalogue.
var shopActionKey = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// shopOutcomes is the closed outcome vocabulary of ADR-0429 §3.
var shopOutcomes = map[string]bool{"completed": true, "rejected": true, "failed": true}

// ShopConfig is the deploy-time configuration of a shop send task.
type ShopConfig struct {
	Mode    string
	Action  string
	Outcome string
	Retries int32
}

// AddShopTask adds a shop send task: a connector-task node carrying the reserved
// shop job type, served in-process.
func (b *Builder) AddShopTask(cfg ShopConfig) int32 {
	detail := int32(len(b.connectorTasks))
	b.connectorTasks = append(b.connectorTasks, ConnectorTaskDetail{
		JobType:     b.intern(ShopJobType),
		Connector:   -1, // no server-registered provider; it changes state the loop owns
		Subject:     -1,
		EventType:   -1,
		ClioQuery:   -1,
		ReduceSpec:  -1,
		Method:      -1,
		ResultVar:   -1,
		Auth:        -1,
		ShopMode:    cfg.Mode,
		ShopAction:  cfg.Action,
		ShopOutcome: cfg.Outcome,
		Retries:     cfg.Retries,
	})
	return b.addNode(TypeConnectorTask, detail)
}

// compileShopTask checks a shop send task's declaration and adds it. Everything is a
// literal checked here, so a typo in an action key or an outcome is a deploy error
// rather than a command that never ends.
func compileShopTask(b *Builder, id string, st *xmlShopTask) (int32, error) {
	mode := strings.TrimSpace(st.Mode)
	if mode == "" {
		mode = ShopModeOutcome
	}
	if mode != ShopModeOutcome {
		return 0, fmt.Errorf("compiler: shop task %q has mode %q; the mode is %q", id, mode, ShopModeOutcome)
	}
	action := strings.TrimSpace(st.Action)
	if !shopActionKey.MatchString(action) {
		return 0, fmt.Errorf("compiler: shop task %q names action %q; an action key is lower-case "+
			"letters, digits and dashes, 1 to 64 characters", id, action)
	}
	outcome := strings.TrimSpace(st.Outcome)
	if !shopOutcomes[outcome] {
		return 0, fmt.Errorf("compiler: shop task %q states outcome %q; it is one of completed, rejected, failed", id, outcome)
	}
	retries, err := parseRetries("send task", id, st.Retries)
	if err != nil {
		return 0, err
	}
	return b.AddShopTask(ShopConfig{Mode: mode, Action: action, Outcome: outcome, Retries: retries}), nil
}

// ShopOutcomePoint is one shop send task that states an outcome.
type ShopOutcomePoint struct {
	// Element is the BPMN element id.
	Element string
	Action  string
	Outcome string
}

// ShopOutcomePoints lists every shop send task of the process that states an
// outcome, in element order. Read at publish, to know that every action a product
// declares is answered (ADR-0429 §4).
func (p *CompiledProcess) ShopOutcomePoints() []ShopOutcomePoint {
	var out []ShopOutcomePoint
	for id := range p.nodes {
		n := &p.nodes[id]
		if n.Type != TypeConnectorTask {
			continue
		}
		d := p.ConnectorTask(n.Detail)
		if d.JobType != ShopJobTypeIndex || d.ShopMode != ShopModeOutcome {
			continue
		}
		out = append(out, ShopOutcomePoint{Element: p.ElementBpmnId(int32(id)), Action: d.ShopAction, Outcome: d.ShopOutcome})
	}
	return out
}

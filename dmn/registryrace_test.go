package dmn_test

import (
	"context"
	"sync"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

const raceModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="d" name="R" namespace="http://atlas/test/race">
  <decision id="dec" name="Pick"><variable name="Pick" typeRef="string"/>
    <literalExpression><text>"a"</text></literalExpression></decision>
</definitions>`

// A business rule task is evaluated by a job handler off the run loop while the
// loop may be registering a decision deployment. The registry used to assume it
// was populated before anything read it, and the race detector reported a data
// race on its indexes; a Go map read during a write can also end the process. This
// runs the two sides together — every read the worker and the surfaces make, against
// every kind of write — and is meant to be run under -race, as the Definition of
// Done does.
func TestTheRegistryIsReadWhileADecisionIsDeployed(t *testing.T) {
	reg := dmn.NewRegistry()
	if err := reg.DeployDecision(1, []byte(raceModel)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if _, _, err := reg.EvaluateTraced(context.Background(), 1, "Pick", nil); err != nil {
				t.Errorf("EvaluateTraced: %v", err)
				return
			}
			_, _, _ = reg.EvaluateLatestTraced(context.Background(), "Pick", nil)
			reg.LatestDecisionKey("Pick")
			reg.LatestDecisionIDs()
			reg.Graph(1, "Pick")
			reg.IsService(1, "Pick")
			reg.DeployedDecisions()
		}
	}()
	go func() {
		defer wg.Done()
		for k := uint64(2); k < 40; k++ {
			if err := reg.DeployDecision(k, []byte(raceModel)); err != nil {
				t.Errorf("DeployDecision: %v", err)
				return
			}
			if err := reg.Deploy(1000+k, []byte(raceModel)); err != nil {
				t.Errorf("Deploy: %v", err)
				return
			}
			if k%3 == 0 {
				reg.UndeployDecision(k)
			}
		}
	}()
	wg.Wait()
}

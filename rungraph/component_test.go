package rungraph

import (
	"fmt"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// W2's acceptance criterion, in the shape W1's was: the claim ADR-0404 §5 makes, measured on
// state the engine actually wrote rather than on keys a test invented — and, like W1's, it
// found the claim conditional in a way the record does not say.
//
// The claim is §5's first sentence: *"One pass over the CSR computes connected components by
// union-find, which for this topology **is** the instance-family decomposition."* Everything
// W2 is for depends on it, because if a component is not an instance then "which component
// does this belong to" is not the question anybody thought they were asking.
//
// **What the two shapes below measure is that it depends on the model, not on the builder.** A
// nested shape gives one component per instance. A flat one with two parallel top-level
// branches gives two, because the only parent those branches share is the process instance —
// and a process instance is not an element instance, so it is not a node of this graph and
// nothing joins them. Sixty-four instances, a hundred and twenty-eight components.
//
// So a component is the **reference-connected** group: what a walk from one node reaches. That
// is the right grouping for impact analysis and it is not the instance, which needs no
// union-find at all — ProcessInstanceKey is a field on the value the store already hands over.
// The API is named after what it answers for exactly this reason.
//
// It has to be measured on engine-written state because the claim is about what the engine
// leaves behind: which element instances exist at once, and which of them carry a reference to
// another that is itself an element instance. A fixture asserts that by construction.

// forkingWorkload parks two element instances on *parallel top-level branches* of one
// instance. It exists to ask the question the nested workload cannot: two siblings whose only
// common parent is the process instance itself.
//
// Start → ParallelGateway → { ServiceTask a, ServiceTask b }. Both park, and both carry
// FlowScopeKey pointing at the process instance key — which is not an element instance and
// therefore not a node of this graph (eachEdge skips a reference to a non-node, and
// TestAReferenceToSomethingThatIsNotANodeIsNotAnEdge pins that). So there is no edge between
// them, and the question is what that does to §5's claim.
func forkingWorkload(t *testing.T) *compiler.CompiledProcess {
	t.Helper()
	bld := compiler.NewBuilder(2, "rungraph-fork", 1)
	start := bld.AddStartEvent()
	fork := bld.AddParallelGateway()
	bld.Connect(start, fork)
	a := bld.AddServiceTask("rungraph-a", 3)
	b := bld.AddServiceTask("rungraph-b", 3)
	bld.Connect(fork, a)
	bld.Connect(fork, b)
	cp, err := bld.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// instanceOfNode reads each node's owning process instance out of the same view the graph was
// built from, which is what the component is compared against.
func instanceOfNode(t *testing.T, view *state.ReadView) map[uint64]uint64 {
	t.Helper()
	owner := map[uint64]uint64{}
	if err := view.ActiveElementInstances(func(key uint64, v *model.ElementInstanceValue) error {
		owner[key] = v.ProcessInstanceKey
		return nil
	}); err != nil {
		t.Fatalf("ActiveElementInstances: %v", err)
	}
	return owner
}

// TestAComponentIsReferenceConnectedNotAnInstance is the criterion. It runs both shapes
// because they answer differently: a component coincides with an instance exactly as far as
// the instance's live element instances are joined by a reference one of them holds to
// another, and the flat shape is where that stops being true.
func TestAComponentIsReferenceConnectedNotAnInstance(t *testing.T) {
	for _, tc := range []struct {
		name     string
		workload func(*testing.T) *compiler.CompiledProcess
		// perInstance is how many components one instance is expected to decompose into.
		perInstance int
		why         string
	}{
		{
			name:        "nested scope and boundary",
			workload:    parkingWorkload,
			perInstance: 1,
			why:         "the inner elements point at the subprocess instance and the boundary event is attached to it, so every live node of the instance is joined",
		},
		{
			name:        "parallel top-level branches",
			workload:    forkingWorkload,
			perInstance: 2,
			why:         "two siblings whose only common parent is the process instance, which is not an element instance and therefore not a node: nothing joins them",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, store := engineFixture(t)
			cp := tc.workload(t)
			p.Deploy(cp)
			if err := p.Recover(); err != nil {
				t.Fatalf("Recover: %v", err)
			}
			const instances = 64
			for range instances {
				p.CreateInstance(cp.Key)
			}
			if err := p.RunUntilIdle(); err != nil {
				t.Fatalf("RunUntilIdle: %v", err)
			}

			view := store.ReadView()
			defer view.Close()
			g, err := Build(view)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			m := g.Membership()
			owner := instanceOfNode(t, view)
			if m.Len() == 0 || len(owner) != m.Len() {
				t.Fatalf("graph has %d nodes, the view has %d element instances", m.Len(), len(owner))
			}

			// Which components each instance's nodes fall into, and which instances each
			// component holds. The second map is the one that would expose the failure that
			// matters: a component spanning two instances makes the lookup relate things that
			// are unrelated.
			componentsOf := map[uint64]map[uint64]bool{}
			instancesInComponent := map[uint64]map[uint64]bool{}
			for key, instance := range owner {
				component, ok := m.Of(key)
				if !ok {
					t.Fatalf("node %d is in the view but not in the membership", key)
				}
				if componentsOf[instance] == nil {
					componentsOf[instance] = map[uint64]bool{}
				}
				componentsOf[instance][component] = true
				if instancesInComponent[component] == nil {
					instancesInComponent[component] = map[uint64]bool{}
				}
				instancesInComponent[component][instance] = true
			}

			t.Logf("%d instances → %d nodes, %d edges, %d components (%.2f components per instance)",
				instances, m.Len(), g.Edges, m.Count(),
				float64(m.Count())/float64(len(componentsOf)))

			// No component may span two instances. This is the direction that has to hold for
			// the lookup to mean anything at all: a false "same component" is an impact
			// analysis reporting an unrelated instance as affected.
			for component, in := range instancesInComponent {
				if len(in) != 1 {
					t.Errorf("component %d spans %d instances; SameComponent would relate unrelated instances", component, len(in))
				}
			}
			// And each instance decomposes into the number of components its shape implies.
			for instance, comps := range componentsOf {
				if len(comps) != tc.perInstance {
					t.Errorf("instance %d falls into %d components, want %d — %s", instance, len(comps), tc.perInstance, tc.why)
				}
			}
			if got, want := m.Count(), len(componentsOf)*tc.perInstance; got != want {
				t.Errorf("Count() = %d, want %d", got, want)
			}

			// The two lookups, against real keys: every pair inside one component is related,
			// and a pair from different instances is not.
			var a, b uint64
			for key, instance := range owner {
				if a == 0 {
					a, _ = key, instance
					continue
				}
				if owner[a] != instance {
					b = key
					break
				}
			}
			if b == 0 {
				t.Fatal("the workload produced only one instance's worth of nodes")
			}
			if m.SameComponent(a, b) {
				t.Errorf("SameComponent(%d, %d) across two instances is true", a, b)
			}
			// Enumeration agrees with the membership it was derived from.
			for _, member := range m.Members(a) {
				if !m.SameComponent(a, member) {
					t.Errorf("Members(%d) returned %d, which SameComponent says is not in it", a, member)
				}
			}
			if got, want := len(m.Members(a)), m.Size(a); got != want {
				t.Errorf("Members(%d) has %d members but Size says %d", a, got, want)
			}
		})
	}
	_ = fmt.Sprint
}

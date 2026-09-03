package api

import (
	"bytes"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
)

// The claim on a message name (ADR-0205, measure M11 step two).
//
// Step one gave a worker an owner and stopped there. It made a stranger unable
// to *configure* somebody's inbound worker, and left them able to deploy a
// process whose message start event named the same message — because
// `Processor.PublishInbound` carries a message name and a correlation key and
// nothing else. The name was the whole authorization, and a name is not a secret:
// it was readable from the subscription listing until step one, and guessable
// afterwards.
//
// So an inbound subscription **claims** its message name, and the claim is checked
// at the two design-time doors where a definition and a subscription meet:
//
//   - **Deploying** is refused when the definition can be delivered a name that a
//     subscription claims and the deployer cannot reach.
//   - **Claiming** is refused when a definition the claimant cannot reach can
//     already be delivered that name.
//
// Both, because a check at one moment is not a rule: with only the first, deploying
// before anybody subscribes would be all it takes; with only the second, subscribing
// first would be.
//
// # The two doors ask different questions, and that is not an oversight
//
// A worker has a sharing scope, so "can this person reach that subscription" is
// the scope question step one already answers. A *deployment* has none — ADR-0071
// put runtime visibility out of scope and this record does not overturn that — so
// "can this person reach that definition" is answered by the one fact a deployment
// now records, its deployer, run through the same rule every other ungrouped
// artifact uses (`canViewArtifact`). A definition filed into a project defers to
// that project's scope, as its drafts do.
//
// # What it is not
//
// A gate at two doors, not isolation. Its limits, all of them stated so nobody
// reads it as more:
//
//   - An administrator passes both doors, because an administrator reaches
//     everything. That is what admin means here, as it does in ADR-0071.
//   - A definition deployed before this carries no deployer and no project. It
//     reads as ownerless, which is open — so a claim on a name it already catches
//     is allowed, and the pre-emption it could have performed stands until it is
//     redeployed. Refusing instead would break every upgrade where somebody claims
//     a name their own long-standing process listens for.
//   - It governs *delivery to a definition*, never a running instance. Nothing here
//     is consulted while a message correlates; the engine still matches on name and
//     key alone. The real isolation — the published message carrying the scope it
//     was published into — changes the message value and touches applyToState, and
//     is its own decision.

// messageClaim is one subscription's hold on a message name, carrying only what a
// refusal needs: which worker it belongs to, so the caller's reach can be
// checked, and nothing that would identify it in an error.
type messageClaim struct {
	messageName string
	connectorID string
}

// claimsByMessage indexes every enabled subscription by the message name it
// publishes under. A disabled subscription publishes nothing, so it claims
// nothing — otherwise turning one off would still hold a name hostage.
//
// Must be called on the run-loop goroutine: it reads the subscription store.
func (s *Server) claimsByMessage() (map[string][]messageClaim, error) {
	subs, err := s.inboundSubs.LoadAll()
	if err != nil {
		return nil, err
	}
	out := map[string][]messageClaim{}
	for _, sub := range subs {
		if !sub.Enabled || sub.MessageName == "" {
			continue
		}
		out[sub.MessageName] = append(out[sub.MessageName],
			messageClaim{messageName: sub.MessageName, connectorID: sub.ConnectorID})
	}
	return out, nil
}

// claimBlockingDeploy reports the first message name this definition could be
// delivered, that a subscription claims, and that the deployer cannot reach —
// "" when nothing is in the way.
//
// Must be called on the run-loop goroutine.
func (s *Server) claimBlockingDeploy(r *http.Request, names []string) (string, error) {
	if !s.authEnabled || len(names) == 0 {
		return "", nil
	}
	claims, err := s.claimsByMessage()
	if err != nil {
		return "", err
	}
	if len(claims) == 0 {
		return "", nil
	}
	for _, name := range names {
		for _, claim := range claims[name] {
			conn, found, err := s.connectors.Get(claim.connectorID)
			if err != nil {
				return "", err
			}
			// A claim whose worker is gone holds nothing: the subscription is an
			// orphan the bridge already ignores.
			if !found {
				continue
			}
			if code, _ := s.checkConnectorRole(r, conn, ScopeRoleViewer); code != 0 {
				return name, nil
			}
		}
	}
	return "", nil
}

// definitionBlockingClaim reports whether some deployed definition the claimant
// cannot reach can already be delivered name.
//
// This is the door that protects the claimant from themselves: pointing a mailbox
// at a name somebody else's process listens for would deliver their post to that
// process, and the claimant would have no way to know.
//
// Must be called on the run-loop goroutine: it reads the deployment registry and
// the project store.
func (s *Server) definitionBlockingClaim(r *http.Request, name string) (bool, error) {
	if !s.authEnabled || name == "" {
		return false, nil
	}
	var projs map[string]project
	for _, d := range s.deployments {
		if d == nil || d.cp == nil {
			continue
		}
		if !receivesMessage(d.cp.ReceivableMessageNames(), name) {
			continue
		}
		// Loaded lazily and once: most deploys catch no claimed name at all, and this
		// is the only branch that needs the projects.
		if projs == nil {
			var err error
			if projs, err = s.projectsByID(); err != nil {
				return false, err
			}
		}
		if !s.canViewArtifact(r, d.ProjectID, d.DeployedBy, projs) {
			return true, nil
		}
	}
	return false, nil
}

func receivesMessage(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// claimRefusal is the body both doors answer with: 409, naming the message and
// nothing else.
//
// Naming the message is what makes it actionable — the person can rename their
// event or ask around. Naming the other party is what it must not do: the point is
// to stop a delivery, not to disclose that somebody has a mailbox, and an error
// that says "anna's posteingang claims this" hands over exactly what the private
// worker was hiding.
func claimRefusal(w http.ResponseWriter, message, detail string) {
	httpapi.JSON(w, http.StatusConflict, map[string]any{
		"error":       "the message name is claimed elsewhere",
		"messageName": message,
		"details":     detail,
	})
}

// claimBlockingModel parses a model and asks the deploy door about it: the first
// claimed message name it could be delivered and the caller cannot reach, or "".
//
// It parses the model a second time — deployModel parses it again to register it —
// and that is the right trade. A deploy is rare and already does far more work than
// a parse, while threading a third failure kind through deployModel and its four
// callers would put this check in the path of the server's own startup deploys,
// which have no caller to refuse.
//
// Must be called on the run-loop goroutine.
func (s *Server) claimBlockingModel(r *http.Request, body []byte) (string, error) {
	if !s.authEnabled {
		return "", nil
	}
	deployables, err := compiler.ParseAll(s.nextKey, 1, bytes.NewReader(body))
	if err != nil {
		// Not this check's business: an unparseable model is refused by the deploy
		// itself, with the compiler's own message.
		return "", nil
	}
	var names []string
	for i := range deployables {
		names = append(names, deployables[i].Process.ReceivableMessageNames()...)
	}
	return s.claimBlockingDeploy(r, names)
}

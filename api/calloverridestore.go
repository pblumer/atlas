package api

import (
	"github.com/pblumer/atlas/api/sidecar"
)

// Call-activity target override actions (ADR-0105). Exactly one applies per record:
//   - redirect: resolve the latest of TargetProcessID instead of the called id;
//   - pin:      resolve exactly TargetVersion of the called id;
//   - disable:  resolve nothing — the call parks (as an undeployed callee does).
const (
	overrideRedirect = "redirect"
	overridePin      = "pin"
	overrideDisable  = "disable"
)

// callOverride is one operator-configured, per-server call-activity target override
// (ADR-0105), keyed by the called bpmn process id. It stores the operator's *intent*
// — never a raw definition key — so it survives redeploys sensibly: a pin re-resolves
// its version to a key at load time. It is admin config, the same category as a
// connector record (ADR-0041): durable on a sidecar, owned by the run-loop goroutine,
// never in the event log.
type callOverride struct {
	CalledProcessID string `json:"calledProcessId"`
	Action          string `json:"action"`
	TargetProcessID string `json:"targetProcessId,omitempty"` // redirect
	TargetVersion   int32  `json:"targetVersion,omitempty"`   // pin
	UpdatedAt       int64  `json:"updatedAt"`
}

// callOverrideStore is a durable store for per-server call-activity overrides, one
// JSON file per called process id under a single directory — the same on-disk sidecar
// approach as the connector/deployment stores (ADR-0019/0041). Owned solely by the
// server's run-loop goroutine, so it needs no locking, and it holds no secrets.

// callOverrideStore is a durable store for callOverride records, one JSON file per id
// under a single directory (ADR-0019). Like every design-time store it is owned
// solely by the server's run-loop goroutine, so it needs no locking of its own.
type callOverrideStore = sidecar.Store[callOverride]

// newCallOverrideStore opens (creating if needed) the calloverride directory.
func newCallOverrideStore(dir string) (*callOverrideStore, error) {
	return sidecar.NewStore(dir, "calloverridestore",
		func(rec callOverride) string { return rec.CalledProcessID },
		sidecar.Order(func(a, b callOverride) bool {
			if a.UpdatedAt != b.UpdatedAt {
				return a.UpdatedAt < b.UpdatedAt
			}
			return a.CalledProcessID < b.CalledProcessID
		}),
	)
}

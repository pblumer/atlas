package entra

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// communityPollInterval and communityFollowBound bound how create-community follows the
// operation Graph accepted: how long it waits between two looks at the operation, and
// how long it looks at all within one job. They are variables so a test can shrink them.
//
// Ninety seconds is well inside a job's lease, and Microsoft gives no figure for how
// long a community takes to be provisioned; the bound is a choice, not a documented
// number. What happens when it is reached is the important part — see createCommunity.
var (
	communityPollInterval = 3 * time.Second
	communityFollowBound  = 90 * time.Second
)

// The statuses Graph documents for an engagementAsyncOperation. notStarted and running
// are the two that are not yet an answer.
const (
	asyncSucceeded = "succeeded"
	asyncFailed    = "failed"
)

// withOwner copies the community attributes and binds the authored user as its first
// owner — owners@odata.bind, an absolute URL under the worker's own Graph root — unless
// the attributes already carry owners@odata.bind, which is then the model's and sent as
// authored. The authored map is not modified, as withPrincipal and withRedirect leave
// theirs alone.
//
// Microsoft documents that a community created with application permissions — which is
// what this worker holds — must name an owner, so a community body without one would be
// a refusal at Graph. That is Microsoft's documentation, not something verified against
// a tenant here.
func withOwner(attrs map[string]any, base, user string) map[string]any {
	out := make(map[string]any, len(attrs)+1)
	for k, v := range attrs {
		out[k] = v
	}
	if _, ok := out["owners@odata.bind"]; !ok {
		out["owners@odata.bind"] = []any{base + "/users/" + user}
	}
	return out
}

// createCommunity creates a Viva Engage community and answers the community.
//
// Graph does not answer the POST with the community. Its documentation (v1.0) says it
// answers 202 Accepted with an empty body and a Location naming an
// engagementAsyncOperation — {id, operationType, status, resourceId, resourceLocation,
// statusDetail}, status one of notStarted, running, succeeded, failed — and the
// community exists once that operation has succeeded, its id being the operation's
// resourceId. So the job follows the operation itself, bounded by
// [communityPollInterval] and [communityFollowBound], and reads the community it made.
//
// Delivery is at least once, as for every create this worker performs: a job that fails
// is retried, and a retry POSTs again. That is why the outcomes are not all failures:
//
//   - succeeded: the community is read by its id and returned (id, displayName,
//     description, privacy, groupId — the group its members are managed on).
//   - failed: an error naming Graph's statusDetail. Microsoft describes a failed
//     operation as having created nothing, so the retry that follows is a second try
//     rather than a duplicate; that is Microsoft's description, unverified here.
//   - still notStarted or running when the bound is reached: the job completes, with
//     {"id": null, "status": <status>, "operation": <location>}. Failing it would let the
//     retry POST again while the first community may still be on its way — a duplicate
//     community nobody asked for. A model routes `= community.id = null` to a person,
//     who can look the operation up.
//
// The same reasoning keeps the job completing when Graph accepted the community but
// the answer cannot be read to the end: a 202 without a Location completes with a null
// id and status "unknown", and a community that succeeded but cannot be read back
// before the bound completes with its id and no more. A transient answer while
// following — 429 or a 5xx — is waited out like a running operation.
//
// A Location on another host is the exception, and fails the job: the client refuses
// to follow it (it would carry the bearer there), and an answer naming a foreign host
// is not one Graph gives, so the premise that Graph accepted anything is itself in
// doubt. The failure is the visible outcome an operator has to look at; its retry may
// POST again, which is the cost of not following a URL a response chose.
func createCommunity(ctx context.Context, j Job, spec Op, client Client) (any, error) {
	res, err := client.Call(ctx, request(j, spec, client.BaseURL()))
	if err != nil {
		return nil, err
	}
	switch v := res.(type) {
	case Accepted:
		if strings.TrimSpace(v.Location) == "" {
			return pendingCommunity("unknown", nil), nil
		}
		return followCommunity(ctx, j.Operation, client, v.Location)
	case map[string]any:
		// Graph answered with a body after all — the community itself. Taken as the
		// answer rather than refused: it is what the operation exists to return.
		return v, nil
	case nil:
		return pendingCommunity("unknown", nil), nil
	default:
		return nil, fmt.Errorf("entra: %s: Graph answered a %T, neither an accepted operation nor a community", j.Operation, res)
	}
}

// pendingCommunity is the answer of a create-community whose community is not known
// yet: a null id a model can route on, the operation's last status, and where it is.
func pendingCommunity(status string, operation any) map[string]any {
	return map[string]any{"id": nil, "status": status, "operation": operation}
}

// followCommunity polls the accepted operation at loc until it has an answer or the
// bound is reached, then reads the community it created. See createCommunity for what
// each outcome returns and why.
func followCommunity(ctx context.Context, op string, client Client, loc string) (any, error) {
	deadline := time.Now().Add(communityFollowBound)
	status := "notStarted"
	resourceID := ""
	for {
		if err := sleepCtx(ctx, communityPollInterval); err != nil {
			return nil, fmt.Errorf("entra: %s: stopped following the accepted operation: %w", op, err)
		}
		if resourceID == "" {
			res, err := client.Call(ctx, Request{Method: http.MethodGet, Path: loc})
			switch {
			case err != nil && transient(err):
				// Waited out like a running operation.
			case err != nil:
				return nil, fmt.Errorf("entra: %s: following the accepted operation: %w", op, err)
			default:
				opObj, ok := res.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("entra: %s: the accepted operation answered a %T, not an engagementAsyncOperation", op, res)
				}
				if s, _ := opObj["status"].(string); s != "" {
					status = s
				}
				switch status {
				case asyncFailed:
					detail, _ := opObj["statusDetail"].(string)
					if strings.TrimSpace(detail) == "" {
						detail = "no statusDetail given"
					}
					return nil, fmt.Errorf("entra: %s: Graph reports the community creation failed: %s (Microsoft describes a failed operation as having created nothing, so a retry creates it afresh)", op, detail)
				case asyncSucceeded:
					id, _ := opObj["resourceId"].(string)
					if strings.TrimSpace(id) == "" {
						// Succeeded, but without saying what: the community exists and
						// cannot be named. Completing keeps a retry from making a second.
						return pendingCommunity(status, loc), nil
					}
					resourceID = strings.TrimSpace(id)
				}
			}
		}
		if resourceID != "" {
			res, err := client.Call(ctx, Request{Method: http.MethodGet, Path: "/employeeExperience/communities/" + url.PathEscape(resourceID)})
			switch {
			case err == nil:
				if c, ok := res.(map[string]any); ok {
					return c, nil
				}
				return nil, fmt.Errorf("entra: %s: the created community answered a %T, not an object", op, res)
			case !errors.As(err, new(*GraphError)):
				return nil, fmt.Errorf("entra: %s: reading the created community: %w", op, err)
			}
			// A Graph answer other than the community — a 404 while the new community
			// is still replicating, say — is waited out up to the bound.
		}
		if !time.Now().Before(deadline) {
			if resourceID != "" {
				return map[string]any{"id": resourceID, "status": asyncSucceeded, "operation": loc}, nil
			}
			return pendingCommunity(status, loc), nil
		}
	}
}

// transient reports whether a Graph answer while following an operation is one to wait
// out rather than fail on: throttling, or the service being briefly unavailable.
func transient(err error) bool {
	var ge *GraphError
	return errors.As(err, &ge) && (ge.Status == http.StatusTooManyRequests || ge.Status >= 500)
}

// sleepCtx waits d, or until ctx is done, whichever is first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

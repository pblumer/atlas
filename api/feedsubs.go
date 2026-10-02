package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// The routes that manage push delivery of the event feed
// (ADR-draft-the-event-feed-is-pushed-to-a-cloudevents-endpoint). Admin-only: a
// subscription sends the map of who holds what to a system beyond Atlas, which is what
// minting an events token does (ADR-0430), and minting is an administrator's act.

// feedSubView is a subscription as the listing answers it: the record, the Worker that
// delivers it by name, and its hold when delivery is failing.
type feedSubView struct {
	feedSubscription
	WorkerName string        `json:"workerName,omitempty"`
	Hold       *feedPushHold `json:"hold,omitempty"`
}

// feedSubRequest is the body of a create or an update. On an update every field is
// optional and only those present change.
type feedSubRequest struct {
	WorkerID  *string   `json:"workerId"`
	Reach     *[]string `json:"reach"`
	BatchSize *int      `json:"batchSize"`
	Enabled   *bool     `json:"enabled"`
	// From places the cursor: "oldest" at the oldest row the feed still holds, "now"
	// after the newest. On a create it defaults to "oldest", as the pull feed's first
	// page does; on an update it rewinds or skips ahead.
	From *string `json:"from"`
}

const (
	feedFromOldest = "oldest"
	feedFromNow    = "now"
)

// readFeedSubRequest decodes a create or an update body.
func (s *Server) readFeedSubRequest(r *http.Request) (feedSubRequest, string) {
	var req feedSubRequest
	body, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().Request))
	if err != nil {
		return req, "read body: " + err.Error()
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return req, "invalid JSON body: " + err.Error()
	}
	if req.BatchSize != nil && (*req.BatchSize < 0 || *req.BatchSize > maxFeedPushBatch) {
		return req, "batchSize must be between 1 and 1000, or 0 for the default"
	}
	if req.From != nil && *req.From != feedFromOldest && *req.From != feedFromNow {
		return req, `from must be "oldest" or "now"`
	}
	return req, ""
}

// handleListFeedSubscriptions lists every subscription with the Worker that delivers it
// and, for one whose endpoint is failing, its hold.
func (s *Server) handleListFeedSubscriptions(w http.ResponseWriter, _ *http.Request) {
	out := []feedSubView{}
	var loadErr error
	s.do(func() {
		var subs []feedSubscription
		if subs, loadErr = s.feedSubs.LoadAll(); loadErr != nil {
			return
		}
		for _, sub := range subs {
			v := feedSubView{feedSubscription: sub}
			if wk, ok, err := s.connectors.Get(sub.WorkerID); err == nil && ok {
				v.WorkerName = wk.Name
			}
			if h, held := s.feedPushes.hold(sub.ID); held {
				v.Hold = &h
			}
			out = append(out, v)
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list feed subscriptions: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// handleCreateFeedSubscription subscribes a cloudevents Worker to the feed.
func (s *Server) handleCreateFeedSubscription(w http.ResponseWriter, r *http.Request) {
	req, bad := s.readFeedSubRequest(r)
	if bad != "" {
		httpapi.Error(w, http.StatusBadRequest, bad)
		return
	}
	if req.WorkerID == nil || strings.TrimSpace(*req.WorkerID) == "" {
		httpapi.Error(w, http.StatusBadRequest, "workerId is required: the cloudevents Worker that delivers the feed")
		return
	}
	var asked []string
	if req.Reach != nil {
		asked = *req.Reach
	}
	reach, refusal, err := s.reachFor(r, apiScopeEvents, asked)
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "check reach: "+err.Error())
		return
	case refusal != "":
		httpapi.Error(w, http.StatusBadRequest, refusal)
		return
	}
	from := feedFromOldest
	if req.From != nil {
		from = *req.From
	}
	cursor, err := s.feedCursorAt(from)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "place the cursor: "+err.Error())
		return
	}
	id, err := newID()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "generate id: "+err.Error())
		return
	}
	rec := feedSubscription{ID: id, WorkerID: strings.TrimSpace(*req.WorkerID), Reach: reach, Cursor: cursor,
		Enabled: true, CreatedAt: time.Now().Unix()}
	if req.BatchSize != nil {
		rec.BatchSize = *req.BatchSize
	}
	if req.Enabled != nil {
		rec.Enabled = *req.Enabled
	}
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		rec.CreatedBy = p.UserID
	}
	var (
		badWorker string
		saveErr   error
	)
	s.do(func() {
		wk, ok, err := s.connectors.Get(rec.WorkerID)
		switch {
		case err != nil:
			saveErr = err
		case !ok:
			badWorker = "no worker " + rec.WorkerID
		case wk.Kind != connectorKindCloudEvents:
			badWorker = "worker " + wk.Name + " is a " + wk.Kind + " worker; the feed is delivered to a cloudevents worker"
		default:
			saveErr = s.feedSubs.Save(rec)
		}
	})
	switch {
	case saveErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "create feed subscription: "+saveErr.Error())
		return
	case badWorker != "":
		httpapi.Error(w, http.StatusBadRequest, badWorker)
		return
	}
	audit(r, logging.FeedSubscriptionChanged, "feed subscription created",
		slog.String("subscription_id", rec.ID), slog.String("worker_id", rec.WorkerID),
		slog.String("reach", strings.Join(rec.Reach, " ")))
	httpapi.JSON(w, http.StatusCreated, feedSubView{feedSubscription: rec})
}

// handleUpdateFeedSubscription changes a subscription: its reach, its batch size, whether
// it is enabled, and — with from — where its cursor stands. Enabling one clears the reason
// delivery switched it off; moving its cursor lifts its hold, so the next round tries it.
func (s *Server) handleUpdateFeedSubscription(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	req, bad := s.readFeedSubRequest(r)
	if bad != "" {
		httpapi.Error(w, http.StatusBadRequest, bad)
		return
	}
	if req.WorkerID != nil {
		httpapi.Error(w, http.StatusBadRequest, "a subscription's worker does not change; delete it and subscribe the other worker")
		return
	}
	var reach []string
	if req.Reach != nil {
		var (
			refusal string
			err     error
		)
		if reach, refusal, err = s.reachFor(r, apiScopeEvents, *req.Reach); err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "check reach: "+err.Error())
			return
		} else if refusal != "" {
			httpapi.Error(w, http.StatusBadRequest, refusal)
			return
		}
	}
	var (
		cursor   uint64
		placeErr error
	)
	if req.From != nil {
		if cursor, placeErr = s.feedCursorAt(*req.From); placeErr != nil {
			httpapi.Error(w, http.StatusInternalServerError, "place the cursor: "+placeErr.Error())
			return
		}
	}
	var (
		rec     feedSubscription
		found   bool
		saveErr error
	)
	s.do(func() {
		if rec, found, saveErr = s.feedSubs.Get(id); saveErr != nil || !found {
			return
		}
		if req.Reach != nil {
			rec.Reach = reach
		}
		if req.BatchSize != nil {
			rec.BatchSize = *req.BatchSize
		}
		if req.From != nil {
			rec.Cursor = cursor
			s.feedPushes.succeeded(id)
		}
		if req.Enabled != nil {
			rec.Enabled = *req.Enabled
			if rec.Enabled {
				rec.DisabledReason = ""
				s.feedPushes.succeeded(id)
			}
		}
		saveErr = s.feedSubs.Save(rec)
	})
	switch {
	case saveErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "update feed subscription: "+saveErr.Error())
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no feed subscription "+id)
		return
	}
	audit(r, logging.FeedSubscriptionChanged, "feed subscription updated",
		slog.String("subscription_id", rec.ID), slog.Bool("enabled", rec.Enabled),
		slog.String("reach", strings.Join(rec.Reach, " ")), slog.Uint64("cursor", rec.Cursor))
	httpapi.JSON(w, http.StatusOK, feedSubView{feedSubscription: rec})
}

// handleDeleteFeedSubscription ends a subscription. Deleting one that is absent succeeds:
// the state asked for holds.
func (s *Server) handleDeleteFeedSubscription(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var delErr error
	s.do(func() {
		delErr = s.feedSubs.Delete(id)
		s.feedPushes.succeeded(id)
	})
	if delErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "delete feed subscription: "+delErr.Error())
		return
	}
	audit(r, logging.FeedSubscriptionChanged, "feed subscription deleted", slog.String("subscription_id", id))
	w.WriteHeader(http.StatusNoContent)
}

// deleteFeedSubscriptionsOf deletes the subscriptions a Worker delivers, as the Worker is
// deleted. Runs on the run loop.
func (s *Server) deleteFeedSubscriptionsOf(workerID string) error {
	subs, err := s.feedSubs.LoadAll()
	if err != nil {
		return err
	}
	for _, sub := range subs {
		if sub.WorkerID != workerID {
			continue
		}
		if err := s.feedSubs.Delete(sub.ID); err != nil {
			return err
		}
		s.feedPushes.succeeded(sub.ID)
	}
	return nil
}

// feedCursorAt is the cursor "oldest" or "now" names in the feed as it stands: after the
// cut its retention made, or after its newest row.
func (s *Server) feedCursorAt(from string) (uint64, error) {
	view, _, part, err := s.feedSnapshot()
	if err != nil {
		return 0, err
	}
	defer view.Close()
	if from == feedFromNow {
		return view.FeedLast(part)
	}
	return view.FeedPrunedThrough(part)
}

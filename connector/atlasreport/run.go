package atlasreport

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/pblumer/atlas/connector/atlasreport/catalog"
	"github.com/pblumer/atlas/job"
)

// Job is an Atlas report task with its input evaluated. There is nowhere here to put a
// token: the engine holds none.
type Job struct {
	// Connector names the Worker — the Atlas server and the token it reads with.
	Connector string `json:"connector"`
	Operation string `json:"operation"`

	Process  string `json:"process,omitempty"`
	Catalog  string `json:"catalog,omitempty"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	Buckets  int32  `json:"buckets,omitempty"`
	Bucket   string `json:"bucket,omitempty"`
	Title    string `json:"title,omitempty"`
	Language string `json:"language,omitempty"`

	ResultVariable string `json:"resultVariable,omitempty"`
	// ErrorCodes are the codes an error boundary or error event subprocess of the task's
	// process catches (ADR-0461); they travel because a worker has no compiled process.
	ErrorCodes []string `json:"errorCodes,omitempty"`
}

// The BPMN error codes a refusal is thrown as when the process catches it.
const (
	CodeRefused  = "ATLASREPORT_REFUSED"
	CodeNotFound = "ATLASREPORT_NOT_FOUND"
)

type errKind int

const (
	kindUnreachable errKind = iota + 1
	kindRefused
	kindNotFound
)

type callError struct {
	kind errKind
	err  error
}

func (e *callError) Error() string { return e.err.Error() }
func (e *callError) Unwrap() error { return e.err }

// thrown is err as the job ends with it: a refusal or a missing process the process has
// a path for becomes that BPMN error; anything else stays a failure, retried.
func thrown(j Job, err error) error {
	var ce *callError
	code := ""
	if errors.As(err, &ce) {
		switch ce.kind {
		case kindRefused:
			code = CodeRefused
		case kindNotFound:
			code = CodeNotFound
		}
	}
	if code != "" && slices.Contains(j.ErrorCodes, code) {
		return &job.BPMNError{Code: code, Message: err.Error()}
	}
	return err
}

// DefaultPeriod is the span process-statistics and delivery-report measure when the task
// names none: the thirty days up to now.
const DefaultPeriod = 30 * 24 * time.Hour

// Run performs a resolved job and returns the variables the job completes with.
func Run(ctx context.Context, j Job, reg *Registry) (map[string]any, error) {
	return RunAt(ctx, j, reg, time.Now())
}

// RunAt is [Run] at a given instant: the default period and the buckets end at now.
func RunAt(ctx context.Context, j Job, reg *Registry, now time.Time) (map[string]any, error) {
	c, ok := reg.Get(j.Connector)
	if !ok {
		return nil, fmt.Errorf("atlasreport: this worker holds no Atlas server named %q", j.Connector)
	}
	if _, ok := catalog.Lookup(j.Operation); !ok {
		return nil, fmt.Errorf("atlasreport: unknown operation %q", j.Operation)
	}
	r, err := report(ctx, c, j, now)
	if err != nil {
		return nil, thrown(j, err)
	}
	if err := r.Check(); err != nil {
		return nil, err
	}
	if j.ResultVariable == "" {
		return map[string]any{}, nil
	}
	return map[string]any{j.ResultVariable: r}, nil
}

func report(ctx context.Context, c *Client, j Job, now time.Time) (Report, error) {
	switch j.Operation {
	case catalog.OpInstancesSummary:
		var rows []InstanceSummaryRow
		if err := c.get(ctx, "/api/v1/instances/summary", nil, &rows); err != nil {
			return Report{}, err
		}
		return FoldInstancesSummary(rows, j.Process, j.Title, j.Language, now), nil

	case catalog.OpProcessStatistics:
		var rows []InstanceSummaryRow
		if err := c.get(ctx, "/api/v1/instances/summary", nil, &rows); err != nil {
			return Report{}, err
		}
		def, ok := LatestVersion(rows, j.Process)
		if !ok {
			return Report{}, &callError{kind: kindNotFound, err: fmt.Errorf("atlasreport: no deployed process %q", j.Process)}
		}
		from, to, err := period(j, now)
		if err != nil {
			return Report{}, err
		}
		windows := []Window{{From: from, To: to}}
		if j.Buckets > 0 {
			windows = Windows(to, int(j.Buckets), j.Bucket, j.Language)
		}
		answers := make([]StatsAnswer, len(windows))
		path := "/api/v1/processes/" + strconv.FormatUint(def.ProcessDefKey, 10) + "/statistics"
		for i, win := range windows {
			q := url.Values{"from": {win.From.UTC().Format(time.RFC3339)}, "to": {win.To.UTC().Format(time.RFC3339)}}
			if err := c.get(ctx, path, q, &answers[i]); err != nil {
				return Report{}, err
			}
		}
		return FoldStatistics(def, windows, answers, j.Title, j.Language, now), nil

	case catalog.OpIncidentSummary:
		var a IncidentSummaryAnswer
		if err := c.get(ctx, "/api/v1/incidents/summary", nil, &a); err != nil {
			return Report{}, err
		}
		return FoldIncidentSummary(a, j.Process, j.Title, j.Language, now), nil

	case catalog.OpDeliveryReport:
		q := url.Values{}
		if j.Catalog != "" {
			q.Set("catalog", j.Catalog)
		}
		// The delivery report reads dates in the server's zone; an instant names its day.
		for k, v := range map[string]string{"from": j.From, "to": j.To} {
			if v == "" {
				continue
			}
			t, err := parseMoment(v)
			if err != nil {
				return Report{}, fmt.Errorf("atlasreport: %s: %w", k, err)
			}
			q.Set(k, t.Format("2006-01-02"))
		}
		var a DeliveryAnswer
		if err := c.get(ctx, "/api/v1/catalog-products/delivery-report", q, &a); err != nil {
			return Report{}, err
		}
		return FoldDelivery(a, j.Title, j.Language, now), nil
	}
	return Report{}, fmt.Errorf("atlasreport: unknown operation %q", j.Operation)
}

// period is the span a task names, its default the thirty days up to now.
func period(j Job, now time.Time) (time.Time, time.Time, error) {
	to := now
	if j.To != "" {
		t, err := parseMoment(j.To)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("atlasreport: to: %w", err)
		}
		to = t
	}
	from := to.Add(-DefaultPeriod)
	if j.From != "" {
		t, err := parseMoment(j.From)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("atlasreport: from: %w", err)
		}
		from = t
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, fmt.Errorf("atlasreport: from %s is not before to %s", from.Format(time.RFC3339), to.Format(time.RFC3339))
	}
	return from, to, nil
}

// parseMoment reads an RFC 3339 instant or a date (midnight UTC).
func parseMoment(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is neither an RFC 3339 instant nor a date", s)
	}
	return t, nil
}

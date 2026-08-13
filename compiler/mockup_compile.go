package compiler

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/expr"
)

// compileMockupTask compiles an <atlas:mockupConnector> service task: the engine
// simulates it itself (ADR-0120) instead of dispatching a job to an external worker
// or connector. It bounds the random simulated duration from minDuration/maxDuration
// (ISO-8601, a fixed duration being min == max), compiles the optional FEEL result
// expression at deploy time (invariant I5), and scales the failure probability to
// parts-per-million so the runtime decision stays integer-pure. It validates the
// bounds, the failure rate range, and that a result expression and its variable are
// authored together.
func compileMockupTask(b *Builder, st xmlServiceTask) (int32, error) {
	cn := st.Mockup
	minNanos, err := mockupDuration(st.Id, "minDuration", cn.MinDuration, 0)
	if err != nil {
		return 0, err
	}
	// An omitted maxDuration means a fixed duration (max == min), not zero.
	maxNanos, err := mockupDuration(st.Id, "maxDuration", cn.MaxDuration, minNanos)
	if err != nil {
		return 0, err
	}
	if maxNanos < minNanos {
		return 0, fmt.Errorf("compiler: mockup task %q has maxDuration before minDuration", st.Id)
	}

	resultVar := strings.TrimSpace(cn.ResultVariable)
	exprText := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(cn.ResultExpression), "="))
	var compiled *expr.Compiled
	if exprText != "" {
		if resultVar == "" {
			return 0, fmt.Errorf("compiler: mockup task %q has a result expression but no result variable", st.Id)
		}
		compiled, err = expr.CompileAuto(exprText)
		if err != nil {
			return 0, fmt.Errorf("compiler: mockup task %q result expression: %w", st.Id, err)
		}
	} else if resultVar != "" {
		return 0, fmt.Errorf("compiler: mockup task %q names a result variable but has no result expression", st.Id)
	}

	failPerMillion, err := mockupFailPerMillion(st.Id, cn.FailRate)
	if err != nil {
		return 0, err
	}

	return b.AddMockupTask(MockupConfig{
		MinNanos:       minNanos,
		MaxNanos:       maxNanos,
		ResultVar:      resultVar,
		Expr:           compiled,
		FailPerMillion: failPerMillion,
		FailMessage:    strings.TrimSpace(cn.FailMessage),
	}), nil
}

// mockupDuration parses one ISO-8601 duration attribute of a mockup task, returning
// def when the attribute is empty. what names the attribute for diagnostics.
func mockupDuration(taskID, what, raw string, def int64) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	nanos, err := parseISO8601Duration(raw)
	if err != nil {
		return 0, fmt.Errorf("compiler: mockup task %q %s: %w", taskID, what, err)
	}
	return nanos, nil
}

// mockupFailPerMillion parses a mockup task's failRate (a probability in [0,1]) into
// parts-per-million. An empty rate is 0 (never fail). A rate outside [0,1] or not a
// number is a compile error.
func mockupFailPerMillion(taskID, raw string) (int32, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	rate, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("compiler: mockup task %q has invalid failRate %q: %w", taskID, raw, err)
	}
	if math.IsNaN(rate) || rate < 0 || rate > 1 {
		return 0, fmt.Errorf("compiler: mockup task %q failRate %q must be between 0 and 1", taskID, raw)
	}
	return int32(math.Round(rate * 1_000_000)), nil
}

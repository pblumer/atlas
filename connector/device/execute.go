package device

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"time"

	"github.com/pblumer/atlas/connector/device/catalog"
)

// Request is one job as the device runs it: the operation and the values the server
// resolved for it when it leased the job (ADR-0168).
type Request struct {
	JobKey uint64
	Op     string
	Fields map[string]any
}

// Execute runs one job against the device. The result is what a result variable
// keeps, nil for an operation that returns nothing. Every way the request itself is
// wrong — an unknown operation, a missing or unused value, a value of the wrong type —
// is a RefusedError, checked before anything is driven.
func (d *Device) Execute(ctx context.Context, req Request) (map[string]any, error) {
	op, ok := catalog.Lookup(req.Op)
	if !ok {
		return nil, refused("unknown operation %q", req.Op)
	}
	keys := make([]string, 0, len(req.Fields))
	for k := range req.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys) // the first refusal names the same value every time
	for _, k := range keys {
		if !op.Uses(k) {
			return nil, refused("%s does not use %q", op.Name, k)
		}
	}
	for _, a := range op.Needs {
		if _, ok := req.Fields[a]; !ok {
			return nil, refused("%s needs %q", op.Name, a)
		}
	}

	switch op.Name {
	case catalog.OpDigitalWrite:
		name, err := stringField(req.Fields, catalog.AttrOutput)
		if err != nil {
			return nil, err
		}
		on, err := boolField(req.Fields, catalog.AttrOn)
		if err != nil {
			return nil, err
		}
		return nil, d.Write(ctx, name, on)
	case catalog.OpDigitalRead:
		name, err := stringField(req.Fields, catalog.AttrLine)
		if err != nil {
			return nil, err
		}
		on, err := d.Read(name)
		if err != nil {
			return nil, err
		}
		return map[string]any{"on": on}, nil
	default: // catalog.OpPulse: the table has no other row
		name, err := stringField(req.Fields, catalog.AttrOutput)
		if err != nil {
			return nil, err
		}
		on := true
		if _, set := req.Fields[catalog.AttrOn]; set {
			if on, err = boolField(req.Fields, catalog.AttrOn); err != nil {
				return nil, err
			}
		}
		ms, err := millisField(req.Fields, catalog.AttrDuration)
		if err != nil {
			return nil, err
		}
		_, err = d.Pulse(ctx, req.JobKey, name, on, time.Duration(ms)*time.Millisecond)
		return nil, err
	}
}

func stringField(f map[string]any, name string) (string, error) {
	s, ok := f[name].(string)
	if !ok {
		return "", refused("%s is a name, not %T", name, f[name])
	}
	return s, nil
}

func boolField(f map[string]any, name string) (bool, error) {
	b, ok := f[name].(bool)
	if !ok {
		return false, refused("%s is true or false, not %T", name, f[name])
	}
	return b, nil
}

// millisField reads a whole number of milliseconds, however the job's JSON decoded it.
func millisField(f map[string]any, name string) (int64, error) {
	switch v := f[name].(type) {
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n, nil
		}
	case float64:
		if v == math.Trunc(v) && math.Abs(v) < 1<<53 {
			return int64(v), nil
		}
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	}
	return 0, refused("%s is a whole number of milliseconds, not %v", name, f[name])
}

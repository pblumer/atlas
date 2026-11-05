package compiler

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/connector/device/catalog"
)

// compileDeviceConnectorTask compiles an <atlas:deviceConnector> task: one operation on a
// device's named outputs and inputs, served by the worker on that device
// (ADR-draft-device-workers-for-the-physical-world).
//
// The operation table is connector/device/catalog's, read here directly so the rules a
// deploy enforces are the very rows the device runs. Every value is required where the
// operation needs it and refused where it does not use it, so a value is never silently
// ignored. Values written out literally are checked here, where it is still the author
// reading the message; values a FEEL expression yields are checked by the device.
func compileDeviceConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.Device
	fail := func(format string, args ...any) (int32, error) {
		return 0, fmt.Errorf("compiler: device task %q "+format, append([]any{st.Id}, args...)...)
	}
	if strings.TrimSpace(cn.Connector) == "" {
		return fail("needs a connector attribute naming the device it acts on")
	}
	workerExpr, err := connectorValue(b.gate(), st.Id, "device worker", "connector", cn.Connector)
	if err != nil {
		return 0, err
	}
	name := strings.TrimSpace(cn.Operation)
	if name == "" {
		return fail("needs an operation (%s)", strings.Join(catalog.Names(), ", "))
	}
	op, ok := catalog.Lookup(name)
	if !ok {
		return fail("has an unknown operation %q (want %s)", cn.Operation, strings.Join(catalog.Names(), ", "))
	}

	raw := map[string]string{
		catalog.AttrOutput:   strings.TrimSpace(cn.Output),
		catalog.AttrLine:     strings.TrimSpace(cn.Line),
		catalog.AttrOn:       strings.TrimSpace(cn.On),
		catalog.AttrDuration: strings.TrimSpace(cn.DurationMs),
	}
	describe := map[string]string{
		catalog.AttrOutput:   "output (the name of one of the device's outputs, e.g. relais)",
		catalog.AttrLine:     "line (the name of one of the device's outputs or inputs)",
		catalog.AttrOn:       "on (true or false, or a FEEL expression)",
		catalog.AttrDuration: "durationMs (the pulse's length in milliseconds)",
	}
	for _, attr := range catalog.Attributes {
		switch set := raw[attr] != ""; {
		case op.NeedsAttr(attr) && !set:
			return fail("operation %q needs %s", name, describe[attr])
		case set && !op.Uses(attr):
			return fail("operation %q does not use %s; remove it rather than leaving a value the device ignores", name, describe[attr])
		}
	}
	switch rv := strings.TrimSpace(cn.ResultVariable) != ""; {
	case op.Returns && !rv:
		return fail("operation %q needs a resultVariable (a read that discards its result is one nothing asked for)", name)
	case rv && !op.Returns:
		return fail("operation %q returns nothing, so a resultVariable would name a value that is never written; remove it", name)
	}

	literal := func(attr string) (string, bool) {
		v := raw[attr]
		return v, v != "" && !strings.HasPrefix(v, "=")
	}
	for _, attr := range []string{catalog.AttrOutput, catalog.AttrLine} {
		if v, ok := literal(attr); ok {
			if err := catalog.CheckName(v); err != nil {
				return fail("%s: %s", attr, strings.TrimPrefix(err.Error(), "device: "))
			}
		}
	}
	if v, ok := literal(catalog.AttrOn); ok && v != "true" && v != "false" {
		return fail("on is %q; write true or false, or a FEEL expression starting with =", v)
	}
	if v, ok := literal(catalog.AttrDuration); ok {
		ms, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fail("durationMs %q is not a whole number of milliseconds", v)
		}
		if err := catalog.CheckDuration(ms); err != nil {
			return fail("%s", strings.TrimPrefix(err.Error(), "device: "))
		}
	}

	cfg := DeviceConfig{
		Worker:     strings.TrimSpace(cn.Connector),
		WorkerExpr: workerExpr,
		Op:         name,
		ResultVar:  strings.TrimSpace(cn.ResultVariable),
		Retries:    retries,
	}
	for _, v := range []struct {
		attr string
		into *RestExpr
	}{
		{catalog.AttrOutput, &cfg.Output},
		{catalog.AttrLine, &cfg.Line},
		{catalog.AttrOn, &cfg.On},
		{catalog.AttrDuration, &cfg.Duration},
	} {
		if raw[v.attr] == "" {
			continue
		}
		val, err := connectorValue(b.gate(), st.Id, "device", v.attr, raw[v.attr])
		if err != nil {
			return 0, err
		}
		*v.into = val
	}
	return b.AddDeviceConnectorTask(cfg), nil
}

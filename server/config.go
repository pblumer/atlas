// Package server hosts the atlasd HTTP surfaces: the public API, the operations
// console web UI, and the MCP endpoint. Per ADR-0011 these all live in a single
// binary sharing one API layer; per ADR-0012 every optional surface is on by
// default and disabled only by an explicit ATLAS_* environment variable.
package server

import (
	"fmt"
	"os"
	"strings"
)

// DefaultAddr is the address atlasd binds when ATLAS_ADDR is unset. It is
// loopback-only on purpose: on-by-default (ADR-0012) must not mean
// exposed-to-the-network by default. Binding publicly is an explicit choice.
const DefaultAddr = "127.0.0.1:8080"

// Config is the resolved runtime configuration for the atlasd server. The zero
// value is not meaningful; build one with ConfigFromEnv or set every field.
type Config struct {
	// Addr is the listen address (host:port). Sourced from ATLAS_ADDR.
	Addr string
	// Web enables the operations console web UI. Sourced from ATLAS_WEB.
	Web bool
	// MCP enables the MCP endpoint. Sourced from ATLAS_MCP.
	MCP bool
}

// ConfigFromEnv resolves configuration from the environment following the
// batteries-on/opt-out principle (ADR-0012): every optional surface defaults to
// enabled, and disabling one requires an explicit, recognized falsy value. An
// unrecognized value is an error rather than a silent default, so a typo in a
// deployment cannot quietly leave a surface in the wrong state.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		Addr: DefaultAddr,
		Web:  true,
		MCP:  true,
	}
	if v, ok := os.LookupEnv("ATLAS_ADDR"); ok && v != "" {
		cfg.Addr = v
	}
	var err error
	if cfg.Web, err = envToggle("ATLAS_WEB", true); err != nil {
		return Config{}, err
	}
	if cfg.MCP, err = envToggle("ATLAS_MCP", true); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// envToggle reads a boolean-style opt-out variable. Unset (or empty) yields def.
// Recognized truthy/falsy spellings are accepted case-insensitively; anything
// else is a hard error so misconfiguration is loud, not silent.
func envToggle(name string, def bool) (bool, error) {
	v, ok := os.LookupEnv(name)
	if !ok || v == "" {
		return def, nil
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "on", "true", "yes", "enable", "enabled":
		return true, nil
	case "0", "off", "false", "no", "disable", "disabled":
		return false, nil
	default:
		return false, fmt.Errorf("%s: unrecognized value %q (use one of on/off, true/false, 1/0, yes/no)", name, v)
	}
}

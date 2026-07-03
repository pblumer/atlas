package server

import "testing"

func TestConfigFromEnvDefaultsEverythingOn(t *testing.T) {
	// No ATLAS_* vars set: batteries-on (ADR-0012).
	t.Setenv("ATLAS_ADDR", "")
	t.Setenv("ATLAS_WEB", "")
	t.Setenv("ATLAS_MCP", "")

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.Addr != DefaultAddr {
		t.Errorf("Addr = %q, want %q", cfg.Addr, DefaultAddr)
	}
	if !cfg.Web || !cfg.MCP {
		t.Errorf("surfaces = web:%v mcp:%v, want both on by default", cfg.Web, cfg.MCP)
	}
}

func TestConfigFromEnvOptOut(t *testing.T) {
	t.Setenv("ATLAS_WEB", "off")
	t.Setenv("ATLAS_MCP", "0")
	t.Setenv("ATLAS_ADDR", ":9000")

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.Web {
		t.Error("ATLAS_WEB=off should disable web")
	}
	if cfg.MCP {
		t.Error("ATLAS_MCP=0 should disable mcp")
	}
	if cfg.Addr != ":9000" {
		t.Errorf("Addr = %q, want :9000", cfg.Addr)
	}
}

func TestEnvToggleRecognizedSpellings(t *testing.T) {
	cases := map[string]bool{
		"on": true, "ON": true, "true": true, "1": true, "yes": true, "enabled": true,
		"off": false, "OFF": false, "false": false, "0": false, "no": false, "disabled": false,
		" off ": false, // trimmed
	}
	for in, want := range cases {
		t.Setenv("ATLAS_WEB", in)
		got, err := envToggle("ATLAS_WEB", true)
		if err != nil {
			t.Errorf("envToggle(%q): unexpected error %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("envToggle(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestEnvToggleUnrecognizedIsError(t *testing.T) {
	t.Setenv("ATLAS_WEB", "maybe")
	if _, err := envToggle("ATLAS_WEB", true); err == nil {
		t.Fatal("expected error for unrecognized value, got nil")
	}
}

func TestEnvToggleUnsetUsesDefault(t *testing.T) {
	t.Setenv("ATLAS_WEB", "")
	for _, def := range []bool{true, false} {
		got, err := envToggle("ATLAS_WEB", def)
		if err != nil {
			t.Fatalf("envToggle: %v", err)
		}
		if got != def {
			t.Errorf("unset value = %v, want default %v", got, def)
		}
	}
}

package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/pblumer/atlas/internal/sysproxy"
	"github.com/pblumer/atlas/logging"
)

// The choice is said once at startup, under a stable event name, so an operator whose
// Worker times out on one host can read which route that process took.
func TestInstallOutboundProxyLogsTheChoice(t *testing.T) {
	var buf bytes.Buffer
	if err := logging.Setup(&buf, logging.DefaultFormat, logging.DefaultLevel); err != nil {
		t.Fatalf("logging.Setup: %v", err)
	}
	t.Cleanup(func() { _ = logging.Setup(os.Stderr, logging.DefaultFormat, logging.DefaultLevel) })

	installOutboundProxy(func(k string) string {
		if k == "HTTPS_PROXY" {
			return "http://proxy.example:8080"
		}
		return ""
	})
	out := buf.String()
	for _, want := range []string{"event=outbound_proxy.selected", "source=environment"} {
		if !strings.Contains(out, want) {
			t.Errorf("startup log %q lacks %q", out, want)
		}
	}
}

// Following the Windows settings, the line names them — and never the credentials a
// proxy address may carry.
func TestOutboundProxyAttrsNameTheWindowsSettings(t *testing.T) {
	attrs := outboundProxyAttrs(sysproxy.ModeWindows, sysproxy.Settings{
		AutoDetect: true, AutoConfigURL: "http://wpad.example/proxy.pac", Proxy: "user:pw@prx.example:8080", Bypass: "<local>",
	})
	got := map[string]string{}
	for _, a := range attrs {
		got[a.Key] = a.Value.String()
	}
	want := map[string]string{"source": "windows", "autoDetect": "true", "pacUrl": "http://wpad.example/proxy.pac", "proxy": "***@prx.example:8080", "bypass": "<local>"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if only := outboundProxyAttrs(sysproxy.ModeNone, sysproxy.Settings{}); len(only) != 1 {
		t.Errorf("with no settings to follow the line carries %d attributes, want the source alone", len(only))
	}
}

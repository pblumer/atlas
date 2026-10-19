//go:build !windows

package sysproxy

import (
	"net/http"
	"testing"
)

// Outside Windows there are no system settings to follow, so Install leaves the default
// transport exactly as Go built it: the proxy variables, nothing else.
func TestInstallLeavesOtherPlatformsAlone(t *testing.T) {
	tr := http.DefaultTransport.(*http.Transport)
	before := tr.Proxy
	mode, s := Install(func(string) string { return "" })
	if mode != ModeNone || s != (Settings{}) {
		t.Errorf("Install = %s %+v, want %s and no settings", mode, s, ModeNone)
	}
	if (tr.Proxy == nil) != (before == nil) {
		t.Error("Install changed the default transport's proxy")
	}
}

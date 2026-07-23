package api

import (
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// TestDMNResolverFromEnv covers the resolver selection: the on-disk default when
// no service URL is configured, and a temis service resolver (with its token) when
// ATLAS_DMN_RESOLVER_URL is set.
func TestDMNResolverFromEnv(t *testing.T) {
	t.Run("default is the on-disk folder", func(t *testing.T) {
		if _, ok := dmnResolverFromEnv("/data/dmn-models").(dmn.DirResolver); !ok {
			t.Fatal("with no env set, want a DirResolver")
		}
	})

	t.Run("service url selects the service resolver", func(t *testing.T) {
		t.Setenv("ATLAS_DMN_RESOLVER_URL", "https://temis.example/models")
		t.Setenv("ATLAS_DMN_RESOLVER_TOKEN", "tok")
		sr, ok := dmnResolverFromEnv("/data/dmn-models").(dmn.ServiceResolver)
		if !ok {
			t.Fatal("with ATLAS_DMN_RESOLVER_URL set, want a ServiceResolver")
		}
		if sr.BaseURL != "https://temis.example/models" || sr.Token != "tok" {
			t.Fatalf("resolver = %+v, want the configured url and token", sr)
		}
	})
}

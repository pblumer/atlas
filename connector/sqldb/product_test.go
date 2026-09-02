package sqldb

import "testing"

// The environment vocabulary, per product.
//
// Three packages have to spell these variables identically — the worker reads them,
// the engine renders them for a supervised worker, and the mock names one in the
// refusal an operator acts on — and none of the three can see the others do it. A
// second spelling is not a compile error; it is an operator who sets the variable and
// is told it is missing. So the names live on the product and this holds them to the
// literal text an operator types.
func TestProductEnvironmentVocabulary(t *testing.T) {
	for _, tc := range []struct{ name, prefix string }{
		{"mssql", "ATLAS_MSSQL_"},
		{"mariadb", "ATLAS_MARIADB_"},
		{"postgres", "ATLAS_POSTGRES_"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := mustProduct(t, tc.name)
			for _, c := range []struct{ what, got, want string }{
				{"EnvPrefix", p.EnvPrefix(), tc.prefix},
				{"ConnectorsEnv", p.ConnectorsEnv(), tc.prefix + "CONNECTORS"},
				{"DSNEnv", p.DSNEnv("hr-db"), tc.prefix + "HR_DB_DSN"},
				{"MockEnv", p.MockEnv(), tc.prefix + "MOCK"},
				{"MockSeedEnv", p.MockSeedEnv(), tc.prefix + "MOCK_SEED"},
			} {
				if c.got != c.want {
					t.Errorf("%s = %q, want %q", c.what, c.got, c.want)
				}
			}
		})
	}
}

// A connector name is folded the way every other kind's is (envname.Key), so a name
// with punctuation in it addresses one variable rather than an unspellable one.
func TestDSNEnvFoldsTheConnectorName(t *testing.T) {
	p := mustProduct(t, "postgres")
	if got := p.DSNEnv("HR Prod.DB"); got != "ATLAS_POSTGRES_HR_PROD_DB_DSN" {
		t.Errorf("DSNEnv = %q, want the folded variable name", got)
	}
}

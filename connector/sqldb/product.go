package sqldb

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/envname"
)

// Product is one of the three databases a SQL task can target. The three
// share every line of code in this package and differ in exactly what a database
// driver forces them to differ in: the driver name, the placeholder syntax a statement
// is written in, and whether it can bind a parameter by name.
//
// Everything an operator or an author sees about a product is derived from here — the
// environment variables its worker reads, the placeholder the Modeler shows, the
// variable the mock's refusal names — so there is one place to answer "what is
// different about MariaDB", and no second answer to disagree with it.
//
// That is also why the product is part of the *model* rather than of the worker's
// configuration. A statement written with $1 is a PostgreSQL statement; a kind per
// product makes pointing it at SQL Server a thing the model cannot express, instead
// of a runtime error an operator meets at 3am (ADR-0173).
type Product struct {
	// Name is the operator-facing kind name: what `atlas worker --connector` takes
	// and what prefixes this product's environment variables.
	Name string
	// Driver is the database/sql driver name the worker opens. The driver itself is
	// registered by the worker's blank import, not here, so this package stays
	// testable against a fake driver and imports no vendor code.
	Driver string
	// JobType is the reserved job type a task of this product carries.
	JobType string
	// NamedParams reports whether the driver binds sql.Named parameters. Where it
	// does not, an object-shaped parameters variable is refused rather than
	// flattened into a positional order the author never wrote.
	NamedParams bool
	// Placeholder is the syntax a statement's parameters use, quoted in errors so a
	// mismatch names the fix.
	Placeholder string
}

// The environment vocabulary of one product. A worker reads these variables, the
// engine renders them for a supervised worker, and the mock names one in the refusal
// an operator has to act on — three packages that cannot see each other spell the same
// four names. Spelling them here once is [envname.Key]'s own argument applied a level up:
// a second spelling is not a compile error, it is an operator who sets the variable and
// is told it is missing.

// EnvPrefix is what every one of this product's variables starts with, e.g.
// ATLAS_MARIADB_.
func (p Product) EnvPrefix() string { return "ATLAS_" + envname.Key(p.Name) + "_" }

// ConnectorsEnv lists the databases a worker of this product serves. It is also the
// answer to "what can this worker actually reach", which is why mock mode still needs
// it: the model names a Worker, and a mockup with no names has nothing a task could
// address.
func (p Product) ConnectorsEnv() string { return p.EnvPrefix() + "CONNECTORS" }

// DSNEnv is where one named database's connection string is read from. The name is
// folded by [envname.Key], so two names that differ only in punctuation address one
// variable — the callers building a variable per name detect that collision and refuse
// rather than letting one silently take the other's credential.
func (p Product) DSNEnv(connector string) string {
	return p.EnvPrefix() + envname.Key(connector) + "_DSN"
}

// MockEnv turns mock mode on for this product: statements are answered from seeded
// answers in the worker's own memory and reach no database (ADR-0221).
func (p Product) MockEnv() string { return p.EnvPrefix() + "MOCK" }

// MockSeedEnv names the JSON file of seeded answers a mock starts with. Without it a
// mock knows nothing and every statement fails naming itself — which is the message
// that quotes this variable, so it has to be the product's own spelling and not a
// pattern the reader applies in their head.
func (p Product) MockSeedEnv() string { return p.EnvPrefix() + "MOCK_SEED" }

// products is the product table, keyed by operator-facing name.
var products = map[string]Product{
	"mssql":    {Name: "mssql", Driver: "sqlserver", JobType: compiler.MsSqlJobType, NamedParams: true, Placeholder: "@p1"},
	"mariadb":  {Name: "mariadb", Driver: "mysql", JobType: compiler.MariaDBJobType, NamedParams: false, Placeholder: "?"},
	"postgres": {Name: "postgres", Driver: "pgx", JobType: compiler.PostgresJobType, NamedParams: false, Placeholder: "$1"},
}

// byJobType indexes the same table by reserved job type, for the engine half.
var byJobType = func() map[string]Product {
	m := make(map[string]Product, len(products))
	for _, p := range products {
		m[p.JobType] = p
	}
	return m
}()

// ProductByName returns the product an operator named, and whether there is one.
func ProductByName(name string) (Product, bool) {
	p, ok := products[strings.ToLower(strings.TrimSpace(name))]
	return p, ok
}

// ProductByJobType returns the product a reserved job type belongs to.
func ProductByJobType(jobType string) (Product, bool) {
	p, ok := byJobType[jobType]
	return p, ok
}

// ProductNames lists the operator-facing names, sorted, for error messages that have
// to say what was expected.
func ProductNames() []string {
	names := make([]string, 0, len(products))
	for n := range products {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// UnknownProduct is the error a caller gets for a name no product answers to.
func UnknownProduct(name string) error {
	return fmt.Errorf("sqldb: no such database kind %q (have %s)", name, strings.Join(ProductNames(), ", "))
}

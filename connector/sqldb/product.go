package sqldb

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pblumer/atlas/compiler"
)

// Product is one of the three databases a SQL connector task can target. The three
// share every line of code in this package and differ in exactly what a database
// driver forces them to differ in: the driver name, and whether it can bind a
// parameter by name.
//
// That is also why the product is part of the *model* rather than of the worker's
// configuration. A statement written with $1 is a PostgreSQL statement; a kind per
// product makes pointing it at SQL Server a thing the model cannot express, instead
// of a runtime error an operator meets at 3am (ADR-draft-generic-sql-connector).
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

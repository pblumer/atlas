// Command evidence creates, renews and verifies the conformance evidence records of
// built-in Worker Types, and prints the dependency digest of any set of packages
// (ADR-0483). It runs from the module root.
//
// Usage:
//
//	go run ./workertype/evidence/cmd/evidence digest [-manifest] <package>...
//	go run ./workertype/evidence/cmd/evidence verify [<record>...]
//	go run ./workertype/evidence/cmd/evidence renew <record>...
//	go run ./workertype/evidence/cmd/evidence create -contract <path> -worker-type <id> -operation <id> -suite <name> <record>
//
// verify without arguments checks every record under workertype/evidence/records,
// which is what CI does. renew rewrites a stale record in place; the pull request
// that carries the change and the renewed record is what the maintainer approves.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/pblumer/atlas/internal/impldigest"
	"github.com/pblumer/atlas/workertype/evidence"
)

// recordsDir is where CI looks for records, relative to the module root.
const recordsDir = "workertype/evidence/records"

func main() {
	os.Exit(run(context.Background(), ".", os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, moduleDir string, args []string, stdout, stderr io.Writer) int {
	if err := dispatch(ctx, moduleDir, args, stdout); err != nil {
		fmt.Fprintf(stderr, "evidence: %v\n", err)
		return 1
	}
	return 0
}

var errUsage = errors.New("usage: evidence digest [-manifest] <package>... | verify [<record>...] | renew <record>... | create -contract <path> -worker-type <id> -operation <id> -suite <name> <record>")

func dispatch(ctx context.Context, moduleDir string, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}
	if _, err := os.Stat(moduleDir + "/go.mod"); err != nil {
		return fmt.Errorf("run from the module root: %w", err)
	}
	cmd, args := args[0], args[1:]
	switch cmd {
	case "digest":
		fs := flag.NewFlagSet("digest", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		manifest := fs.Bool("manifest", false, "print the manifest the digest is taken over")
		if err := fs.Parse(args); err != nil || fs.NArg() == 0 {
			return errUsage
		}
		res, err := impldigest.Compute(ctx, moduleDir, fs.Args()...)
		if err != nil {
			return err
		}
		if *manifest {
			if _, err := stdout.Write(res.Manifest); err != nil {
				return err
			}
		}
		_, err = fmt.Fprintln(stdout, res.Digest)
		return err
	case "verify":
		if len(args) == 0 {
			if err := evidence.VerifyAll(ctx, moduleDir, moduleDir+"/"+recordsDir); err != nil {
				return err
			}
			_, err := fmt.Fprintln(stdout, "every record is current")
			return err
		}
		var problems []error
		for _, path := range args {
			raw, err := os.ReadFile(path)
			if err == nil {
				err = evidence.Verify(ctx, moduleDir, raw)
			}
			if err != nil {
				problems = append(problems, fmt.Errorf("%s: %w", path, err))
				continue
			}
			fmt.Fprintf(stdout, "%s is current\n", path)
		}
		return errors.Join(problems...)
	case "renew":
		if len(args) == 0 {
			return errUsage
		}
		for _, path := range args {
			if err := renew(ctx, moduleDir, path, stdout); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		}
		return nil
	case "create":
		fs := flag.NewFlagSet("create", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		var b evidence.Binding
		fs.StringVar(&b.Contract, "contract", "", "module-relative path of the published contract")
		fs.StringVar(&b.WorkerType, "worker-type", "", "Worker Type ID")
		fs.StringVar(&b.Operation, "operation", "", "operation ID in the Worker Type's catalogue")
		fs.StringVar(&b.Suite, "suite", "", "registered conformance suite")
		if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
			return errUsage
		}
		path := fs.Arg(0)
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s exists; renew it instead", path)
		}
		rec, err := evidence.Create(ctx, moduleDir, b)
		if err != nil {
			return err
		}
		if err := write(path, rec); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "created %s\n", path)
		return err
	}
	return errUsage
}

func renew(ctx context.Context, moduleDir, path string, stdout io.Writer) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	rec, err := evidence.ParseEvidenceJSON(raw)
	if err != nil {
		return err
	}
	if err := evidence.Verify(ctx, moduleDir, raw); err == nil {
		_, err := fmt.Fprintf(stdout, "%s is current\n", path)
		return err
	} else if stale := (*evidence.StaleError)(nil); !errors.As(err, &stale) {
		return err
	}
	renewed, err := evidence.Renew(ctx, moduleDir, rec)
	if err != nil {
		return err
	}
	if err := write(path, renewed); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "renewed %s\n", path)
	return err
}

func write(path string, rec evidence.Evidence) error {
	raw, err := rec.Render()
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

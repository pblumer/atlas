package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const templateSpec = `
openapi: 3.0.0
info: {title: Petstore, version: '1.0.11'}
servers: [{url: 'https://api.example.com/v1'}]
paths:
  /pet/{petId}:
    get:
      operationId: getPetById
      summary: Find pet by ID
      responses:
        '200': {description: ok}
`

// writeTemplateSpec puts a document on disk and returns its path.
func writeTemplateSpec(t *testing.T, doc string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spec.yaml")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOpenAPITemplateWritesOnePackagePerOperation(t *testing.T) {
	out := filepath.Join(t.TempDir(), "packages")
	var banner bytes.Buffer
	if err := runOpenAPITemplate([]string{"--spec", writeTemplateSpec(t, templateSpec), "--out", out}, &banner); err != nil {
		t.Fatalf("runOpenAPITemplate: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(out, "getpetbyid.json"))
	if err != nil {
		t.Fatalf("read the written package: %v", err)
	}
	// The catalog's files are read by people as well as by the server: an escaped
	// ">=0.9" is valid JSON and wrong beside the packages it sits next to.
	if !strings.Contains(string(body), `">=0.9"`) {
		t.Errorf("engineCompat is escaped:\n%s", body)
	}
	var pkg map[string]any
	if err := json.Unmarshal(body, &pkg); err != nil {
		t.Fatalf("the written package is not JSON: %v", err)
	}
	if pkg["id"] != "openapi.petstore.getpetbyid" {
		t.Errorf("id = %v", pkg["id"])
	}
	// The banner says what these files are for, because a directory of packages
	// nothing can install is worth explaining where it appears.
	if got := banner.String(); !strings.Contains(got, "ADR-0212") || !strings.Contains(got, "1 package written") {
		t.Errorf("banner = %q", got)
	}
}

func TestOpenAPITemplateNeedsBothPaths(t *testing.T) {
	spec := writeTemplateSpec(t, templateSpec)
	for name, args := range map[string][]string{
		"no spec": {"--out", t.TempDir()},
		"no out":  {"--spec", spec},
	} {
		t.Run(name, func(t *testing.T) {
			if err := runOpenAPITemplate(args, &bytes.Buffer{}); err == nil {
				t.Error("want an error naming the missing flag")
			}
		})
	}
}

func TestOpenAPITemplateRefusesABrokenDocument(t *testing.T) {
	err := runOpenAPITemplate([]string{"--spec", writeTemplateSpec(t, "openapi: 3.0.0\n"), "--out", t.TempDir()}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no paths") {
		t.Errorf("err = %v, want the load error", err)
	}
}

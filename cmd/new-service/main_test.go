package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratorPreservesExistingServicesAndRewritesImports(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "templates/microservice")
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "services"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "main.go"), []byte(`import "qslate/templates/microservice/internal/http"`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := generate(root, "workspace"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "services/workspace/main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "qslate/services/workspace/internal/http") {
		t.Fatal("template imports not adapted")
	}
	if err = generate(root, "workspace"); err == nil {
		t.Fatal("existing service overwritten")
	}
	for _, name := range []string{"../auth", "Auth", "", "a--b"} {
		if err = generate(root, name); err == nil {
			t.Fatalf("invalid name %q accepted", name)
		}
	}
}

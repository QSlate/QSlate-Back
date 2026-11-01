package main

import (
	"os"
	"os/exec"
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
	for _, name := range []string{"AGENTS.md", ".env", ".env.oauth", "go.work"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte("local-only"), 0600); err != nil {
			t.Fatal(err)
		}
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
	for _, name := range []string{"AGENTS.md", ".env", ".env.oauth", "go.work"} {
		if _, err := os.Stat(filepath.Join(root, "services/workspace", name)); !os.IsNotExist(err) {
			t.Fatalf("local artifact copied: %s", name)
		}
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

func TestGeneratedServiceBuildsWithoutRepositoryOrWorkspace(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "templates"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(root, "templates/microservice"), os.DirFS("../../templates/microservice")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "services"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := generate(root, "standalone-check"); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "services/standalone-check")
	for _, name := range []string{"go.mod", "go.sum", "Dockerfile", ".dockerignore", "Makefile", "internal/platform/token/token.go"} {
		if _, err := os.Stat(filepath.Join(target, name)); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "-mod=readonly", "./...")
	cmd.Dir = target
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated service is not standalone: %v\n%s", err, output)
	}
}

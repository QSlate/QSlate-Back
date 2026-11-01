package architecture

import (
	"context"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func moduleDirectories(t *testing.T) []string {
	t.Helper()
	services, err := filepath.Glob("../../services/*/go.mod")
	if err != nil {
		t.Fatal(err)
	}
	modules := []string{"../../gateway", "../../templates/microservice"}
	for _, path := range services {
		modules = append(modules, filepath.Dir(path))
	}
	return modules
}

func TestModulesDoNotImportSiblingOrRootPackages(t *testing.T) {
	for _, dir := range moduleDirectories(t) {
		body, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields(string(body))
		if len(fields) < 2 || fields[0] != "module" {
			t.Fatalf("invalid module %s", dir)
		}
		module := fields[1]
		if strings.Contains(string(body), "replace ") {
			t.Fatalf("%s must build without local module replacements", dir)
		}
		err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "bin" || strings.HasPrefix(entry.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, item := range file.Imports {
				imported, err := strconv.Unquote(item.Path.Value)
				if err != nil {
					return err
				}
				if strings.HasPrefix(imported, "qslate/") && imported != module && !strings.HasPrefix(imported, module+"/") {
					t.Errorf("%s imports non-local project code %s", path, imported)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// Copy only source/configuration fixtures, never local credentials or binaries.
func copyBuildInputs(t *testing.T, source, target string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "bin" || entry.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(target, rel), 0755)
		}
		name := entry.Name()
		if name != "go.mod" && name != "go.sum" && !strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".sql") {
			return nil
		}
		if !entry.Type().IsRegular() {
			t.Fatalf("unexpected build-input symlink %s", path)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(target, rel), body, 0644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestModulesBuildOutsideRepository(t *testing.T) {
	for _, dir := range moduleDirectories(t) {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			t.Parallel()
			isolated := t.TempDir()
			copyBuildInputs(t, dir, isolated)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "test", "-mod=readonly", "-tags", "integration", "-run", "^$", "./...")
			cmd.Dir = isolated
			cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("standalone module %s failed: %v\n%s", dir, err, output)
			}
		})
	}
}

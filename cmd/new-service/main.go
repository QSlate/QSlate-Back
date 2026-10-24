package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

func main() {
	name := flag.String("name", "", "service name (lowercase, hyphen-separated)")
	flag.Parse()
	if err := generate(".", *name); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Created services/%s; run go build ./services/%s/...\n", *name, *name)
}

func generate(root, name string) error {
	if !namePattern.MatchString(name) || len(name) > 48 {
		return errors.New("name must contain lowercase letters, digits and single hyphens (max 48 characters)")
	}
	source := filepath.Join(root, "templates/microservice")
	target := filepath.Join(root, "services", name)
	if _, err := os.Stat(source); err != nil {
		return err
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		return errors.New("target already exists or cannot be inspected")
	}
	if err := os.Mkdir(target, 0755); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		out := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(out, 0755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported template entry %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		contents := strings.ReplaceAll(string(data), "qslate/templates/microservice", "qslate/services/"+name)
		contents = strings.ReplaceAll(contents, "./templates/microservice", "./services/"+name)
		contents = strings.ReplaceAll(contents, "microservice-template", name)
		return os.WriteFile(out, []byte(contents), 0644)
	})
}

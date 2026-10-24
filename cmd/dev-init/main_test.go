package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceCredentialUpgradePreservesExistingValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	original := []byte("AUTH_DATABASE_PASSWORD=do-not-rotate\nGATEWAY_PORT=9000")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ensureWorkspaceCredential(path); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	if !bytes.HasPrefix(first, original) || bytes.Count(first, []byte("WORKSPACE_DATABASE_PASSWORD=")) != 1 {
		t.Fatal("existing credentials modified or missing new password")
	}
	if err := ensureWorkspaceCredential(path); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if !bytes.Equal(first, second) {
		t.Fatal("upgrade must be idempotent")
	}
}

func TestWidgetsCredentialUpgradeIsIndependentAndIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	original := []byte("AUTH_DATABASE_PASSWORD=keep-auth\nWORKSPACE_DATABASE_PASSWORD=keep-workspace\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ensureDatabaseCredential(path, "WIDGETS_DATABASE_PASSWORD"); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	if !bytes.HasPrefix(first, original) || bytes.Count(first, []byte("WIDGETS_DATABASE_PASSWORD=")) != 1 {
		t.Fatal("credential upgrade changed existing data")
	}
	if err := ensureDatabaseCredential(path, "WIDGETS_DATABASE_PASSWORD"); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if !bytes.Equal(first, second) {
		t.Fatal("credential was rotated")
	}
}

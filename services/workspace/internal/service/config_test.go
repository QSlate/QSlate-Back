package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testConfig() LayoutConfig {
	c := LayoutConfig{Columns: 12, MaxRows: 100, MaxWidgets: 32}
	c.DefaultWidget.Width, c.DefaultWidget.Height = 6, 4
	return c
}

func TestConfigFileRejectsMalformedOrUnsupportedSettings(t *testing.T) {
	for _, data := range []string{`null`, `{}`, `{"columns":12,"unknown":true}`, `{} {}`, `[{}]`, strings.Repeat(" ", 4097)} {
		path := filepath.Join(t.TempDir(), "layout.json")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil {
			t.Fatal("invalid configuration file accepted")
		}
	}
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("missing configuration file accepted")
	}
}

func TestLayoutConfiguration(t *testing.T) {
	if _, err := LoadConfig("../../config/layout.json"); err != nil {
		t.Fatal(err)
	}
	valid := testConfig()
	if valid.Validate() != nil {
		t.Fatal("valid config rejected")
	}
	for _, change := range []func(*LayoutConfig){
		func(c *LayoutConfig) { c.Columns = 0 }, func(c *LayoutConfig) { c.MaxRows = 10001 },
		func(c *LayoutConfig) { c.MaxWidgets = 129 }, func(c *LayoutConfig) { c.DefaultWidget.Width = 13 },
		func(c *LayoutConfig) { c.DefaultWidget.Height = 0 }, func(c *LayoutConfig) { c.MaxRows = 4 },
	} {
		c := valid
		change(&c)
		if c.Validate() == nil {
			t.Fatal("invalid layout config accepted")
		}
	}
}

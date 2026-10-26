package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
)

// LayoutConfig is supplied explicitly, not inferred from widget catalogue data.
type LayoutConfig struct {
	Columns       int `json:"columns"`
	MaxRows       int `json:"max_rows"`
	MaxWidgets    int `json:"max_widgets"`
	DefaultWidget struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"default_widget"`
}

func (c LayoutConfig) Validate() error {
	if c.Columns < 1 || c.Columns > 100 || c.MaxRows < 1 || c.MaxRows > 10000 || c.MaxWidgets < 1 || c.MaxWidgets > 128 || c.DefaultWidget.Width < 1 || c.DefaultWidget.Width > c.Columns || c.DefaultWidget.Height < 1 || c.DefaultWidget.Height > c.MaxRows || (c.Columns/c.DefaultWidget.Width)*(c.MaxRows/c.DefaultWidget.Height) < c.MaxWidgets {
		return errors.New("invalid workspace layout configuration or insufficient default grid capacity")
	}
	return nil
}

func LoadConfig(path string) (LayoutConfig, error) {
	var cfg LayoutConfig
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if len(data) > 4096 || len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
		return cfg, errors.New("invalid workspace config object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&cfg); err != nil {
		return cfg, err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return cfg, errors.New("expected one workspace config object")
	}
	return cfg, cfg.Validate()
}

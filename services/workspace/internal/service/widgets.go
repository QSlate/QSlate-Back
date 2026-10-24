package service

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"qslate/services/workspace/internal/domain"
)

var referencePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$`)

func configuration(raw json.RawMessage, optional bool) (json.RawMessage, error) {
	if len(raw) == 0 && optional {
		return json.RawMessage(`{}`), nil
	}
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || len(value) > 4096 || value[0] != '{' || !utf8.Valid(value) || !json.Valid(value) {
		return nil, invalid("configuration must be a JSON object of at most 4096 bytes")
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber() // Never round opaque widget configuration numbers to float64.
	var object any
	if err := decoder.Decode(&object); err != nil {
		return nil, invalid("invalid configuration object")
	}
	if !storableJSON(object) {
		return nil, invalid("configuration cannot contain NUL characters or numeric exponents outside -10000..10000")
	}
	var compact bytes.Buffer
	encoder := json.NewEncoder(&compact)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(object); err != nil {
		return nil, invalid("invalid configuration object")
	}
	stored := bytes.TrimSpace(compact.Bytes())
	if len(stored) > 4096 {
		return nil, invalid("normalized configuration exceeds 4096 bytes")
	}
	return json.RawMessage(stored), nil
}

// PostgreSQL jsonb cannot store NUL or unbounded numeric exponents. Normalize
// surrogate escapes through encoding/json before storage, and preserve numbers.
func storableJSON(value any) bool {
	switch v := value.(type) {
	case string:
		return !strings.ContainsRune(v, 0)
	case json.Number:
		if i := strings.IndexAny(string(v), "eE"); i >= 0 {
			exponent, err := strconv.Atoi(string(v)[i+1:])
			return err == nil && exponent >= -10000 && exponent <= 10000
		}
	case []any:
		for _, child := range v {
			if !storableJSON(child) {
				return false
			}
		}
	case map[string]any:
		for key, child := range v {
			if strings.ContainsRune(key, 0) || !storableJSON(child) {
				return false
			}
		}
	}
	return true
}

func (s *Dashboards) Duplicate(ctx context.Context, owner int64, id string) (domain.Dashboard, error) {
	source, err := s.Get(ctx, owner, id)
	if err != nil {
		return domain.Dashboard{}, err
	}
	// The repository returns an independent snapshot. All IDs are regenerated,
	// including layout references; revision and timestamps start afresh on insert.
	source.ID, err = newID()
	if err != nil {
		return domain.Dashboard{}, err
	}
	ids := make(map[string]string, len(source.Widgets))
	for i := range source.Widgets {
		fresh, err := newID()
		if err != nil {
			return domain.Dashboard{}, err
		}
		ids[source.Widgets[i].InstanceID] = fresh
		source.Widgets[i].InstanceID = fresh
	}
	for i := range source.Layout {
		source.Layout[i].InstanceID = ids[source.Layout[i].InstanceID]
	}
	return s.repo.Create(ctx, source)
}

func (s *Dashboards) AddWidget(ctx context.Context, owner int64, id, widgetID, versionID string, raw json.RawMessage) (domain.Widget, error) {
	if err := scope(ctx, owner, id); err != nil {
		return domain.Widget{}, err
	}
	if !referencePattern.MatchString(widgetID) || !referencePattern.MatchString(versionID) {
		return domain.Widget{}, invalid("widget and version references must contain 1-128 identifier characters")
	}
	config, err := configuration(raw, true)
	if err != nil {
		return domain.Widget{}, err
	}
	instanceID, err := newID()
	if err != nil {
		return domain.Widget{}, err
	}
	widget := domain.Widget{InstanceID: instanceID, WidgetID: widgetID, WidgetVersionID: versionID, Configuration: config}
	_, err = s.repo.Mutate(ctx, owner, id, func(d *domain.Dashboard) error {
		if len(d.Widgets) >= s.layout.MaxWidgets {
			return invalid("dashboard widget limit reached")
		}
		position, err := s.place(instanceID, d.Layout)
		if err != nil {
			return err
		}
		d.Widgets = append(d.Widgets, widget)
		d.Layout = append(d.Layout, position)
		return nil
	})
	return widget, err
}

func (s *Dashboards) PatchWidget(ctx context.Context, owner int64, id, instanceID string, raw json.RawMessage) (domain.Widget, error) {
	if err := scope(ctx, owner, id); err != nil {
		return domain.Widget{}, err
	}
	if !idPattern.MatchString(instanceID) {
		return domain.Widget{}, domain.ErrNotFound
	}
	config, err := configuration(raw, false)
	if err != nil {
		return domain.Widget{}, err
	}
	var result domain.Widget
	_, err = s.repo.Mutate(ctx, owner, id, func(d *domain.Dashboard) error {
		for i := range d.Widgets {
			if d.Widgets[i].InstanceID == instanceID {
				d.Widgets[i].Configuration = config
				result = d.Widgets[i]
				return nil
			}
		}
		return domain.ErrNotFound
	})
	return result, err
}

func (s *Dashboards) DeleteWidget(ctx context.Context, owner int64, id, instanceID string) error {
	if err := scope(ctx, owner, id); err != nil {
		return err
	}
	if !idPattern.MatchString(instanceID) {
		return domain.ErrNotFound
	}
	_, err := s.repo.Mutate(ctx, owner, id, func(d *domain.Dashboard) error {
		found := false
		for i := range d.Widgets {
			if d.Widgets[i].InstanceID == instanceID {
				d.Widgets = append(d.Widgets[:i], d.Widgets[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			return domain.ErrNotFound
		}
		for i := range d.Layout {
			if d.Layout[i].InstanceID == instanceID {
				d.Layout = append(d.Layout[:i], d.Layout[i+1:]...)
				break
			}
		}
		return nil
	})
	return err
}

func overlaps(a, b domain.LayoutItem) bool {
	return a.X < b.X+b.Width && a.X+a.Width > b.X && a.Y < b.Y+b.Height && a.Y+a.Height > b.Y
}

func (s *Dashboards) SaveLayout(ctx context.Context, owner int64, id string, revision int64, items []domain.LayoutItem) (domain.Dashboard, error) {
	if err := scope(ctx, owner, id); err != nil {
		return domain.Dashboard{}, err
	}
	if revision < 1 || items == nil || len(items) > s.layout.MaxWidgets {
		return domain.Dashboard{}, invalid("revision and a bounded items array are required")
	}
	return s.repo.Mutate(ctx, owner, id, func(d *domain.Dashboard) error {
		if d.Revision != revision {
			return domain.ErrConflict
		}
		if len(items) != len(d.Widgets) {
			return invalid("layout must contain every widget exactly once")
		}
		remaining := make(map[string]bool, len(d.Widgets))
		for _, widget := range d.Widgets {
			remaining[widget.InstanceID] = true
		}
		for i, item := range items {
			// Subtraction after bounds checks avoids overflowing client-controlled ints.
			if !remaining[item.InstanceID] || item.X < 0 || item.Y < 0 || item.Width < 1 || item.Height < 1 || item.Width > s.layout.Columns || item.Height > s.layout.MaxRows || item.X > s.layout.Columns-item.Width || item.Y > s.layout.MaxRows-item.Height {
				return invalid("layout contains unknown/duplicate instances or out-of-bounds rectangles")
			}
			delete(remaining, item.InstanceID)
			for _, previous := range items[:i] {
				if overlaps(previous, item) {
					return invalid("layout rectangles must not overlap")
				}
			}
		}
		d.Layout = append([]domain.LayoutItem{}, items...)
		return nil
	})
}

// place scans a finite grid in row-major order; occupied rectangles are bounded
// when saved. The settings file controls both placement and reset dimensions.
func (s *Dashboards) place(id string, occupied []domain.LayoutItem) (domain.LayoutItem, error) {
	candidate := domain.LayoutItem{InstanceID: id, Width: s.layout.DefaultWidget.Width, Height: s.layout.DefaultWidget.Height}
	for y := 0; y <= s.layout.MaxRows-candidate.Height; y++ {
		for x := 0; x <= s.layout.Columns-candidate.Width; x++ {
			candidate.X, candidate.Y = x, y
			free := true
			for _, item := range occupied {
				if overlaps(candidate, item) {
					free = false
					break
				}
			}
			if free {
				return candidate, nil
			}
		}
	}
	return domain.LayoutItem{}, invalid("no room for default widget dimensions; rearrange or reset layout")
}

func (s *Dashboards) ResetLayout(ctx context.Context, owner int64, id string) (domain.Dashboard, error) {
	if err := scope(ctx, owner, id); err != nil {
		return domain.Dashboard{}, err
	}
	return s.repo.Mutate(ctx, owner, id, func(d *domain.Dashboard) error {
		if len(d.Widgets) > s.layout.MaxWidgets {
			return invalid("stored widgets exceed configured limit")
		}
		d.Layout = []domain.LayoutItem{}
		for _, widget := range d.Widgets {
			item, err := s.place(widget.InstanceID, d.Layout)
			if err != nil {
				return err
			}
			d.Layout = append(d.Layout, item)
		}
		return nil
	})
}

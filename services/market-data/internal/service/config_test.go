package service

import (
	"qslate/services/market-data/internal/domain"
	"testing"
)

func TestConfigurationMappingAndOrigins(t *testing.T) {
	c, e := LoadConfig("../../config/market-data.json")
	if e != nil {
		t.Fatal(e)
	}
	c.MaxVersions = 0
	if c.Validate() == nil {
		t.Fatal("invalid quota")
	}
	var m domain.Mapping
	m.TimestampColumn = "time"
	m.TimestampUnit = "iso8601"
	m.Timezone = "UTC"
	m.Columns.Open = "o"
	m.Columns.High = "h"
	m.Columns.Low = "l"
	m.Columns.Close = "c"
	cols := []string{"time", "o", "h", "l", "c"}
	if e = ValidateMapping(m, cols); e != nil {
		t.Fatal(e)
	}
	m.Columns.Close = "time"
	if ValidateMapping(m, cols) == nil {
		t.Fatal("duplicate mapping")
	}
	for _, s := range []string{"http://user:pass@example.org", "http://example.org/path", "http://example.org?x=1", "http://example.org"} {
		if _, e = Origin(s, false); e == nil {
			t.Fatal(s)
		}
	}
	if !ValidKey("validation-request-0001") || ValidKey("short") {
		t.Fatal("idempotency validation")
	}
}

package service

import (
	"testing"
	"time"
)

func TestPublicCatalogueExposesFrozenHourlyAndDailyViews(t *testing.T) {
	cfg, err := LoadConfig("../../config/market-data.json")
	if err != nil {
		t.Fatal(err)
	}
	public, err := LoadPublic(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if symbols := public.Symbols(); len(symbols) != 6 {
		t.Fatalf("got %d public symbols", len(symbols))
	}
	hourly, found, err := public.Snapshot("public-btc-usdt-1h", "2024-03-19-v1")
	if err != nil || !found || hourly.RowCount != 8760 || hourly.SHA256 == "" {
		t.Fatalf("hourly snapshot: %+v %v", hourly, err)
	}
	daily, found, err := public.Snapshot("public-btc-usdt-1d", "2024-03-19-v1")
	if err != nil || !found || daily.RowCount != 365 || daily.SHA256 == hourly.SHA256 {
		t.Fatalf("daily snapshot: %+v %v", daily, err)
	}
	start := time.Date(2024, 3, 20, 0, 0, 0, 0, time.UTC)
	history, found, err := public.History("BTC/USDT@1h", "", "1h", start, start.Add(4*time.Hour), 2, nil)
	if err != nil || !found || len(history.Items) != 2 || history.NextCursor == nil || history.Items[0].Time != start {
		t.Fatalf("history: %+v %v", history, err)
	}
	cursor, _ := time.Parse(time.RFC3339Nano, *history.NextCursor)
	next, _, err := public.History("BTC/USDT@1h", "2024-03-19-v1", "1h", start, start.Add(4*time.Hour), 2, &cursor)
	if err != nil || len(next.Items) != 2 || !next.Items[0].Time.After(cursor) {
		t.Fatalf("next page: %+v %v", next, err)
	}
}

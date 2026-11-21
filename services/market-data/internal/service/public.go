package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"io"
	"math/big"
	"os"
	"qslate/services/market-data/internal/domain"
	"sort"
	"strings"
	"time"
)

type publicEntry struct {
	config PublicDataset
	rows   []domain.Candle
	csv    string
	hash   string
}

type PublicCatalogue struct {
	byID     map[string]publicEntry
	bySymbol map[string]publicEntry
}

func LoadPublic(c Config) (*PublicCatalogue, error) {
	root, err := os.OpenRoot(c.PublicDataRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	parsed := map[string][]domain.Candle{}
	out := &PublicCatalogue{byID: map[string]publicEntry{}, bySymbol: map[string]publicEntry{}}
	for _, cfg := range c.PublicDatasets {
		rows, found := parsed[cfg.File]
		if !found {
			f, openErr := root.Open(cfg.File)
			if openErr != nil {
				return nil, openErr
			}
			raw, readErr := io.ReadAll(io.LimitReader(f, int64(c.MaxSnapshotBytes)+1))
			closeErr := f.Close()
			if err = errors.Join(readErr, closeErr); err != nil || len(raw) > c.MaxSnapshotBytes {
				return nil, errors.New("public dataset exceeds configured snapshot limit")
			}
			sum := sha256.Sum256(raw)
			if hex.EncodeToString(sum[:]) != cfg.SHA256 {
				return nil, errors.New("public dataset checksum mismatch")
			}
			rows, err = parsePublicCSV(raw, c.MaxRows)
			if err != nil {
				return nil, err
			}
			parsed[cfg.File] = rows
		}
		view := rows
		if cfg.Resolution == "1d" {
			view = daily(rows)
		}
		body, err := canonicalCSV(view)
		if err != nil || len(body) > c.MaxSnapshotBytes {
			return nil, errors.New("invalid public dataset snapshot")
		}
		sum := sha256.Sum256([]byte(body))
		entry := publicEntry{config: cfg, rows: view, csv: body, hash: hex.EncodeToString(sum[:])}
		out.byID[cfg.ID], out.bySymbol[cfg.Symbol] = entry, entry
	}
	return out, nil
}

func amount(value string, positive bool) (*big.Rat, bool) {
	v, ok := new(big.Rat).SetString(value)
	return v, ok && (v.Sign() > 0 || (!positive && v.Sign() == 0))
}

func parsePublicCSV(raw []byte, maxRows int) ([]domain.Candle, error) {
	r := csv.NewReader(bytes.NewReader(raw))
	header, err := r.Read()
	if err != nil || strings.Join(header, ",") != "Datetime,Open,High,Low,Close,Volume" {
		return nil, errors.New("invalid public dataset header")
	}
	rows := []domain.Candle{}
	var previous time.Time
	for {
		row, readErr := r.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil || len(row) != 6 || len(rows) >= maxRows {
			return nil, errors.New("invalid public dataset row")
		}
		ts, parseErr := time.Parse(time.RFC3339Nano, row[0])
		o, okO := amount(row[1], true)
		h, okH := amount(row[2], true)
		l, okL := amount(row[3], true)
		c, okC := amount(row[4], true)
		_, okV := amount(row[5], false)
		if parseErr != nil || !ts.Equal(ts.UTC()) || (!previous.IsZero() && !ts.After(previous)) || !okO || !okH || !okL || !okC || !okV || h.Cmp(o) < 0 || h.Cmp(c) < 0 || l.Cmp(o) > 0 || l.Cmp(c) > 0 {
			return nil, errors.New("invalid public OHLCV row")
		}
		previous = ts
		rows = append(rows, domain.Candle{Time: ts, Open: row[1], High: row[2], Low: row[3], Close: row[4], Volume: row[5]})
	}
	if len(rows) < 2 {
		return nil, errors.New("public dataset is empty")
	}
	return rows, nil
}

func decimal(v *big.Rat) string {
	s := strings.TrimRight(strings.TrimRight(v.FloatString(18), "0"), ".")
	if s == "" {
		return "0"
	}
	return s
}

func daily(rows []domain.Candle) []domain.Candle {
	out := []domain.Candle{}
	for _, row := range rows {
		day := row.Time.UTC().Truncate(24 * time.Hour)
		if len(out) == 0 || !out[len(out)-1].Time.Equal(day) {
			row.Time = day
			out = append(out, row)
			continue
		}
		current := &out[len(out)-1]
		high, _ := amount(current.High, true)
		candidateHigh, _ := amount(row.High, true)
		if candidateHigh.Cmp(high) > 0 {
			current.High = row.High
		}
		low, _ := amount(current.Low, true)
		candidateLow, _ := amount(row.Low, true)
		if candidateLow.Cmp(low) < 0 {
			current.Low = row.Low
		}
		current.Close = row.Close
		volume, _ := amount(current.Volume, false)
		addition, _ := amount(row.Volume, false)
		current.Volume = decimal(volume.Add(volume, addition))
	}
	return out
}

func canonicalCSV(rows []domain.Candle) (string, error) {
	var body bytes.Buffer
	w := csv.NewWriter(&body)
	if err := w.Write([]string{"Datetime", "Open", "High", "Low", "Close", "Volume"}); err != nil {
		return "", err
	}
	for _, row := range rows {
		if err := w.Write([]string{row.Time.UTC().Format(time.RFC3339Nano), row.Open, row.High, row.Low, row.Close, row.Volume}); err != nil {
			return "", err
		}
	}
	w.Flush()
	return body.String(), w.Error()
}

func (p *PublicCatalogue) Symbols() []domain.Symbol {
	out := make([]domain.Symbol, 0, len(p.byID))
	for _, entry := range p.byID {
		out = append(out, domain.Symbol{Symbol: entry.config.Symbol, Name: entry.config.Name, DatasetID: entry.config.ID, VersionID: entry.config.VersionID, Resolution: entry.config.Resolution})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return out
}

func (p *PublicCatalogue) Snapshot(id, version string) (domain.Snapshot, bool, error) {
	entry, found := p.byID[id]
	if !found {
		return domain.Snapshot{}, false, nil
	}
	if version != entry.config.VersionID {
		return domain.Snapshot{}, true, domain.ErrNotFound
	}
	return domain.Snapshot{DatasetID: id, VersionID: version, CSV: entry.csv, SHA256: entry.hash, RowCount: len(entry.rows)}, true, nil
}

func (p *PublicCatalogue) History(symbol, version, resolution string, start, end time.Time, limit int, cursor *time.Time) (domain.History, bool, error) {
	entry, found := p.bySymbol[symbol]
	out := domain.History{Symbol: symbol, Resolution: resolution, Items: []domain.Candle{}}
	if !found {
		return out, false, nil
	}
	if resolution != entry.config.Resolution || (version != "" && version != entry.config.VersionID) {
		return out, true, domain.ErrNotFound
	}
	for _, row := range entry.rows {
		if row.Time.Before(start) || !row.Time.Before(end) || (cursor != nil && !row.Time.After(*cursor)) {
			continue
		}
		out.Items = append(out.Items, row)
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			next := out.Items[limit-1].Time.UTC().Format(time.RFC3339Nano)
			out.NextCursor = &next
			break
		}
	}
	return out, true, nil
}

func (d *Data) Symbols(ctx context.Context, owner int64) ([]domain.Symbol, error) {
	items, err := d.Repo.Symbols(ctx, owner)
	if err != nil {
		return nil, err
	}
	return append(items, d.public.Symbols()...), nil
}

func (d *Data) Snapshot(ctx context.Context, owner int64, id, version string) (domain.Snapshot, error) {
	if out, found, err := d.public.Snapshot(id, version); found {
		return out, err
	}
	return d.Repo.Snapshot(ctx, owner, id, version)
}

func (d *Data) PublicHistory(symbol, version, resolution string, start, end time.Time, limit int, cursor *time.Time) (domain.History, bool, error) {
	return d.public.History(symbol, version, resolution, start, end, limit, cursor)
}

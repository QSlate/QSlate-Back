package service

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"io"
	"math"
	"os"
	"qslate/services/lab/internal/domain"
	"strconv"
	"strings"
	"time"
)

func (c Config) Dataset(owner int64, id, version string, window int) (string, string, error) {
	var entry *Dataset
	for i := range c.Datasets {
		d := &c.Datasets[i]
		if d.ID == id && d.VersionID == version && (d.OwnerID == 0 || d.OwnerID == owner) {
			entry = d
			break
		}
	}
	if entry == nil {
		return "", "", domain.ErrNotFound
	}
	root, e := os.OpenRoot(c.DataRoot)
	if e != nil {
		return "", "", domain.ErrUnavailable
	}
	defer root.Close()
	f, e := root.Open(entry.File)
	if e != nil {
		return "", "", domain.ErrUnavailable
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, c.MaxDatasetBytes+1))
	if e != nil || int64(len(b)) > c.MaxDatasetBytes {
		return "", "", domain.ErrUnavailable
	}
	h := sha256.Sum256(b)
	sum := hex.EncodeToString(h[:])
	if !strings.EqualFold(sum, entry.SHA256) {
		return "", "", domain.ErrUnavailable
	}
	if e = validateCSV(string(b), window, c.MaxDatasetRows); e != nil {
		return "", "", e
	}
	return string(b), sum, nil
}
func validateCSV(data string, window, maxRows int) error {
	r := csv.NewReader(strings.NewReader(data))
	header, e := r.Read()
	if e != nil || len(header) < 5 || len(header) > 6 {
		return domain.ErrInvalid
	}
	expected := []string{"open", "high", "low", "close", "volume"}
	for i := 1; i < len(header); i++ {
		if strings.ToLower(header[i]) != expected[i-1] {
			return domain.ErrInvalid
		}
	}
	count := 0
	var last time.Time
	for {
		row, e := r.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return domain.ErrInvalid
		}
		ts, e := time.Parse(time.RFC3339, row[0])
		if e != nil || (!last.IsZero() && !ts.After(last)) {
			return domain.ErrInvalid
		}
		last = ts
		prices := make([]float64, len(row)-1)
		for i := 1; i < len(row); i++ {
			v, e := strconv.ParseFloat(row[i], 64)
			if e != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || (i < 5 && v == 0) {
				return domain.ErrInvalid
			}
			prices[i-1] = v
		}
		if prices[1] < math.Max(prices[0], prices[3]) || prices[2] > math.Min(prices[0], prices[3]) {
			return domain.ErrInvalid
		}
		count++
		if count > maxRows {
			return domain.ErrInvalid
		}
	}
	if count <= window {
		return domain.ErrInvalid
	}
	return nil
}

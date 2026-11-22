// Command public-data creates the versioned beginner data snapshots committed
// under services/market-data/data. It is a maintainer tool, not a runtime fetcher.
package main

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

var (
	start = time.Date(2024, 3, 19, 0, 0, 0, 0, time.UTC)
	end   = time.Date(2025, 3, 19, 0, 0, 0, 0, time.UTC)
)

func main() {
	aapl := flag.String("aapl-source", "", "legacy DATA_1H_AAPL.csv source")
	out := flag.String("out", "services/market-data/data", "output directory")
	flag.Parse()
	if *aapl == "" {
		fatal(errors.New("-aapl-source is required"))
	}
	if err := os.MkdirAll(*out, 0755); err != nil {
		fatal(err)
	}
	if err := writeBinance(filepath.Join(*out, "btc-usdt-1h.csv"), "BTCUSDT"); err != nil {
		fatal(err)
	}
	if err := writeBinance(filepath.Join(*out, "eth-usdt-1h.csv"), "ETHUSDT"); err != nil {
		fatal(err)
	}
	if err := writeAAPL(filepath.Join(*out, "aapl-1h.csv"), *aapl); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func output(path string) (*os.File, *csv.Writer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return nil, nil, err
	}
	w := csv.NewWriter(f)
	if err = w.Write([]string{"Datetime", "Open", "High", "Low", "Close", "Volume"}); err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, w, nil
}

func finish(f *os.File, w *csv.Writer) error {
	w.Flush()
	return errors.Join(w.Error(), f.Close())
}

func writeBinance(path, symbol string) error {
	f, w, err := output(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	client := &http.Client{Timeout: 15 * time.Second}
	next := start
	for next.Before(end) {
		u := "https://data-api.binance.vision/api/v3/klines?symbol=" + symbol + "&interval=1h&limit=1000&startTime=" + strconv.FormatInt(next.UnixMilli(), 10) + "&endTime=" + strconv.FormatInt(end.Add(-time.Millisecond).UnixMilli(), 10)
		resp, requestErr := client.Get(u)
		if requestErr != nil {
			return requestErr
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return fmt.Errorf("%s returned %s", symbol, resp.Status)
		}
		var rows [][]json.RawMessage
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rows)
		closeErr := resp.Body.Close()
		if err = errors.Join(decodeErr, closeErr); err != nil {
			return err
		}
		if len(rows) == 0 {
			return fmt.Errorf("%s history ended at %s", symbol, next)
		}
		for _, row := range rows {
			if len(row) < 6 {
				return errors.New("invalid Binance kline")
			}
			var millis int64
			var values [5]string
			if json.Unmarshal(row[0], &millis) != nil {
				return errors.New("invalid Binance timestamp")
			}
			for i := range values {
				if json.Unmarshal(row[i+1], &values[i]) != nil {
					return errors.New("invalid Binance amount")
				}
			}
			ts := time.UnixMilli(millis).UTC()
			if ts.Before(start) || !ts.Before(end) {
				continue
			}
			if err = w.Write([]string{ts.Format(time.RFC3339), values[0], values[1], values[2], values[3], values[4]}); err != nil {
				return err
			}
			next = ts.Add(time.Hour)
		}
		if len(rows) < 1000 {
			break
		}
	}
	return finish(f, w)
}

func writeAAPL(path, source string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	r := csv.NewReader(in)
	for i := 0; i < 3; i++ {
		if _, err = r.Read(); err != nil {
			return err
		}
	}
	f, w, err := output(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	for {
		row, readErr := r.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil || len(row) != 6 {
			return errors.New("invalid legacy AAPL row")
		}
		ts, parseErr := time.ParseInLocation("2006-01-02 15:04:05", row[0], time.UTC)
		if parseErr != nil {
			return parseErr
		}
		if ts.Before(start) || !ts.Before(end) {
			continue
		}
		// Legacy columns are timestamp, close, high, low, open, volume.
		if err = w.Write([]string{ts.Format(time.RFC3339), row[4], row[2], row[3], row[1], row[5]}); err != nil {
			return err
		}
	}
	return finish(f, w)
}

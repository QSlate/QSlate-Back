# Public beginner datasets

These immutable snapshots let a new account run a first backtest without an
upload. They cover `[2024-03-19T00:00:00Z, 2025-03-19T00:00:00Z)` at one-hour
resolution. Market Data derives the one-day views from the same frozen candles.

- `btc-usdt-1h.csv` and `eth-usdt-1h.csv` come from Binance's public spot kline
  endpoint at `data-api.binance.vision`; no account or API key was used.
- `aapl-1h.csv` is a normalized copy of the pre-existing legacy backend fixture
  `DATA_1H_AAPL.csv`. Its original provider was not recorded, so that provenance
  limitation must remain visible to users of the demonstration dataset.

Run `go run ./cmd/public-data -aapl-source /path/to/DATA_1H_AAPL.csv` only when
deliberately refreshing the versioned snapshots. Review the date range and update
the configured version IDs and SHA-256 values whenever bytes change. Runtime
services never download or silently refresh these files.

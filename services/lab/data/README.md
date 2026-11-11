# Operator-managed datasets

Mount versioned, read-only OHLC[V] CSV files here. Files are ignored by Git.
Register their ID, immutable version, relative file and SHA-256 in
[Lab configuration](../config/lab.json).
CSV columns: `Datetime,Open,High,Low,Close[,Volume]`; ascending, unique RFC3339
timestamps, finite positive prices and non-negative volume. `owner_id: 0` means
shared; otherwise only that Auth user can submit the dataset. The API confines
file reads to this directory and freezes verified bytes before enqueueing.

No sample financial data is bundled or downloaded automatically. This is a
temporary dataset adapter, not an implementation of section 8 Market Data.

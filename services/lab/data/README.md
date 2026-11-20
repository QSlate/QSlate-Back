# Operator-managed datasets

This optional adapter is used only when `MARKET_DATA_SERVICE_URL` is unset.
The default Compose stack resolves ready owned versions through Market Data.
For standalone Lab, mount versioned, read-only OHLC[V] CSV files here. Files are ignored by Git.
Register their ID, immutable version, relative file and SHA-256 in
[Lab configuration](../config/lab.json).
CSV columns: `Datetime,Open,High,Low,Close[,Volume]`; ascending, unique RFC3339
timestamps, finite positive prices and non-negative volume. `owner_id: 0` means
shared; otherwise only that Auth user can submit the dataset. The API confines
file reads to this directory and freezes verified bytes before enqueueing.

No sample financial data is bundled or downloaded automatically. This is a
standalone file adapter; section 8 imports belong to the separate Market Data service.

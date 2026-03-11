# QSlate-Back
QSlate backend infrastructure. Built in Go , it orchestrates secure, ephemeral Docker containers to execute Python trading scripts. Leveraging TimescaleDB for time-series data , it powers a high-fidelity backtesting engine , an AI-assisted strategy builder , and live market execution.

The backtest flow is split into two services:

- `main.py`: public API layer
- `runner_service.py`: dedicated backtest runner

When the frontend calls `POST /api/backtest`, the API forwards the request to the runner through `RUNNER_SERVICE_URL`. The runner executes the backtest job and returns the final `{ report, trades }` payload.

## Endpoints

API service (`main.py`):

- `GET /api/options`
- `GET /api/data/{ticker}?limit=100`
- `GET /api/assets`
- `POST /api/assets/download/{ticker}`
- `POST /api/backtest`

Runner service (`runner_service.py`):

- `GET /healthz`
- `POST /backtest/run`

## Requirements

Install the direct project dependencies with:

```bash
pip install -r requirements.txt
```

## Environment variables

API:

- `RUNNER_SERVICE_URL` default: `http://localhost:8090/backtest/run`
- `RUNNER_TIMEOUT_SECONDS` default: `600`
- `API_HOST` default: `127.0.0.1`
- `API_PORT` default: `8000`

Runner:

- `RUNNER_HOST` default: `127.0.0.1`
- `RUNNER_PORT` default: `8090`

## Local run

From the project root:

```bash
cd QSlate-Back
source venv/bin/activate
```

Start the runner in terminal A:

```bash
python runner_service.py
```

Start the API in terminal B:

```bash
cd QSlate-Back
source venv/bin/activate
RUNNER_SERVICE_URL=http://127.0.0.1:8090/backtest/run python main.py
```

## Manual test

Check that the API is up:

```bash
curl http://127.0.0.1:8000/api/options
```

If you do not already have local data for a ticker, download some:

```bash
curl -X POST http://127.0.0.1:8000/api/assets/download/AAPL
```

Check the local candles:

```bash
curl "http://127.0.0.1:8000/api/data/AAPL?limit=3"
```

Run a minimal backtest:

```bash
curl -X POST http://127.0.0.1:8000/api/backtest \
  -H "Content-Type: application/json" \
  -d '{"ticker":"AAPL","initial_capital":10000,"window":5,"indicators":[],"strategy_code":"def custom_strategy(history, open_trades, remaining_capital):\n    return []","strategy_function_name":"custom_strategy","requested_stats":null,"sort_trades_by":"date","top_trades":null}'
```

Expected result for this empty strategy:

```json
{"report":{"Message":"No trades to analyze."},"trades":[]}
```

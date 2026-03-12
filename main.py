import glob
import json
import os
import urllib.error
import urllib.request

import pandas as pd
import yfinance as yf
from fastapi import FastAPI, HTTPException
from fastapi.middleware.cors import CORSMiddleware

import backtest
from models import BacktestRequest

app = FastAPI(
    title="Backtest API Service",
    docs_url="/api/docs",
    redoc_url="/api/redoc",
    openapi_url="/api/openapi.json",
)

RUNNER_SERVICE_URL = os.getenv("RUNNER_SERVICE_URL", "http://localhost:8090/backtest/run")
RUNNER_TIMEOUT_SECONDS = float(os.getenv("RUNNER_TIMEOUT_SECONDS", "600"))

# Setup CORS to allow requests from any frontend
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=False,
    allow_methods=["*"],
    allow_headers=["*"],
)


@app.get("/api/data/{ticker}")
def get_historical_data(ticker: str, limit: int = 100):
    """
    Get the last `limit` rows of data for a specific ticker from the local dataset.
    """
    file_name = f"DATA_1H_{ticker}.csv"
    if not os.path.exists(file_name):
        raise HTTPException(status_code=404, detail=f"Data for ticker {ticker} not found. Please run the data fetcher first.")

    try:
        df = pd.read_csv(file_name)
        if len(df) > limit:
            df = df.tail(limit)

        # The CSV reader used by the backtest engine treats the first column as the time axis,
        # regardless of its header. Keep the API route aligned with that contract.
        df = df.rename(
            columns={
                df.columns[0]: "time",
                "Close": "close",
                "High": "high",
                "Low": "low",
                "Open": "open",
                "Volume": "volume",
            }
        )
        df["time"] = pd.to_datetime(df["time"], format="mixed", errors="coerce", utc=True)
        df = df.dropna(subset=["time"])
        df["time"] = df["time"].astype(str)

        # Convert to dictionary for JSON response
        return df.to_dict(orient="records")
    except Exception as exc:
        raise HTTPException(status_code=500, detail=str(exc)) from exc


@app.get("/api/options")
def get_options():
    """
    Get available indicators, stats, and sorting options for backtesting.
    """
    return {
        "indicators": list(backtest.INDICATOR_REGISTRY.keys()),
        "stats": list(backtest.STATS_REGISTRY.keys()),
        "sort_trades_by": ["date", "pnl_high_to_low", "pnl_low_to_high"],
    }


ASSET_INFO_CACHE = {}


@app.get("/api/assets")
def get_assets():
    """
    Get available assets/tickers for the platform by reading downloaded local CSVs.
    """
    files = glob.glob("DATA_1H_*.csv")
    tickers = [f.replace("DATA_1H_", "").replace(".csv", "") for f in files]

    results = []
    for ticker in tickers:
        if ticker not in ASSET_INFO_CACHE:
            try:
                t = yf.Ticker(ticker)
                info = t.info
                quote_type = info.get("quoteType", "").lower()

                # Map yfinance quoteType to stock/crypto
                if quote_type == "equity":
                    asset_type = "stock"
                elif quote_type == "cryptocurrency":
                    asset_type = "crypto"
                else:
                    asset_type = quote_type

                ASSET_INFO_CACHE[ticker] = {
                    "symbol": ticker,
                    "name": info.get("shortName", ticker),
                    "exchange": info.get("exchange", "Unknown"),
                    "type": asset_type,
                }
            except Exception:
                # Fallback if yfinance fetch fails or is missing info
                ASSET_INFO_CACHE[ticker] = {
                    "symbol": ticker,
                    "name": ticker,
                    "exchange": "Unknown",
                    "type": "unknown",
                }
        results.append(ASSET_INFO_CACHE[ticker])

    return results


@app.post("/api/assets/download/{ticker}")
def download_asset(ticker: str):
    """
    Download hourly historical data for a specific ticker from yfinance
    and save it locally as a CSV file to make it available for backtesting.
    """
    ticker = ticker.upper()
    try:
        backtest.fetch_hourly_data(ticker)
    except Exception as exc:
        raise HTTPException(status_code=500, detail=str(exc)) from exc

    file_name = f"DATA_1H_{ticker}.csv"
    if not os.path.exists(file_name):
        raise HTTPException(status_code=404, detail=f"Failed to download data for {ticker}. Check if the ticker is valid.")

    return {"message": f"Successfully downloaded and saved data for {ticker}", "ticker": ticker}


def _request_payload(req: BacktestRequest) -> dict:
    if hasattr(req, "model_dump"):
        return req.model_dump()
    return req.dict()


def _run_backtest_via_runner(req: BacktestRequest) -> dict:
    payload = json.dumps(_request_payload(req)).encode("utf-8")
    request = urllib.request.Request(
        RUNNER_SERVICE_URL,
        data=payload,
        method="POST",
        headers={"Content-Type": "application/json", "Accept": "application/json"},
    )

    try:
        with urllib.request.urlopen(request, timeout=RUNNER_TIMEOUT_SECONDS) as response:
            response_body = response.read()
    except urllib.error.HTTPError as exc:
        raw_error = exc.read().decode("utf-8", errors="ignore")
        detail = f"Runner service returned HTTP {exc.code}."
        if raw_error:
            try:
                parsed = json.loads(raw_error)
                if isinstance(parsed, dict) and parsed.get("detail"):
                    detail = f"Runner service error: {parsed['detail']}"
                else:
                    detail = f"Runner service error: {raw_error}"
            except json.JSONDecodeError:
                detail = f"Runner service error: {raw_error}"
        raise HTTPException(status_code=exc.code, detail=detail) from exc
    except urllib.error.URLError as exc:
        if isinstance(exc.reason, TimeoutError):
            raise HTTPException(status_code=504, detail="Runner service timeout.") from exc
        raise HTTPException(
            status_code=502,
            detail=f"Runner service is unreachable at {RUNNER_SERVICE_URL}: {exc.reason}",
        ) from exc

    if not response_body:
        return {"report": {}, "trades": []}

    try:
        parsed_payload = json.loads(response_body.decode("utf-8"))
    except json.JSONDecodeError as exc:
        raise HTTPException(status_code=502, detail=f"Runner service returned invalid JSON: {exc}") from exc

    if not isinstance(parsed_payload, dict) or "report" not in parsed_payload or "trades" not in parsed_payload:
        raise HTTPException(status_code=502, detail="Runner service returned an unexpected payload.")

    return parsed_payload


@app.post("/api/backtest")
def run_custom_backtest(req: BacktestRequest):
    return _run_backtest_via_runner(req)


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(
        "main:app",
        host=os.getenv("API_HOST", "127.0.0.1"),
        port=int(os.getenv("API_PORT", "8000")),
        reload=False,
    )

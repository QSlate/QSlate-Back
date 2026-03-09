from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field
import pandas as pd
from typing import List, Optional
import backtest
import os
import glob
import yfinance as yf
from fastapi.middleware.cors import CORSMiddleware

app = FastAPI(title="Backtest API Service")

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
        
        df = df.rename(columns={
            "Price": "time",
            "Close": "close",
            "High": "high",
            "Low": "low",
            "Open": "open",
            "Volume": "volume"
        })
        
        # Convert to dictionary for JSON response
        return df.to_dict(orient="records")
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


@app.get("/api/options")
def get_options():
    """
    Get available indicators, stats, and sorting options for backtesting.
    """
    return {
        "indicators": list(backtest.INDICATOR_REGISTRY.keys()),
        "stats": list(backtest.STATS_REGISTRY.keys()),
        "sort_trades_by": ["date", "pnl_high_to_low", "pnl_low_to_high"]
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
                    "type": asset_type
                }
            except Exception as e:
                # Fallback if yfinance fetch fails or is missing info
                ASSET_INFO_CACHE[ticker] = {
                    "symbol": ticker,
                    "name": ticker,
                    "exchange": "Unknown",
                    "type": "unknown"
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
        # Call the existing fetch method from backtest.py
        backtest.fetch_hourly_data(ticker)
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))
        
    # Verify the file was actually created (fetch_hourly_data prints errors instead of throwing them if data is empty)
    file_name = f"DATA_1H_{ticker}.csv"
    if not os.path.exists(file_name):
        raise HTTPException(status_code=404, detail=f"Failed to download data for {ticker}. Check if the ticker is valid.")
        
    return {"message": f"Successfully downloaded and saved data for {ticker}", "ticker": ticker}

class BacktestRequest(BaseModel):
    ticker: str = Field(..., description="The asset ticker to backtest (e.g., AAPL)")
    initial_capital: float = Field(10000.0, description="The starting balance.")
    window: int = Field(10, description="The number of historical rows passed to the strategy at each step.")
    indicators: Optional[List[str]] = Field([], description="List of technical indicators to calculate. Hit GET /api/options to see available ones.")
    strategy_code: str = Field(..., description="The raw Python code string containing the strategy function.")
    strategy_function_name: str = Field("custom_strategy", description="Must exactly match the name of the function inside the strategy_code.")
    requested_stats: Optional[List[str]] = Field(None, description="Exact names of the statistics to evaluate. Hit GET /api/options to see available ones. 'None' means all.")
    custom_stats_code: Optional[str] = Field(None, description="Raw Python code containing custom statistical format functions def stat(df, init_cap).")
    custom_stats_names: Optional[List[str]] = Field(None, description="List of function names from 'custom_stats_code' to evaluate.")
    sort_trades_by: Optional[str] = Field("date", description="How to order output trades: 'date', 'pnl_high_to_low', or 'pnl_low_to_high'.", pattern="^(date|pnl_high_to_low|pnl_low_to_high)$")
    top_trades: Optional[int] = Field(None, description="Limit the total number of trades returned. 'None' means all trades.", ge=1)



@app.post("/api/backtest")
def run_custom_backtest(req: BacktestRequest):
    """
    Launch a backtest with a custom strategy sent by the frontend.
    The strategy code must contain a function with the signature:
    def custom_strategy(history, open_trades, remaining_capital):
    """
    file_name = f"DATA_1H_{req.ticker}.csv"
    if not os.path.exists(file_name):
        raise HTTPException(status_code=404, detail=f"Data for ticker {req.ticker} not found.")

    # 1. Compile and extract the dynamic strategy function
    local_env = {}
    try:
        exec(req.strategy_code, {}, local_env)
    except Exception as e:
        raise HTTPException(status_code=400, detail=f"Error compiling strategy code: {str(e)}")
    
    if req.strategy_function_name not in local_env:
        raise HTTPException(status_code=400, detail=f"Strategy function '{req.strategy_function_name}' not found in the provided code.")
    
    strategy_func = local_env[req.strategy_function_name]
    
    if not callable(strategy_func):
        raise HTTPException(status_code=400, detail=f"'{req.strategy_function_name}' is not callable.")

    # 2. Run the backtest
    try:
        df_history = backtest.run_backtest(
            csv_file=file_name,
            strategy_function=strategy_func,
            initial_capital=req.initial_capital,
            window=req.window,
            requested_indicators=req.indicators
        )
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Error during backtest execution: {str(e)}")

    # 3. Generate Report
    custom_stats_dict = {}
    if req.custom_stats_code and req.custom_stats_names:
        stats_env = {}
        try:
            exec(req.custom_stats_code, {}, stats_env)
            for s_name in req.custom_stats_names:
                if s_name in stats_env and callable(stats_env[s_name]):
                    custom_stats_dict[s_name] = stats_env[s_name]
                else:
                    raise HTTPException(status_code=400, detail=f"Custom stat '{s_name}' not found or is not callable in code.")
        except HTTPException:
            raise
        except Exception as e:
            raise HTTPException(status_code=400, detail=f"Error compiling custom stats code: {str(e)}")

    try:
        # If requested_stats is passed, it uses that; otherwise backtest.generate_report defaults to all of them
        report = backtest.generate_report(
            df_history, 
            initial_capital=req.initial_capital, 
            requested_stats=req.requested_stats,
            custom_stats=custom_stats_dict if custom_stats_dict else None
        )
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Error generating report: {str(e)}")
    
    # 4. Format Trade History output
    if not df_history.empty:
        # Filter and sort trades based on client request
        if req.sort_trades_by == "pnl_high_to_low":
            df_history = df_history.sort_values(by="pnl_usd", ascending=False)
        elif req.sort_trades_by == "pnl_low_to_high":
            df_history = df_history.sort_values(by="pnl_usd", ascending=True)
        # default is "date", which they are already sorted by
            
        if req.top_trades is not None and req.top_trades > 0:
            df_history = df_history.head(req.top_trades)

        # Convert date objects to strings to serialize them safely
        df_history['entry_date'] = df_history['entry_date'].astype(str)
        df_history['exit_date'] = df_history['exit_date'].astype(str)
        trades = df_history.to_dict(orient="records")
    else:
        trades = []

    # Clean the report formatting for JSON
    report_dict = {}
    if isinstance(report, pd.Series):
        report_dict = report.to_dict()
    elif isinstance(report, str):
        report_dict = {"message": report}

    return {
        "report": report_dict,
        "trades": trades
    }

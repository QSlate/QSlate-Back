import os
from typing import Optional
import pandas as pd


def load_market_data(
    file_path: str,
    start_date: Optional[str] = None,
    end_date: Optional[str] = None,
) -> pd.DataFrame:
    """Loads and standardizes OHLCV market data from a CSV file.

    Ensures standard capitalized column names: Datetime, Open, High, Low, Close, Volume.
    """
    if not os.path.exists(file_path):
        raise FileNotFoundError(f"Market data file not found: {file_path}")

    df = pd.read_csv(file_path)

    # First column is assumed to be Datetime if not named
    first_col = df.columns[0]
    if first_col not in ["Datetime", "datetime", "Date", "date", "Time", "time"]:
        df.rename(columns={first_col: "Datetime"}, inplace=True)
    else:
        df.rename(columns={first_col: "Datetime"}, inplace=True)

    df["Datetime"] = pd.to_datetime(df["Datetime"], format="mixed", errors="coerce", utc=True)
    df = df.dropna(subset=["Datetime"]).set_index("Datetime")

    # Map column names case-insensitively to standard OHLCV
    col_map = {}
    for col in df.columns:
        c_lower = col.lower()
        if c_lower == "open":
            col_map[col] = "Open"
        elif c_lower == "high":
            col_map[col] = "High"
        elif c_lower == "low":
            col_map[col] = "Low"
        elif c_lower == "close":
            col_map[col] = "Close"
        elif c_lower == "volume":
            col_map[col] = "Volume"

    df.rename(columns=col_map, inplace=True)

    required = ["Open", "High", "Low", "Close"]
    for req in required:
        if req not in df.columns:
            raise ValueError(f"Missing required price column: {req}")
        df[req] = pd.to_numeric(df[req], errors="coerce")

    if "Volume" not in df.columns:
        df["Volume"] = 0.0
    else:
        df["Volume"] = pd.to_numeric(df["Volume"], errors="coerce").fillna(0.0)

    df = df.dropna(subset=required).sort_index()

    if start_date:
        df = df[df.index >= pd.to_datetime(start_date, utc=True)]
    if end_date:
        df = df[df.index <= pd.to_datetime(end_date, utc=True)]

    if df.empty:
        raise ValueError("Market data is empty after filtering dates.")

    return df

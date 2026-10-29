import pandas as pd


def calculate(data: pd.DataFrame, period: int = 10, multiplier: float = 2.0) -> pd.Series:
    """SuperTrend indicator implementation."""
    atr = (data["High"] - data["Low"]).rolling(period).mean()
    hl2 = (data["High"] + data["Low"]) / 2.0
    return hl2 + (multiplier * atr)

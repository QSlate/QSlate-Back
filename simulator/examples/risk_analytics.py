import pandas as pd


def analyze(
    trades: pd.DataFrame,
    equity_curve: pd.DataFrame,
    market_data: pd.DataFrame,
    initial_capital: float,
) -> dict:
    """Example post-backtest analytics module."""
    total_bars = len(market_data)
    total_trades = len(trades)
    avg_win = float(trades[trades["pnl_usd"] > 0]["pnl_usd"].mean()) if not trades.empty and (trades["pnl_usd"] > 0).any() else 0.0

    return {
        "analyzed_market_bars": total_bars,
        "total_trades_count": total_trades,
        "avg_win_usd": round(avg_win, 2),
    }

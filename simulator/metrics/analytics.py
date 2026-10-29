from typing import List, Dict, Any
import numpy as np
import pandas as pd

from simulator.core.models import Trade


def calculate_metrics(
    trades: List[Trade],
    equity_history: List[Dict[str, Any]],
    initial_capital: float,
) -> Dict[str, Any]:
    """Calculates comprehensive performance metrics from trades and equity timeline."""
    df_trades = pd.DataFrame([t.to_dict() for t in trades]) if trades else pd.DataFrame()
    df_equity = pd.DataFrame(equity_history) if equity_history else pd.DataFrame()

    total_trades = len(trades)
    winning_trades = [t for t in trades if t.pnl_usd > 0]
    losing_trades = [t for t in trades if t.pnl_usd < 0]

    win_count = len(winning_trades)
    loss_count = len(losing_trades)
    win_rate = (win_count / total_trades * 100.0) if total_trades > 0 else 0.0

    long_trades = [t for t in trades if t.side == "long"]
    short_trades = [t for t in trades if t.side == "short"]

    gross_profit = sum(t.pnl_usd for t in winning_trades)
    gross_loss = abs(sum(t.pnl_usd for t in losing_trades))
    net_profit = sum(t.pnl_usd for t in trades)
    total_fees = sum(t.fee_usd for t in trades)
    final_capital = initial_capital + net_profit
    return_pct = (net_profit / initial_capital) * 100.0 if initial_capital > 0 else 0.0

    profit_factor = (gross_profit / gross_loss) if gross_loss > 0 else (999.0 if gross_profit > 0 else 0.0)

    # Max Drawdown from equity history (bar-by-bar accuracy)
    max_drawdown_pct = 0.0
    max_drawdown_usd = 0.0
    if not df_equity.empty and "equity" in df_equity.columns:
        equity_series = df_equity["equity"]
        peak = equity_series.cummax()
        drawdown_usd = peak - equity_series
        drawdown_pct = (drawdown_usd / peak.replace(0, np.nan)) * 100.0
        max_drawdown_usd = float(drawdown_usd.max()) if not drawdown_usd.empty else 0.0
        max_drawdown_pct = float(drawdown_pct.max()) if not drawdown_pct.empty else 0.0

    # Sharpe Ratio from trade returns
    sharpe_ratio = 0.0
    if not df_trades.empty and total_trades >= 2:
        returns = df_trades["pnl_usd"] / initial_capital
        std = returns.std()
        if std > 0:
            sharpe_ratio = float(returns.mean() / std)

    # Margin metrics
    max_margin_usd = 0.0
    margin_utilization = 0.0
    if not df_equity.empty and "margin_used" in df_equity.columns:
        max_margin_usd = float(df_equity["margin_used"].max())
        util = (df_equity["margin_used"] / df_equity["equity"].replace(0, np.nan)) * 100.0
        margin_utilization = float(util.max())

    fitness = (return_pct / max_drawdown_pct) if max_drawdown_pct > 0 else return_pct

    return {
        "total_trades": total_trades,
        "winning_trades": win_count,
        "losing_trades": loss_count,
        "win_rate_pct": round(win_rate, 2),
        "long_count": len(long_trades),
        "short_count": len(short_trades),
        "initial_capital": round(initial_capital, 2),
        "final_capital": round(final_capital, 2),
        "net_profit_usd": round(net_profit, 2),
        "total_fees_usd": round(total_fees, 2),
        "return_pct": round(return_pct, 2),
        "profit_factor": round(profit_factor, 2),
        "max_drawdown_pct": round(max_drawdown_pct, 2),
        "max_drawdown_usd": round(max_drawdown_usd, 2),
        "sharpe_ratio": round(sharpe_ratio, 3),
        "max_margin_usd": round(max_margin_usd, 2),
        "margin_utilization_pct": round(margin_utilization, 2),
        "fitness_score": round(fitness, 2),
    }

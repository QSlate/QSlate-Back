import os
from typing import Any, Dict

import pandas as pd

import backtest
from models import BacktestRequest


class BacktestRunnerError(Exception):
    def __init__(self, status_code: int, detail: str) -> None:
        super().__init__(detail)
        self.status_code = status_code
        self.detail = detail


def run_backtest_job(req: BacktestRequest) -> Dict[str, Any]:
    ticker = req.ticker.upper()
    if not ticker.isalnum():
        raise BacktestRunnerError(status_code=400, detail=f"Invalid ticker format: {req.ticker}")
    file_name = f"DATA_1H_{ticker}.csv"
    if not os.path.exists(file_name):
        raise BacktestRunnerError(status_code=404, detail=f"Data for ticker {req.ticker} not found.")

    local_env: Dict[str, Any] = {}
    try:
        exec(req.strategy_code, local_env)
    except Exception as exc:
        raise BacktestRunnerError(status_code=400, detail=f"Error compiling strategy code: {exc}") from exc

    if req.strategy_function_name not in local_env:
        raise BacktestRunnerError(
            status_code=400,
            detail=f"Strategy function '{req.strategy_function_name}' not found in the provided code.",
        )

    strategy_func = local_env[req.strategy_function_name]
    if not callable(strategy_func):
        raise BacktestRunnerError(status_code=400, detail=f"'{req.strategy_function_name}' is not callable.")

    try:
        df_history = backtest.run_backtest(
            csv_file=file_name,
            strategy_function=strategy_func,
            initial_capital=req.initial_capital,
            window=req.window,
            requested_indicators=req.indicators,
        )
    except Exception as exc:
        raise BacktestRunnerError(status_code=500, detail=f"Error during backtest execution: {exc}") from exc

    custom_stats_dict = {}
    if req.custom_stats_code and req.custom_stats_names:
        stats_env: Dict[str, Any] = {}
        try:
            exec(req.custom_stats_code, stats_env)
            for s_name in req.custom_stats_names:
                if s_name in stats_env and callable(stats_env[s_name]):
                    custom_stats_dict[s_name] = stats_env[s_name]
                else:
                    raise BacktestRunnerError(status_code=400, detail=f"Custom stat '{s_name}' not found or is not callable in code.")
        except BacktestRunnerError:
            raise
        except Exception as exc:
            raise BacktestRunnerError(status_code=400, detail=f"Error compiling custom stats code: {exc}") from exc

    try:
        report = backtest.generate_report(
            df_history,
            initial_capital=req.initial_capital,
            requested_stats=req.requested_stats,
            custom_stats=custom_stats_dict if custom_stats_dict else None
        )
    except Exception as exc:
        if custom_stats_dict:
            raise BacktestRunnerError(status_code=400, detail=f"Error generating report while executing custom stats: {exc}") from exc
        raise BacktestRunnerError(status_code=500, detail=f"Error generating report: {exc}") from exc

    if not df_history.empty:
        if req.sort_trades_by == "pnl_high_to_low":
            df_history = df_history.sort_values(by="pnl_usd", ascending=False)
        elif req.sort_trades_by == "pnl_low_to_high":
            df_history = df_history.sort_values(by="pnl_usd", ascending=True)

        if req.top_trades is not None and req.top_trades > 0:
            df_history = df_history.head(req.top_trades)

        df_history["entry_date"] = df_history["entry_date"].astype(str)
        df_history["exit_date"] = df_history["exit_date"].astype(str)
        trades = df_history.to_dict(orient="records")
    else:
        trades = []

    report_dict: Dict[str, Any] = {}
    if isinstance(report, pd.Series):
        report_dict = report.to_dict()
    elif isinstance(report, str):
        report_dict = {"message": report}

    return {"report": report_dict, "trades": trades}

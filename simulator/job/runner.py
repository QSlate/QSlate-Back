import io
import json
import os
import sys
import time
import traceback
import uuid
from contextlib import redirect_stdout, redirect_stderr
from typing import Dict, Any, Tuple, Optional
import numpy as np
import pandas as pd

from simulator.core.strategy import Strategy
from simulator.core.engine import BacktestEngine, SimulationResult
from simulator.data.loader import load_market_data
from simulator.job.schema import JobConfig, JobSummary


def _resolve_data_path(config: JobConfig) -> str:
    if config.dataset.file_path and os.path.exists(config.dataset.file_path):
        return config.dataset.file_path

    if config.dataset.ticker:
        # Check standard locations
        candidates = [
            f"DATA_1H_{config.dataset.ticker.upper()}.csv",
            os.path.join("simulator", f"DATA_1H_{config.dataset.ticker.upper()}.csv"),
            os.path.join(os.path.dirname(__file__), "..", f"DATA_1H_{config.dataset.ticker.upper()}.csv"),
        ]
        for c in candidates:
            if os.path.exists(c):
                return c

    raise FileNotFoundError(
        f"Unable to resolve dataset. Checked file_path='{config.dataset.file_path}' "
        f"and ticker='{config.dataset.ticker}'"
    )


def _load_strategy_instance(strategy_config, globals_scope: Dict[str, Any]) -> Strategy:
    # Inject standard simulator modules into script environment
    import simulator
    from simulator import Strategy, Context, Candle, Position, Trade, OrderSide

    globals_scope.update(
        {
            "simulator": simulator,
            "Strategy": Strategy,
            "Context": Context,
            "Candle": Candle,
            "Position": Position,
            "Trade": Trade,
            "OrderSide": OrderSide,
        }
    )

    source_code = strategy_config.source_code
    if not source_code and strategy_config.file_path:
        if not os.path.exists(strategy_config.file_path):
            raise FileNotFoundError(f"Strategy file not found: {strategy_config.file_path}")
        with open(strategy_config.file_path, "r", encoding="utf-8") as f:
            source_code = f.read()

    if not source_code:
        raise ValueError("Neither 'source_code' nor 'file_path' provided for StrategyConfig.")

    exec(source_code, globals_scope)

    # 1. If explicit class_name is provided
    if strategy_config.class_name:
        target = globals_scope.get(strategy_config.class_name)
        if not target:
            raise ValueError(f"Strategy class '{strategy_config.class_name}' not found in provided code.")
        if isinstance(target, type) and issubclass(target, Strategy):
            return target(**strategy_config.parameters)
        raise TypeError(f"Target '{strategy_config.class_name}' is not a subclass of Strategy.")

    # 2. Look for any subclass of Strategy defined in user code
    strategy_subclasses = [
        cls
        for cls in globals_scope.values()
        if isinstance(cls, type) and issubclass(cls, Strategy) and cls is not Strategy
    ]
    if len(strategy_subclasses) == 1:
        return strategy_subclasses[0](**strategy_config.parameters)
    elif len(strategy_subclasses) > 1:
        names = [c.__name__ for c in strategy_subclasses]
        raise ValueError(f"Multiple Strategy subclasses found ({names}). Please specify 'class_name'.")

    raise ValueError("No Strategy subclass found in provided code. Please define a class inheriting from Strategy.")


def _load_marketplace_indicators(config: JobConfig) -> Dict[str, Any]:
    custom_indicators: Dict[str, Any] = {}
    for ind in config.marketplace_indicators:
        source_code = ind.source_code
        if not source_code and ind.file_path:
            if not os.path.exists(ind.file_path):
                raise FileNotFoundError(f"Indicator file not found: {ind.file_path}")
            with open(ind.file_path, "r", encoding="utf-8") as f:
                source_code = f.read()

        scope: Dict[str, Any] = {"pd": pd, "np": np}
        exec(source_code, scope)

        target_fn = scope.get(ind.name) or scope.get("calculate")
        if not target_fn or not callable(target_fn):
            callables = [v for k, v in scope.items() if callable(v) and not k.startswith("_") and k not in ["pd", "np"]]
            if len(callables) == 1:
                target_fn = callables[0]
            else:
                raise ValueError(f"Indicator '{ind.name}' must define a callable function (e.g. 'calculate').")

        # Wrap with default params
        def _make_wrapper(fn, defaults):
            def wrapper(data, *args, **kwargs):
                merged = {**defaults, **kwargs}
                return fn(data, *args, **merged)
            return wrapper

        custom_indicators[ind.name] = _make_wrapper(target_fn, ind.default_params)
    return custom_indicators


def _run_analytics_modules(
    config: JobConfig,
    result: SimulationResult,
    df_market: pd.DataFrame,
) -> Dict[str, Any]:
    analytics_results: Dict[str, Any] = {}
    if not config.analytics_modules:
        return analytics_results

    df_trades = pd.DataFrame([t.to_dict() for t in result.trades]) if result.trades else pd.DataFrame()
    df_equity = pd.DataFrame(result.equity_history) if result.equity_history else pd.DataFrame()

    for mod in config.analytics_modules:
        source_code = mod.source_code
        if not source_code and mod.file_path:
            if not os.path.exists(mod.file_path):
                raise FileNotFoundError(f"Analytics file not found: {mod.file_path}")
            with open(mod.file_path, "r", encoding="utf-8") as f:
                source_code = f.read()

        scope: Dict[str, Any] = {"pd": pd, "np": np}
        exec(source_code, scope)

        analyze_fn = scope.get(mod.name) or scope.get("analyze")
        if not analyze_fn or not callable(analyze_fn):
            callables = [v for k, v in scope.items() if callable(v) and not k.startswith("_") and k not in ["pd", "np"]]
            if len(callables) == 1:
                analyze_fn = callables[0]
            else:
                raise ValueError(f"Analytics module '{mod.name}' must define an 'analyze' function.")

        try:
            mod_output = analyze_fn(
                trades=df_trades,
                equity_curve=df_equity,
                market_data=df_market,
                initial_capital=config.backtest.initial_capital,
            )
            analytics_results[mod.name] = mod_output
        except Exception as exc:
            analytics_results[mod.name] = {"error": str(exc)}

    return analytics_results


def run_job(config: JobConfig) -> Tuple[JobSummary, Optional[SimulationResult], str]:
    """Executes a backtest job in an isolated context with stdout/stderr capture."""
    job_id = config.job_id or f"job_{uuid.uuid4().hex[:12]}"
    start_time = time.time()

    stdout_capture = io.StringIO()
    stderr_capture = io.StringIO()

    try:
        data_path = _resolve_data_path(config)
        df_market = load_market_data(
            file_path=data_path,
            start_date=config.dataset.start_date,
            end_date=config.dataset.end_date,
        )

        with redirect_stdout(stdout_capture), redirect_stderr(stderr_capture):
            script_env: Dict[str, Any] = {}
            strategy_instance = _load_strategy_instance(config.strategy, script_env)
            custom_indicators = _load_marketplace_indicators(config)

            engine = BacktestEngine(
                data=df_market,
                strategy=strategy_instance,
                initial_capital=config.backtest.initial_capital,
                window=config.backtest.window,
                commission_rate=config.backtest.commission_rate,
                slippage_bps=config.backtest.slippage_bps,
                custom_indicators=custom_indicators,
            )
            result = engine.run()

            # Execute external post-processing analytics modules
            ext_analytics = _run_analytics_modules(config, result, df_market)
            if ext_analytics:
                result.metrics["analytics_modules"] = ext_analytics

        elapsed = time.time() - start_time
        summary = JobSummary(
            job_id=job_id,
            status="SUCCESS",
            execution_time_seconds=round(elapsed, 3),
            metrics=result.metrics,
        )
        logs = stdout_capture.getvalue() + stderr_capture.getvalue()
        return summary, result, logs

    except Exception as exc:
        elapsed = time.time() - start_time
        err_msg = f"{type(exc).__name__}: {str(exc)}\n{traceback.format_exc()}"
        summary = JobSummary(
            job_id=job_id,
            status="FAILED",
            execution_time_seconds=round(elapsed, 3),
            error=f"{type(exc).__name__}: {str(exc)}",
        )
        logs = stdout_capture.getvalue() + stderr_capture.getvalue() + f"\n[ERROR] {err_msg}"
        return summary, None, logs


def save_job_artifacts(
    output_dir: str,
    summary: JobSummary,
    result: Optional[SimulationResult],
    logs: str,
) -> Dict[str, str]:
    """Writes standardized execution artifacts into the output directory."""
    os.makedirs(output_dir, exist_ok=True)
    artifact_paths = {}

    summary_path = os.path.join(output_dir, "summary.json")
    with open(summary_path, "w", encoding="utf-8") as f:
        json.dump(summary.model_dump(), f, indent=2)
    artifact_paths["summary"] = summary_path

    logs_path = os.path.join(output_dir, "logs.txt")
    with open(logs_path, "w", encoding="utf-8") as f:
        f.write(logs)
    artifact_paths["logs"] = logs_path

    if result:
        trades_path = os.path.join(output_dir, "trades.json")
        with open(trades_path, "w", encoding="utf-8") as f:
            json.dump([t.to_dict() for t in result.trades], f, indent=2)
        artifact_paths["trades"] = trades_path

        equity_path = os.path.join(output_dir, "equity_curve.json")
        with open(equity_path, "w", encoding="utf-8") as f:
            json.dump(result.equity_history, f, indent=2)
        artifact_paths["equity_curve"] = equity_path

        if result.telemetry_history:
            telemetry_path = os.path.join(output_dir, "telemetry.json")
            with open(telemetry_path, "w", encoding="utf-8") as f:
                json.dump(result.telemetry_history, f, indent=2)
            artifact_paths["telemetry"] = telemetry_path
    else:
        err_path = os.path.join(output_dir, "error.json")
        with open(err_path, "w", encoding="utf-8") as f:
            json.dump({"error": summary.error}, f, indent=2)
        artifact_paths["error"] = err_path

    return artifact_paths

#!/usr/bin/env python3
"""Interactive test runner for QSlate Simulator.

Allows running a backtest directly from .py files without needing to paste code into JSON.

Usage:
    # Run default test using example .py files:
    python simulator/tests/run_test.py

    # Run with a custom Python strategy file:
    python simulator/tests/run_test.py --strategy path/to/my_strategy.py --ticker AAPL

    # Run with a clean JSON config (referencing .py files):
    python simulator/tests/run_test.py --config simulator/examples/config_local.json
"""

import argparse
import json
import os
import sys

# Ensure repository root is on sys.path
REPO_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
if REPO_ROOT not in sys.path:
    sys.path.insert(0, REPO_ROOT)

from simulator.job.schema import (
    JobConfig,
    DatasetConfig,
    StrategyConfig,
    BacktestConfig,
    MarketplaceIndicator,
    AnalyticsModule,
)
from simulator.job.runner import run_job, save_job_artifacts


def build_config_from_args(args) -> JobConfig:
    # 1. If a JSON config file is provided, load it
    if args.config:
        config_path = args.config
        if not os.path.isabs(config_path):
            config_path = os.path.join(REPO_ROOT, config_path)
        with open(config_path, "r", encoding="utf-8") as f:
            data = json.load(f)
        return JobConfig(**data)

    # 2. Otherwise build dynamically from CLI arguments and default .py files
    strategy_path = args.strategy
    if not os.path.isabs(strategy_path):
        strategy_path = os.path.join(REPO_ROOT, strategy_path)

    if not os.path.exists(strategy_path):
        raise FileNotFoundError(f"Strategy file not found: {strategy_path}")

    # Optional marketplace indicators
    indicators = []
    if args.indicator:
        for ind_spec in args.indicator:
            # Format: name=path/to/file.py
            if "=" in ind_spec:
                name, path = ind_spec.split("=", 1)
            else:
                name = os.path.splitext(os.path.basename(ind_spec))[0]
                path = ind_spec
            if not os.path.isabs(path):
                path = os.path.join(REPO_ROOT, path)
            indicators.append(MarketplaceIndicator(name=name, file_path=path))

    # Optional analytics modules
    analytics = []
    if args.analytics:
        for ana_spec in args.analytics:
            if "=" in ana_spec:
                name, path = ana_spec.split("=", 1)
            else:
                name = os.path.splitext(os.path.basename(ana_spec))[0]
                path = ana_spec
            if not os.path.isabs(path):
                path = os.path.join(REPO_ROOT, path)
            analytics.append(AnalyticsModule(name=name, file_path=path))

    data_file = args.data_file
    if data_file and not os.path.isabs(data_file):
        data_file = os.path.join(REPO_ROOT, data_file)

    output_dir = args.output_dir
    if output_dir and not os.path.isabs(output_dir):
        output_dir = os.path.join(REPO_ROOT, output_dir)

    return JobConfig(
        job_id=args.job_id,
        dataset=DatasetConfig(
            source="local_csv",
            ticker=args.ticker,
            file_path=data_file,
        ),
        strategy=StrategyConfig(
            file_path=strategy_path,
            parameters=json.loads(args.strategy_params) if args.strategy_params else {},
        ),
        marketplace_indicators=indicators,
        analytics_modules=analytics,
        backtest=BacktestConfig(
            initial_capital=args.capital,
            window=args.window,
            commission_rate=args.commission,
            slippage_bps=args.slippage,
        ),
        output_dir=output_dir,
    )


def print_banner():
    print("=" * 65)
    print("        🚀 QSlate Backtest Runner - Local Test Suite")
    print("=" * 65)


def print_results(summary, result, logs, artifacts):
    print("\n" + "-" * 65)
    print(f" Execution Status : {summary.status}")
    print(f" Execution Time   : {summary.execution_time_seconds:.3f} s")
    print("-" * 65)

    if summary.status == "SUCCESS" and summary.metrics:
        m = summary.metrics
        print("\n📊 KEY PERFORMANCE METRICS:")
        print(f"  • Initial Capital   : ${m.get('initial_capital', 0):,.2f}")
        print(f"  • Final Capital     : ${m.get('final_capital', 0):,.2f}")
        print(f"  • Net Profit        : ${m.get('net_profit_usd', 0):,.2f} ({m.get('return_pct', 0):+.2f}%)")
        print(f"  • Total Trades      : {m.get('total_trades', 0)} (Wins: {m.get('winning_trades', 0)}, Losses: {m.get('losing_trades', 0)})")
        print(f"  • Win Rate          : {m.get('win_rate_pct', 0):.2f}%")
        print(f"  • Profit Factor     : {m.get('profit_factor', 0):.2f}")
        print(f"  • Max Drawdown      : {m.get('max_drawdown_pct', 0):.2f}% (${m.get('max_drawdown_usd', 0):,.2f})")
        print(f"  • Sharpe Ratio      : {m.get('sharpe_ratio', 0):.3f}")
        print(f"  • Total Fees Paid   : ${m.get('total_fees_usd', 0):.2f}")

        if "strategy_metrics" in m:
            print("\n🎯 IN-STRATEGY METRICS (on_stop):")
            for k, v in m["strategy_metrics"].items():
                print(f"  • {k}: {v}")

        if "analytics_modules" in m:
            print("\n📦 MARKETPLACE ANALYTICS:")
            for mod_name, mod_data in m["analytics_modules"].items():
                print(f"  • [{mod_name}]: {mod_data}")

        if result and result.trades:
            print(f"\n📜 RECENT TRADES (showing last min(5, {len(result.trades)})):")
            for t in result.trades[-5:]:
                color = "+" if t.pnl_usd >= 0 else "-"
                print(
                    f"  [{t.exit_date}] {t.side.upper():5} | "
                    f"Entry: ${t.entry_price:7.2f} -> Exit: ${t.exit_price:7.2f} | "
                    f"PnL: {color}${abs(t.pnl_usd):6.2f} ({color}{abs(t.pnl_pct):.2f}%) | "
                    f"Reason: {t.exit_reason}"
                )

        if result and result.telemetry_history:
            print(f"\n📈 TELEMETRY: {len(result.telemetry_history)} time-series data points recorded.")

    elif summary.status == "FAILED":
        print(f"\n❌ ERROR: {summary.error}")

    print("\n📁 GENERATED ARTIFACTS:")
    for name, path in artifacts.items():
        print(f"  • {name:13}: {path}")

    print("=" * 65 + "\n")


def main():
    parser = argparse.ArgumentParser(description="Run backtest test directly from .py files")
    parser.add_argument(
        "--config",
        "-c",
        help="Path to JSON config referencing .py files (default: simulator/examples/config_local.json)",
        default="simulator/examples/config_local.json",
    )
    parser.add_argument(
        "--strategy",
        "-s",
        help="Path to Python strategy .py file",
        default="simulator/examples/rsi_trend_strategy.py",
    )
    parser.add_argument("--ticker", "-t", default="AAPL", help="Ticker symbol (default: AAPL)")
    parser.add_argument("--data-file", "-d", help="Explicit path to CSV market data file", default=None)
    parser.add_argument("--capital", type=float, default=10000.0, help="Initial capital (default: 10000.0)")
    parser.add_argument("--window", type=int, default=50, help="Window size (default: 50)")
    parser.add_argument("--commission", type=float, default=0.0005, help="Commission rate (default: 0.0005)")
    parser.add_argument("--slippage", type=float, default=2.0, help="Slippage in bps (default: 2.0)")
    parser.add_argument(
        "--strategy-params",
        help="JSON string of parameters for the Strategy (e.g. '{\"rsi_period\": 14}')",
        default=None,
    )
    parser.add_argument(
        "--indicator",
        action="append",
        help="Path to marketplace indicator .py file (format: name=path.py)",
    )
    parser.add_argument(
        "--analytics",
        action="append",
        help="Path to analytics module .py file (format: name=path.py)",
    )
    parser.add_argument(
        "--output-dir",
        "-o",
        default="simulator/output/test_run",
        help="Directory to save output JSON files (default: simulator/output/test_run)",
    )
    parser.add_argument("--job-id", default="test_interactive_run", help="Custom job ID")

    # If user passed specific strategy/ticker or no config explicitly, check arguments
    args = parser.parse_args()

    print_banner()

    try:
        config = build_config_from_args(args)
    except Exception as exc:
        print(f"❌ Configuration error: {exc}", file=sys.stderr)
        return 1

    print(f"Loading Strategy: {config.strategy.file_path or 'inline code'}")
    print(f"Loading Ticker  : {config.dataset.ticker or config.dataset.file_path}")
    print(f"Initial Capital : ${config.backtest.initial_capital:,.2f}")
    print("Running backtest simulation...\n")

    summary, result, logs = run_job(config)

    output_dir = config.output_dir or os.path.join(REPO_ROOT, "simulator", "output", summary.job_id)
    artifacts = save_job_artifacts(output_dir, summary, result, logs)

    print_results(summary, result, logs, artifacts)
    return 0 if summary.status == "SUCCESS" else 1


if __name__ == "__main__":
    sys.exit(main())

import argparse
import json
import os
import sys

from simulator.job.schema import JobConfig
from simulator.job.runner import run_job, save_job_artifacts


def main() -> int:
    parser = argparse.ArgumentParser(description="QSlate Backtest Isolated Job Runner")
    parser.add_argument(
        "--config",
        "-c",
        required=True,
        help="Path to JSON job configuration file, or '-' to read from stdin.",
    )
    parser.add_argument(
        "--output-dir",
        "-o",
        default=None,
        help="Directory to save output artifacts (summary.json, trades.json, equity_curve.json, logs.txt).",
    )

    args = parser.parse_args()

    # Load configuration
    try:
        if args.config == "-":
            raw_content = sys.stdin.read()
        else:
            with open(args.config, "r", encoding="utf-8") as f:
                raw_content = f.read()

        config_dict = json.loads(raw_content)
        job_config = JobConfig(**config_dict)
    except Exception as exc:
        print(f"Failed to parse job configuration: {exc}", file=sys.stderr)
        return 1

    # Execute job
    summary, result, logs = run_job(job_config)

    # Determine output directory
    output_dir = args.output_dir or job_config.output_dir or os.path.join("artifacts", summary.job_id)
    artifacts = save_job_artifacts(output_dir, summary, result, logs)

    print(f"Job completed: {summary.status} (elapsed: {summary.execution_time_seconds}s)")
    print(f"Artifacts saved to: {output_dir}")
    for name, path in artifacts.items():
        print(f"  - {name}: {path}")

    if summary.status == "SUCCESS" and summary.metrics:
        print("\n--- Key Performance Metrics ---")
        for k in ["total_trades", "win_rate_pct", "return_pct", "max_drawdown_pct", "sharpe_ratio"]:
            if k in summary.metrics:
                print(f"  {k}: {summary.metrics[k]}")

    return 0 if summary.status == "SUCCESS" else 1


if __name__ == "__main__":
    sys.exit(main())

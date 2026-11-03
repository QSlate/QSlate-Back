"""Lab stdin/NDJSON adapter. Run ONLY in the worker's isolated job container.

No HTTP listener, network access, JWT, DB credential or caller-supplied file path.
The engine remains the implementation imported from branch #18.
"""
import json
import math
import os
import random
import sys
import tempfile
from pathlib import Path

import numpy as np

from simulator.job.runner import run_job
from simulator.job.schema import JobConfig


def finite_json(value):
    if isinstance(value, dict):
        return {str(k): finite_json(v) for k, v in value.items()}
    if isinstance(value, (list, tuple)):
        return [finite_json(v) for v in value]
    if isinstance(value, np.generic):
        return finite_json(value.item())
    if isinstance(value, float) and not math.isfinite(value):
        return None
    if value is None or isinstance(value, (str, int, float, bool)):
        return value
    raise TypeError("unsupported artifact value")


def execute(payload, emit, directory):
    # Configuration comes from the trusted Go worker, not from a public JobConfig.
    run = payload["input"]
    data = Path(directory) / "dataset.csv"
    data.write_text(payload["csv"], encoding="utf-8")
    random.seed(0)
    np.random.seed(0)
    config = JobConfig.model_validate({
        "job_id": payload["job_id"],
        "dataset": {"source": "local_csv", "file_path": str(data)},
        "strategy": {"source_code": payload["source"],
                     "parameters": run.get("parameters") or {},
                     "class_name": run.get("class_name") or None},
        "backtest": {"initial_capital": float(run["initial_capital"]),
                     "window": run["window"],
                     "commission_rate": run.get("commission_rate", 0),
                     "slippage_bps": run.get("slippage_bps", 0)},
    })
    def progress(done, total):
        emit({"type": "progress", "progress": min(99, done * 100 // total)})

    summary, result, logs = run_job(config, on_progress=progress,
                                    max_log_bytes=payload["max_log_bytes"])
    output = {
        "status": "completed" if summary.status == "SUCCESS" else "failed",
        "summary": summary.model_dump(),
        "trades": [t.to_dict() for t in result.trades] if result else [],
        "equity_curve": result.equity_history if result else [],
        "telemetry": result.telemetry_history if result else [],
        "logs": logs,
        "error": summary.error or "",
    }
    emit({"type": "result", "result": finite_json(output)})


def main():
    raw = sys.stdin.buffer.read(20 * 1024 * 1024 + 1)
    if len(raw) > 20 * 1024 * 1024:
        raise ValueError("job envelope too large")
    payload = json.loads(raw)
    # Keep protocol output out of run_job's Python-level stdout redirection.
    with os.fdopen(os.dup(sys.stdout.fileno()), "w", buffering=1) as protocol:
        def emit(event):
            protocol.write(json.dumps(event, allow_nan=False, ensure_ascii=True) + "\n")
        with tempfile.TemporaryDirectory(prefix="lab-", dir=os.environ.get("TMPDIR", "/work")) as directory:
            execute(payload, emit, directory)


if __name__ == "__main__":
    main()

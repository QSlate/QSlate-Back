from fastapi import FastAPI, HTTPException
import os

from job_runner import BacktestRunnerError, run_backtest_job
from models import BacktestRequest

app = FastAPI(title="QSlate Backtest Runner")


@app.get("/healthz")
def healthcheck():
    return {"status": "ok"}


@app.post("/backtest/run")
def run_backtest(req: BacktestRequest):
    try:
        return run_backtest_job(req)
    except BacktestRunnerError as err:
        raise HTTPException(status_code=err.status_code, detail=err.detail) from err


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(
        "runner_service:app",
        host=os.getenv("RUNNER_HOST", "127.0.0.1"),
        port=int(os.getenv("RUNNER_PORT", "8090")),
        reload=False,
    )

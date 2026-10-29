# ── Stage 1: Build deps ───────────────────────────────────────────────────────
FROM python:3.12-slim AS builder

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir --prefix=/install -r requirements.txt

# ── Stage 2: Runtime ─────────────────────────────────────────────────────────
FROM python:3.12-slim

WORKDIR /app

COPY --from=builder /install /usr/local
COPY . .

# Default entrypoint for ephemeral backtest jobs in cloud / Kubernetes
ENTRYPOINT ["python", "-m", "simulator.job.cli"]
CMD ["--help"]

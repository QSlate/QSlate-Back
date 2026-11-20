FROM python:3.12-slim AS base
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY parse.py ./
ENV PYTHONDONTWRITEBYTECODE=1 OPENBLAS_NUM_THREADS=1 OMP_NUM_THREADS=1

FROM base AS test
COPY test_parse.py ./
USER 10001:10001
RUN python -B -m unittest discover -s /app -p 'test_*.py'

FROM base AS runtime
COPY server.py ./
USER 10001:10001
ENTRYPOINT ["python", "-I", "-B", "/app/server.py"]

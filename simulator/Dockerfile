FROM python:3.12-slim AS build
WORKDIR /build
COPY requirements.txt .
RUN pip install --no-cache-dir --prefix=/install -r requirements.txt

FROM python:3.12-slim
WORKDIR /app
COPY --from=build /install /usr/local
COPY . /app/simulator
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 TMPDIR=/work OPENBLAS_NUM_THREADS=1 OMP_NUM_THREADS=1
USER 10001:10001
ENTRYPOINT ["python", "-B", "-m", "simulator.lab_bridge"]

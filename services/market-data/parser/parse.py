"""Trusted one-shot CSV/Parquet decoder. Never imports or executes uploaded code."""
import base64
import csv
from datetime import datetime, timezone, timedelta
from decimal import Decimal, InvalidOperation
import io
import json
import math
import re
import resource
import sys
from zoneinfo import ZoneInfo

def read_rows(payload):
    raw = base64.b64decode(payload["data"], validate=True)
    if len(raw) > payload["max_upload_bytes"]:
        raise ValueError("upload limit")
    if payload["format"] == "csv":
        csv.field_size_limit(4096)
        text = raw.decode("utf-8-sig")
        if "\0" in text:
            raise ValueError("NUL byte")
        reader = csv.reader(io.StringIO(text), strict=True)
        columns = next(reader)
        rows = []
        for row in reader:
            if len(row) != len(columns) or len(rows) >= payload["max_rows"]:
                raise ValueError("row count or width")
            rows.append(row)
    elif payload["format"] == "parquet":
        if not raw.startswith(b"PAR1") or not raw.endswith(b"PAR1"):
            raise ValueError("Parquet magic")
        import pyarrow as pa
        import pyarrow.parquet as pq
        pa.set_cpu_count(1)
        pa.set_io_thread_count(1)
        f = pq.ParquetFile(pa.BufferReader(raw), arrow_extensions_enabled=False,
                           thrift_string_size_limit=65536, thrift_container_size_limit=65536,
                           page_checksum_verification=True)
        if f.metadata.num_rows > payload["max_rows"] or len(f.schema_arrow) > 64:
            raise ValueError("Parquet metadata limits")
        for field in f.schema_arrow:
            t = field.type
            if not (pa.types.is_integer(t) or pa.types.is_floating(t) or pa.types.is_decimal(t)
                    or pa.types.is_string(t) or pa.types.is_boolean(t)
                    or pa.types.is_timestamp(t) or pa.types.is_date(t)):
                raise ValueError("unsupported Parquet type")
        columns = f.schema_arrow.names
        rows = []
        for batch in f.iter_batches(batch_size=128, use_threads=False):
            for row in batch.to_pylist():
                rows.append(["" if row[c] is None else str(row[c]) for c in columns])
                if len(rows) > payload["max_rows"]:
                    raise ValueError("row limit")
    else:
        raise ValueError("format")
    if not 1 <= len(columns) <= 64 or len(set(columns)) != len(columns) or not rows:
        raise ValueError("empty or ambiguous schema")
    for c in columns:
        if not c or c.strip() != c or len(c.encode()) > 128 or any(ord(x) < 32 for x in c):
            raise ValueError("column name")
    for row in rows:
        if any(len(c.encode()) > 4096 or "\0" in c for c in row):
            raise ValueError("cell limit")
    return columns, rows

def instant(value, mapping):
    unit = mapping["timestamp_unit"]
    if unit == "iso8601":
        match = re.search(r"\.(\d+)", value)
        if match and len(match[1]) > 6:
            raise ValueError("timestamp precision exceeds microseconds")
        ts = datetime.fromisoformat(value.replace("Z", "+00:00"))
        if ts.tzinfo is None:
            zone = ZoneInfo(mapping["timezone"])
            a, b = ts.replace(tzinfo=zone, fold=0), ts.replace(tzinfo=zone, fold=1)
            if a.utcoffset() != b.utcoffset() or a.astimezone(timezone.utc).astimezone(zone).replace(tzinfo=None) != ts:
                raise ValueError("ambiguous/nonexistent local timestamp; supply an offset")
            ts = a
    else:
        scale = {"s": Decimal(1000000), "ms": Decimal(1000), "us": Decimal(1), "ns": Decimal("0.001")}[unit]
        micros = Decimal(value) * scale
        if not micros.is_finite() or micros != micros.to_integral_value() or abs(micros) > Decimal("1e16"):
            raise ValueError("invalid timestamp")
        ts = datetime(1970, 1, 1, tzinfo=timezone.utc) + timedelta(microseconds=int(micros))
    ts = ts.astimezone(timezone.utc)
    if not 1970 <= ts.year <= 2200:
        raise ValueError("timestamp range")
    return ts

def amount(value, positive):
    if not re.fullmatch(r"[+-]?[0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]{1,3})?", value):
        raise ValueError("decimal syntax")
    n = Decimal(value)
    if not n.is_finite() or n < 0 or (positive and n == 0) or n >= Decimal("1e20"):
        raise ValueError("numeric range")
    normalized = n.normalize()
    if normalized.as_tuple().exponent < -18:
        raise ValueError("numeric precision")
    return format(n, "f")

def parse(payload):
    columns, rows = read_rows(payload)
    out = {"columns": columns, "preview": [[c[:128] for c in row] for row in rows[:5]], "row_count": len(rows)}
    mapping = payload.get("mapping")
    if mapping is None:
        return out
    names = [mapping["timestamp_column"]] + [mapping["columns"][k] for k in ("open", "high", "low", "close")]
    volume = mapping["columns"].get("volume")
    if volume:
        names.append(volume)
    if len(set(names)) != len(names) or not all(n in columns for n in names):
        raise ValueError("mapping")
    indexes = [columns.index(n) for n in names]
    normalized = []
    previous = None
    for row in rows:
        ts = instant(row[indexes[0]], mapping)
        if previous is not None and ts <= previous:
            raise ValueError("timestamps must be strictly increasing")
        previous = ts
        prices = [amount(row[i], True) for i in indexes[1:5]]
        o, h, l, c = map(Decimal, prices)
        if h < max(o, c) or l > min(o, c):
            raise ValueError("invalid OHLC")
        v = amount(row[indexes[5]], False) if volume else "0"
        normalized.append([ts.isoformat().replace("+00:00", "Z"), *prices, v])
    out["rows"] = normalized
    return out

def main():
    resource.setrlimit(resource.RLIMIT_AS, (2 * 1024**3,) * 2)
    resource.setrlimit(resource.RLIMIT_CPU, (5, 5))
    resource.setrlimit(resource.RLIMIT_FSIZE, (0, 0))
    resource.setrlimit(resource.RLIMIT_NOFILE, (64, 64))
    payload = json.loads(sys.stdin.buffer.read(145 * 1024**2 + 1))
    result = parse(payload)
    encoded = json.dumps(result, allow_nan=False, ensure_ascii=True)
    if len(encoded) > 32 * 1024**2:
        raise ValueError("decoded response limit")
    sys.stdout.write(encoded)

if __name__ == "__main__":
    try:
        main()
    except Exception:
        sys.exit(2)

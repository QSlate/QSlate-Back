import json
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from simulator.lab_bridge import execute, finite_json


SOURCE = '''from simulator import Strategy
class BuyOnce(Strategy):
    def on_bar(self, ctx):
        print("bar processed")
        ctx.record(price=ctx.price)
        if not ctx.has_position:
            ctx.buy(usd_amount=1000)
'''


class TestLabBridge(unittest.TestCase):
    def payload(self, source=SOURCE):
        rows = ["Datetime,Open,High,Low,Close,Volume"]
        for i in range(30):
            ts = datetime(2024, 1, 1, tzinfo=timezone.utc) + timedelta(hours=i)
            rows.append(f"{ts.isoformat()},{100+i},{102+i},{99+i},{101+i},100")
        return {"job_id": "test-real-engine", "csv": "\n".join(rows), "source": source,
                "input": {"initial_capital": "10000", "window": 5, "parameters": {}},
                "max_log_bytes": 128}

    def test_actual_engine_artifacts_progress_and_repeatability(self):
        events = []
        with tempfile.TemporaryDirectory() as directory:
            execute(self.payload(), events.append, directory)
            first = events[-1]["result"]
            again = []
            execute(self.payload(), again.append, directory)
        self.assertEqual(first["status"], "completed")
        self.assertEqual(len(first["equity_curve"]), 25)
        self.assertGreater(len(first["trades"]), 0)
        self.assertGreater(len(first["telemetry"]), 0)
        self.assertEqual(first["trades"], again[-1]["result"]["trades"])
        self.assertIn("bar processed", first["logs"])
        self.assertLessEqual(len(first["logs"].encode()), 128)
        self.assertTrue(any(e["type"] == "progress" and e["progress"] > 0 for e in events))
        json.dumps(first, allow_nan=False)

    def test_failure_is_not_reported_as_completed(self):
        events = []
        with tempfile.TemporaryDirectory() as directory:
            execute(self.payload("raise RuntimeError('broken strategy')"), events.append, directory)
        result = events[-1]["result"]
        self.assertEqual(result["status"], "failed")
        self.assertIn("broken strategy", result["logs"])
        self.assertEqual(result["trades"], [])

    def test_non_finite_values_remain_null_not_fabricated_zero(self):
        self.assertIsNone(finite_json(float("nan")))
        self.assertIsNone(finite_json(float("inf")))

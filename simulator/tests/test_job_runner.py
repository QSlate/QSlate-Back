import unittest
import tempfile
import os
import shutil
from simulator.job.schema import JobConfig, DatasetConfig, StrategyConfig
from simulator.job.runner import run_job, save_job_artifacts


class TestJobRunner(unittest.TestCase):
    def setUp(self):
        self.temp_dir = tempfile.mkdtemp()

    def tearDown(self):
        shutil.rmtree(self.temp_dir, ignore_errors=True)

    def test_run_valid_job(self):
        source = """
from simulator import Strategy

class SimpleTestStrategy(Strategy):
    def on_bar(self, ctx):
        if not ctx.has_position:
            ctx.buy(usd_amount=1000.0, stop_loss=ctx.price * 0.9, take_profit=ctx.price * 1.1)
"""
        config = JobConfig(
            job_id="unit_test_job_1",
            dataset=DatasetConfig(ticker="AAPL"),
            strategy=StrategyConfig(source_code=source),
            output_dir=self.temp_dir,
        )
        summary, result, logs = run_job(config)
        self.assertEqual(summary.status, "SUCCESS")
        self.assertIsNotNone(result)

        artifacts = save_job_artifacts(self.temp_dir, summary, result, logs)
        self.assertTrue(os.path.exists(artifacts["summary"]))
        self.assertTrue(os.path.exists(artifacts["trades"]))
        self.assertTrue(os.path.exists(artifacts["equity_curve"]))
        self.assertTrue(os.path.exists(artifacts["logs"]))

    def test_run_buggy_job(self):
        source = """
from simulator import Strategy

class Buggy(Strategy):
    def on_bar(self, ctx):
        raise ValueError("Intentional crash for test")
"""
        config = JobConfig(
            job_id="unit_test_job_fail",
            dataset=DatasetConfig(ticker="AAPL"),
            strategy=StrategyConfig(source_code=source),
            output_dir=self.temp_dir,
        )
        summary, result, logs = run_job(config)
        self.assertEqual(summary.status, "FAILED")
        self.assertIn("ValueError", summary.error)
        self.assertIn("Intentional crash for test", logs)

    def test_marketplace_and_telemetry(self):
        from simulator.job.schema import MarketplaceIndicator, AnalyticsModule

        ind_code = """
def calculate(data, p=10):
    return (data['High'] - data['Low']).rolling(p).mean()
"""
        strat_code = """
from simulator import Strategy

class TelemetryStrategy(Strategy):
    def on_bar(self, ctx):
        spread = ctx.indicators.custom_spread(p=5)
        if len(spread) > 5:
            ctx.record(spread=float(spread.iloc[-1]))
    def on_stop(self, ctx):
        return {'ended_ok': True}
"""
        analytics_code = """
def analyze(trades, equity_curve, market_data, initial_capital):
    return {'analyst_score': 99}
"""
        config = JobConfig(
            job_id="test_mkp_telemetry",
            dataset=DatasetConfig(ticker="AAPL"),
            marketplace_indicators=[MarketplaceIndicator(name="custom_spread", source_code=ind_code)],
            strategy=StrategyConfig(source_code=strat_code),
            analytics_modules=[AnalyticsModule(name="pack_alpha", source_code=analytics_code)],
            output_dir=self.temp_dir,
        )
        summary, result, logs = run_job(config)
        self.assertEqual(summary.status, "SUCCESS")
        self.assertIn("ended_ok", summary.metrics["strategy_metrics"])
        self.assertEqual(summary.metrics["analytics_modules"]["pack_alpha"]["analyst_score"], 99)
        self.assertGreater(len(result.telemetry_history), 0)

        artifacts = save_job_artifacts(self.temp_dir, summary, result, logs)
        self.assertIn("telemetry", artifacts)
        self.assertTrue(os.path.exists(artifacts["telemetry"]))


if __name__ == "__main__":
    unittest.main()

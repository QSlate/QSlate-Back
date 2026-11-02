import unittest
import pandas as pd
import numpy as np
from simulator.core.strategy import Strategy
from simulator.core.context import Context
from simulator.core.engine import BacktestEngine


def _make_dummy_candles(n: int = 100) -> pd.DataFrame:
    dates = pd.date_range("2024-01-01", periods=n, freq="1h", tz="UTC")
    prices = 100.0 + np.cumsum(np.random.normal(0, 1, n))
    df = pd.DataFrame(
        {
            "Open": prices,
            "High": prices + 0.5,
            "Low": prices - 0.5,
            "Close": prices,
            "Volume": 1000.0,
        },
        index=dates,
    )
    df.index.name = "Datetime"
    return df


class DummyStrategy(Strategy):
    trades_executed = 0

    def on_bar(self, ctx: Context):
        if not ctx.has_position and len(ctx.history) > 10:
            ctx.buy(usd_amount=1000.0, stop_loss=ctx.price * 0.95, take_profit=ctx.price * 1.05)
            self.trades_executed += 1


class TestBacktestEngine(unittest.TestCase):
    def test_backtest_engine_execution(self):
        df = _make_dummy_candles(100)
        strat = DummyStrategy()
        engine = BacktestEngine(data=df, strategy=strat, initial_capital=10000.0, window=10)
        res = engine.run()

        self.assertGreater(strat.trades_executed, 0)
        self.assertGreater(len(res.trades), 0)
        self.assertEqual(len(res.equity_history), 90)
        self.assertIn("win_rate_pct", res.metrics)
        self.assertIn("return_pct", res.metrics)

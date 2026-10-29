import unittest
from datetime import datetime, timezone
from simulator.core.models import Candle, OrderSide
from simulator.core.portfolio import Portfolio


class TestPortfolio(unittest.TestCase):
    def test_portfolio_initialization(self):
        p = Portfolio(initial_capital=5000.0)
        self.assertEqual(p.cash, 5000.0)
        self.assertEqual(p.equity, 5000.0)
        self.assertEqual(p.available_cash, 5000.0)
        self.assertEqual(len(p.positions), 0)

    def test_open_and_close_long_with_fees(self):
        p = Portfolio(initial_capital=10000.0, commission_rate=0.001, slippage_bps=10.0)
        now = datetime.now(timezone.utc)

        pos = p.open_position(
            side=OrderSide.LONG,
            price=100.0,
            size_usd=1000.0,
            entry_date=now,
        )
        self.assertIsNotNone(pos)
        self.assertEqual(pos.entry_price, 100.1)
        self.assertEqual(p.cash, 9999.0)

        trade = p.close_position(pos, price=110.0, exit_date=now, reason="Take Profit")
        self.assertGreater(trade.pnl_usd, 0)
        self.assertEqual(trade.fee_usd, 2.0)
        self.assertEqual(len(p.positions), 0)
        self.assertEqual(len(p.closed_trades), 1)

    def test_auto_stop_loss(self):
        p = Portfolio(initial_capital=10000.0)
        now = datetime.now(timezone.utc)

        pos = p.open_position(
            side=OrderSide.LONG,
            price=100.0,
            size_usd=1000.0,
            stop_loss=95.0,
            entry_date=now,
        )
        self.assertIsNotNone(pos)

        candle_hit = Candle(timestamp=now, open=98.0, high=99.0, low=94.0, close=96.0)
        closed = p.check_auto_stops(candle_hit)
        self.assertEqual(len(closed), 1)
        self.assertEqual(closed[0].exit_reason, "Stop Loss")
        self.assertEqual(closed[0].exit_price, 95.0)
        self.assertEqual(len(p.positions), 0)

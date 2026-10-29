from dataclasses import dataclass, field
from typing import List, Dict, Any, Optional
import pandas as pd

from simulator.core.models import Candle, Trade, OrderSide
from simulator.core.portfolio import Portfolio
from simulator.core.context import Context, ActionOrder
from simulator.core.strategy import Strategy
from simulator.metrics.analytics import calculate_metrics


@dataclass
class SimulationResult:
    metrics: Dict[str, Any]
    trades: List[Trade]
    equity_history: List[Dict[str, Any]]
    telemetry_history: List[Dict[str, Any]] = field(default_factory=list)


class BacktestEngine:
    """Deterministic, bar-by-bar backtesting simulation engine."""

    def __init__(
        self,
        data: pd.DataFrame,
        strategy: Strategy,
        initial_capital: float = 10000.0,
        window: int = 20,
        commission_rate: float = 0.0,
        slippage_bps: float = 0.0,
        custom_indicators: Optional[Dict[str, Any]] = None,
    ):
        self.data = data
        self.strategy = strategy
        self.initial_capital = float(initial_capital)
        self.window = max(int(window), 1)
        self.commission_rate = float(commission_rate)
        self.slippage_bps = float(slippage_bps)
        self.custom_indicators = custom_indicators or {}

    def run(self) -> SimulationResult:
        n_rows = len(self.data)
        if n_rows <= self.window:
            raise ValueError(
                f"Data length ({n_rows}) is smaller than or equal to window size ({self.window})."
            )

        portfolio = Portfolio(
            initial_capital=self.initial_capital,
            commission_rate=self.commission_rate,
            slippage_bps=self.slippage_bps,
        )

        # Initial context for on_init
        init_candle = Candle.from_row(self.data.index[self.window - 1], self.data.iloc[self.window - 1])
        init_ctx = Context(
            candle=init_candle,
            history=self.data.iloc[: self.window],
            available_cash=portfolio.available_cash,
            equity=portfolio.equity,
            positions=list(portfolio.positions),
            custom_indicators=self.custom_indicators,
        )
        self.strategy.on_init(init_ctx)

        telemetry_history: List[Dict[str, Any]] = []

        # Main bar-by-bar simulation loop
        for i in range(self.window, n_rows):
            timestamp = self.data.index[i]
            row = self.data.iloc[i]
            candle = Candle.from_row(timestamp, row)

            # 1. Automatic stop-loss, take-profit, timeout checks
            portfolio.check_auto_stops(candle)

            # 2. Build context for current step
            visible_history = self.data.iloc[i - self.window : i + 1]
            ctx = Context(
                candle=candle,
                history=visible_history,
                available_cash=portfolio.available_cash,
                equity=portfolio.equity,
                positions=list(portfolio.positions),
                custom_indicators=self.custom_indicators,
            )

            # 3. Strategy execution
            self.strategy.on_bar(ctx)

            # Collect in-strategy telemetry if any
            telemetry_snapshot = ctx.get_telemetry_snapshot()
            if telemetry_snapshot:
                iso_ts = candle.timestamp.isoformat() if hasattr(candle.timestamp, "isoformat") else str(candle.timestamp)
                telemetry_history.append({"timestamp": iso_ts, **telemetry_snapshot})

            # 4. Process strategy actions
            for action in ctx._pending_actions:
                if action.action == "OPEN":
                    portfolio.open_position(
                        side=action.side or OrderSide.LONG,
                        price=candle.close,
                        size_usd=action.size_usd,
                        leverage=action.leverage,
                        stop_loss=action.stop_loss,
                        take_profit=action.take_profit,
                        timeout_bars=action.timeout_bars,
                        entry_date=candle.timestamp,
                    )
                elif action.action == "CLOSE":
                    target_pos = next((p for p in portfolio.positions if p.id == action.position_id), None)
                    if target_pos:
                        portfolio.close_position(
                            position=target_pos,
                            price=candle.close,
                            exit_date=candle.timestamp,
                            reason=action.reason,
                        )

            # 5. Snapshot equity at close of bar
            portfolio.record_snapshot(candle)

        # End of simulation: Force close remaining open positions
        final_timestamp = self.data.index[-1]
        final_price = float(self.data["Close"].iloc[-1])
        portfolio.close_all(final_price, final_timestamp, reason="End of Data")

        # Call on_stop
        final_candle = Candle.from_row(final_timestamp, self.data.iloc[-1])
        stop_ctx = Context(
            candle=final_candle,
            history=self.data.iloc[-self.window :],
            available_cash=portfolio.available_cash,
            equity=portfolio.equity,
            positions=[],
            custom_indicators=self.custom_indicators,
        )
        stop_metrics = self.strategy.on_stop(stop_ctx)

        # Calculate analytics
        metrics = calculate_metrics(
            trades=portfolio.closed_trades,
            equity_history=portfolio.equity_history,
            initial_capital=self.initial_capital,
        )

        # Strategy-level post-processing hook
        df_trades = pd.DataFrame([t.to_dict() for t in portfolio.closed_trades]) if portfolio.closed_trades else pd.DataFrame()
        df_equity = pd.DataFrame(portfolio.equity_history) if portfolio.equity_history else pd.DataFrame()
        strategy_custom_metrics = self.strategy.custom_metrics(df_trades, df_equity, self.initial_capital)

        strategy_metrics_combined = {}
        if isinstance(stop_metrics, dict):
            strategy_metrics_combined.update(stop_metrics)
        if isinstance(strategy_custom_metrics, dict):
            strategy_metrics_combined.update(strategy_custom_metrics)

        if strategy_metrics_combined:
            metrics["strategy_metrics"] = strategy_metrics_combined

        return SimulationResult(
            metrics=metrics,
            trades=portfolio.closed_trades,
            equity_history=portfolio.equity_history,
            telemetry_history=telemetry_history,
        )

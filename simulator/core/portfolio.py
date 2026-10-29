from datetime import datetime
from typing import List, Optional, Dict, Any

from simulator.core.models import Candle, OrderSide, Position, Trade


class Portfolio:
    """Manages cash, margin, open positions, closed trades, and equity curve."""

    def __init__(
        self,
        initial_capital: float = 10000.0,
        commission_rate: float = 0.0,
        slippage_bps: float = 0.0,
    ):
        self.initial_capital = float(initial_capital)
        self.cash = float(initial_capital)
        self.commission_rate = float(commission_rate)
        self.slippage_pct = float(slippage_bps) / 10000.0

        self.positions: List[Position] = []
        self.closed_trades: List[Trade] = []
        self.equity_history: List[Dict[str, Any]] = []
        self._position_counter = 1

    @property
    def equity(self) -> float:
        unrealized = sum(p.unrealized_pnl_usd for p in self.positions)
        return self.cash + unrealized

    @property
    def margin_used(self) -> float:
        return sum(p.margin_usd for p in self.positions)

    @property
    def available_cash(self) -> float:
        return self.cash - self.margin_used

    def open_position(
        self,
        side: OrderSide,
        price: float,
        size_usd: float,
        leverage: float = 1.0,
        stop_loss: Optional[float] = None,
        take_profit: Optional[float] = None,
        timeout_bars: Optional[int] = None,
        entry_date: Optional[datetime] = None,
    ) -> Optional[Position]:
        leverage = max(leverage, 1.0)
        margin_needed = size_usd / leverage
        open_fee = size_usd * self.commission_rate

        if self.available_cash < (margin_needed + open_fee):
            return None

        # Apply slippage on entry
        if side == OrderSide.LONG:
            effective_price = price * (1.0 + self.slippage_pct)
        else:
            effective_price = price * (1.0 - self.slippage_pct)

        self.cash -= open_fee

        pos_id = f"POS_{self._position_counter}"
        self._position_counter += 1

        position = Position(
            id=pos_id,
            side=side,
            entry_date=entry_date or datetime.now(),
            entry_price=effective_price,
            size_usd=size_usd,
            leverage=leverage,
            stop_loss=stop_loss,
            take_profit=take_profit,
            timeout_bars=timeout_bars,
            bars_held=0,
        )
        self.positions.append(position)
        return position

    def close_position(
        self,
        position: Position,
        price: float,
        exit_date: datetime,
        reason: str = "Manual Exit",
    ) -> Trade:
        # Apply slippage on exit
        if position.side == OrderSide.LONG:
            effective_price = price * (1.0 - self.slippage_pct)
            raw_pct = (effective_price - position.entry_price) / position.entry_price
        else:
            effective_price = price * (1.0 + self.slippage_pct)
            raw_pct = (position.entry_price - effective_price) / position.entry_price

        gross_pnl_usd = position.size_usd * raw_pct * position.leverage
        close_fee = position.size_usd * self.commission_rate
        open_fee = position.size_usd * self.commission_rate
        total_fee = open_fee + close_fee
        net_pnl_usd = gross_pnl_usd - close_fee
        pnl_pct = (net_pnl_usd / position.margin_usd) * 100.0

        # Update cash
        self.cash += gross_pnl_usd - close_fee

        if position in self.positions:
            self.positions.remove(position)

        trade = Trade(
            id=position.id,
            side=position.side,
            entry_date=position.entry_date,
            exit_date=exit_date,
            entry_price=position.entry_price,
            exit_price=effective_price,
            size_usd=position.size_usd,
            leverage=position.leverage,
            pnl_usd=net_pnl_usd,
            pnl_pct=pnl_pct,
            exit_reason=reason,
            fee_usd=total_fee,
        )
        self.closed_trades.append(trade)
        return trade

    def close_all(self, price: float, exit_date: datetime, reason: str = "Close All") -> List[Trade]:
        trades = []
        for pos in list(self.positions):
            trade = self.close_position(pos, price, exit_date, reason)
            trades.append(trade)
        return trades

    def check_auto_stops(self, candle: Candle) -> List[Trade]:
        """Check Stop Loss, Take Profit, and Bar Timeouts for all open positions."""
        closed = []
        for pos in list(self.positions):
            pos.bars_held += 1
            is_closed = False

            # 1. Timeout
            if pos.timeout_bars is not None and pos.bars_held >= pos.timeout_bars:
                trade = self.close_position(pos, candle.close, candle.timestamp, f"Timeout ({pos.timeout_bars} bars)")
                closed.append(trade)
                is_closed = True

            # 2. SL / TP
            if not is_closed:
                if pos.side == OrderSide.LONG:
                    if pos.stop_loss is not None and candle.low <= pos.stop_loss:
                        trade = self.close_position(pos, pos.stop_loss, candle.timestamp, "Stop Loss")
                        closed.append(trade)
                    elif pos.take_profit is not None and candle.high >= pos.take_profit:
                        trade = self.close_position(pos, pos.take_profit, candle.timestamp, "Take Profit")
                        closed.append(trade)
                elif pos.side == OrderSide.SHORT:
                    if pos.stop_loss is not None and candle.high >= pos.stop_loss:
                        trade = self.close_position(pos, pos.stop_loss, candle.timestamp, "Stop Loss")
                        closed.append(trade)
                    elif pos.take_profit is not None and candle.low <= pos.take_profit:
                        trade = self.close_position(pos, pos.take_profit, candle.timestamp, "Take Profit")
                        closed.append(trade)

        return closed

    def record_snapshot(self, candle: Candle) -> None:
        """Record equity and capital snapshot at current candle."""
        for pos in self.positions:
            pos.update_pnl(candle.close)

        total_unrealized = sum(p.unrealized_pnl_usd for p in self.positions)
        current_equity = self.cash + total_unrealized

        self.equity_history.append(
            {
                "timestamp": candle.timestamp.isoformat() if hasattr(candle.timestamp, "isoformat") else str(candle.timestamp),
                "equity": round(current_equity, 2),
                "cash": round(self.cash, 2),
                "margin_used": round(self.margin_used, 2),
                "unrealized_pnl": round(total_unrealized, 2),
                "open_positions": len(self.positions),
            }
        )

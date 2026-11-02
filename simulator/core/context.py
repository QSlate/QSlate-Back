from dataclasses import dataclass
from typing import Optional, List, Any, Dict
import pandas as pd

from simulator.core.models import Candle, OrderSide, Position
from simulator.indicators.registry import IndicatorAccessor


@dataclass
class ActionOrder:
    action: str  # "OPEN" or "CLOSE"
    side: Optional[OrderSide] = None
    size_usd: float = 0.0
    leverage: float = 1.0
    stop_loss: Optional[float] = None
    take_profit: Optional[float] = None
    timeout_bars: Optional[int] = None
    position_id: Optional[str] = None
    reason: str = "Strategy Request"


class Context:
    """Trading context provided to user strategies during simulation."""

    def __init__(
        self,
        candle: Candle,
        history: pd.DataFrame,
        available_cash: float,
        equity: float,
        positions: List[Position],
        custom_indicators: Optional[Dict[str, Any]] = None,
    ):
        self.candle: Candle = candle
        self.history: pd.DataFrame = history
        self.available_cash: float = available_cash
        self.equity: float = equity
        self._positions: List[Position] = positions
        self.indicators: IndicatorAccessor = IndicatorAccessor(history, custom_indicators)
        self._pending_actions: List[ActionOrder] = []
        self._telemetry: Dict[str, Any] = {}

    def record(self, **kwargs: Any) -> None:
        """Records custom metric data or indicators computed during this candle.

        These telemetry points are collected over time and exported to telemetry.json.
        Example:
            ctx.record(ai_confidence=0.87, regime="trending", my_metric=12.4)
        """
        for k, v in kwargs.items():
            # Convert non-serializable objects (like numpy floats) to native types
            if hasattr(v, "item"):
                v = v.item()
            self._telemetry[k] = v

    def get_telemetry_snapshot(self) -> Dict[str, Any]:
        """Returns recorded telemetry for current candle."""
        return dict(self._telemetry)

    @property
    def price(self) -> float:
        """Shorthand for the current candle close price."""
        return self.candle.close

    @property
    def capital(self) -> float:
        """Available cash for opening new positions."""
        return self.available_cash

    @property
    def positions(self) -> List[Position]:
        """List of currently open positions."""
        return self._positions

    @property
    def has_position(self) -> bool:
        """True if there is at least one active position."""
        return len(self._positions) > 0

    @property
    def position(self) -> Optional[Position]:
        """First active position, or None if no positions are open."""
        return self._positions[0] if self._positions else None

    def buy(
        self,
        usd_amount: float,
        leverage: float = 1.0,
        stop_loss: Optional[float] = None,
        take_profit: Optional[float] = None,
        timeout_bars: Optional[int] = None,
    ) -> None:
        """Open a LONG position."""
        self._pending_actions.append(
            ActionOrder(
                action="OPEN",
                side=OrderSide.LONG,
                size_usd=float(usd_amount),
                leverage=float(leverage),
                stop_loss=stop_loss,
                take_profit=take_profit,
                timeout_bars=timeout_bars,
            )
        )

    def sell(
        self,
        usd_amount: float,
        leverage: float = 1.0,
        stop_loss: Optional[float] = None,
        take_profit: Optional[float] = None,
        timeout_bars: Optional[int] = None,
    ) -> None:
        """Open a SHORT position."""
        self._pending_actions.append(
            ActionOrder(
                action="OPEN",
                side=OrderSide.SHORT,
                size_usd=float(usd_amount),
                leverage=float(leverage),
                stop_loss=stop_loss,
                take_profit=take_profit,
                timeout_bars=timeout_bars,
            )
        )

    def close(self, position_id: Optional[str] = None, reason: str = "Strategy Exit") -> None:
        """Close a specific position, or the first active one if position_id is omitted."""
        target_id = position_id or (self.position.id if self.position else None)
        if target_id:
            self._pending_actions.append(
                ActionOrder(action="CLOSE", position_id=target_id, reason=reason)
            )

    def close_all(self, reason: str = "Strategy Exit") -> None:
        """Close all currently active positions."""
        for pos in self._positions:
            self._pending_actions.append(
                ActionOrder(action="CLOSE", position_id=pos.id, reason=reason)
            )

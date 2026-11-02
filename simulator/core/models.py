from dataclasses import dataclass, field
from datetime import datetime
from enum import Enum
from typing import Optional, Dict, Any


class OrderSide(str, Enum):
    LONG = "long"
    SHORT = "short"


class TradeStatus(str, Enum):
    OPEN = "open"
    CLOSED = "closed"


@dataclass
class Candle:
    timestamp: datetime
    open: float
    high: float
    low: float
    close: float
    volume: float = 0.0

    @classmethod
    def from_row(cls, timestamp: Any, row: Any) -> "Candle":
        return cls(
            timestamp=timestamp,
            open=float(row["Open"]),
            high=float(row["High"]),
            low=float(row["Low"]),
            close=float(row["Close"]),
            volume=float(row.get("Volume", 0.0)),
        )


@dataclass
class Position:
    id: str
    side: OrderSide
    entry_date: datetime
    entry_price: float
    size_usd: float
    leverage: float = 1.0
    stop_loss: Optional[float] = None
    take_profit: Optional[float] = None
    timeout_bars: Optional[int] = None
    bars_held: int = 0
    unrealized_pnl_usd: float = 0.0
    unrealized_pnl_pct: float = 0.0

    @property
    def margin_usd(self) -> float:
        lev = max(self.leverage, 1.0)
        return self.size_usd / lev

    def update_pnl(self, current_price: float) -> None:
        if self.side == OrderSide.LONG:
            raw_pct = (current_price - self.entry_price) / self.entry_price
        else:
            raw_pct = (self.entry_price - current_price) / self.entry_price

        self.unrealized_pnl_pct = raw_pct * self.leverage * 100.0
        self.unrealized_pnl_usd = self.size_usd * raw_pct * self.leverage


@dataclass
class Trade:
    id: str
    side: OrderSide
    entry_date: datetime
    exit_date: datetime
    entry_price: float
    exit_price: float
    size_usd: float
    leverage: float
    pnl_usd: float
    pnl_pct: float
    exit_reason: str
    fee_usd: float = 0.0

    def to_dict(self) -> Dict[str, Any]:
        return {
            "id": self.id,
            "side": self.side.value if isinstance(self.side, OrderSide) else str(self.side),
            "entry_date": self.entry_date.isoformat() if hasattr(self.entry_date, "isoformat") else str(self.entry_date),
            "exit_date": self.exit_date.isoformat() if hasattr(self.exit_date, "isoformat") else str(self.exit_date),
            "entry_price": round(self.entry_price, 4),
            "exit_price": round(self.exit_price, 4),
            "size_usd": round(self.size_usd, 2),
            "leverage": self.leverage,
            "pnl_usd": round(self.pnl_usd, 2),
            "pnl_pct": round(self.pnl_pct, 2),
            "fee_usd": round(self.fee_usd, 2),
            "exit_reason": self.exit_reason,
        }

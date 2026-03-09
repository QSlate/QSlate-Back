from typing import List, Optional

from pydantic import BaseModel, Field


class BacktestRequest(BaseModel):
    ticker: str = Field(..., description="The asset ticker to backtest (e.g., AAPL)")
    initial_capital: float = Field(10000.0, description="The starting balance.")
    window: int = Field(10, description="The number of historical rows passed to the strategy at each step.")
    indicators: Optional[List[str]] = Field(
        default_factory=list,
        description="List of technical indicators to calculate. Hit GET /api/options to see available ones.",
    )
    strategy_code: str = Field(..., description="The raw Python code string containing the strategy function.")
    strategy_function_name: str = Field(
        "custom_strategy",
        description="Must exactly match the name of the function inside the strategy_code.",
    )
    requested_stats: Optional[List[str]] = Field(
        None,
        description="Exact names of the statistics to evaluate. Hit GET /api/options to see available ones. 'None' means all.",
    )
    sort_trades_by: Optional[str] = Field(
        "date",
        description="How to order output trades: 'date', 'pnl_high_to_low', or 'pnl_low_to_high'.",
        pattern="^(date|pnl_high_to_low|pnl_low_to_high)$",
    )
    top_trades: Optional[int] = Field(
        None,
        description="Limit the total number of trades returned. 'None' means all trades.",
        ge=1,
    )

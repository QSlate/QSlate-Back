from typing import Any, Dict, Optional
import pandas as pd

from simulator.core.context import Context


class Strategy:
    """Base class for writing modular, event-driven trading strategies.

    Subclasses can define default parameters as class attributes and override
    the lifecycle methods: `on_init`, `on_bar`, `on_stop`, and optionally `custom_metrics`.
    """

    def __init__(self, **params: Any):
        # Override class-level attributes with any user-provided parameters
        for key, value in params.items():
            setattr(self, key, value)

    def on_init(self, ctx: Context) -> None:
        """Called once before the simulation begins."""
        pass

    def on_bar(self, ctx: Context) -> None:
        """Called on every new candle."""
        pass

    def on_stop(self, ctx: Context) -> Optional[Dict[str, Any]]:
        """Called once after the simulation finishes.

        Can optionally return a dictionary of custom metrics to include in summary.json.
        """
        return None

    def custom_metrics(
        self,
        trades: pd.DataFrame,
        equity_curve: pd.DataFrame,
        initial_capital: float,
    ) -> Dict[str, Any]:
        """Optional post-backtest analysis hook called after trades and equity curve are computed."""
        return {}

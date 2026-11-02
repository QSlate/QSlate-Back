from typing import Union, Tuple, Optional, Dict, Any
import numpy as np
import pandas as pd


def calc_sma(data: Union[pd.DataFrame, pd.Series], period: int = 20) -> pd.Series:
    series = data["Close"] if isinstance(data, pd.DataFrame) else data
    return series.rolling(window=period).mean()


def calc_ema(data: Union[pd.DataFrame, pd.Series], period: int = 20) -> pd.Series:
    series = data["Close"] if isinstance(data, pd.DataFrame) else data
    return series.ewm(span=period, adjust=False).mean()


def calc_rsi(data: Union[pd.DataFrame, pd.Series], period: int = 14) -> pd.Series:
    series = data["Close"] if isinstance(data, pd.DataFrame) else data
    delta = series.diff()
    gain = delta.where(delta > 0, 0.0).rolling(window=period).mean()
    loss = (-delta.where(delta < 0, 0.0)).rolling(window=period).mean()
    rs = gain / loss.replace(0, np.nan)
    rsi = 100.0 - (100.0 / (1.0 + rs))
    return rsi.fillna(50.0)


def calc_volatility(data: Union[pd.DataFrame, pd.Series], period: int = 20) -> pd.Series:
    series = data["Close"] if isinstance(data, pd.DataFrame) else data
    return series.pct_change().rolling(window=period).std() * 100.0


def calc_atr(data: pd.DataFrame, period: int = 14) -> pd.Series:
    high = data["High"]
    low = data["Low"]
    close = data["Close"]
    prev_close = close.shift(1)

    tr1 = high - low
    tr2 = (high - prev_close).abs()
    tr3 = (low - prev_close).abs()
    true_range = pd.concat([tr1, tr2, tr3], axis=1).max(axis=1)
    return true_range.rolling(window=period).mean()


def calc_bollinger_bands(
    data: Union[pd.DataFrame, pd.Series], period: int = 20, std_dev: float = 2.0
) -> Tuple[pd.Series, pd.Series, pd.Series]:
    series = data["Close"] if isinstance(data, pd.DataFrame) else data
    sma = series.rolling(window=period).mean()
    std = series.rolling(window=period).std()
    upper = sma + (std * std_dev)
    lower = sma - (std * std_dev)
    return upper, sma, lower


def calc_macd(
    data: Union[pd.DataFrame, pd.Series], fast: int = 12, slow: int = 26, signal: int = 9
) -> Tuple[pd.Series, pd.Series, pd.Series]:
    series = data["Close"] if isinstance(data, pd.DataFrame) else data
    ema_fast = series.ewm(span=fast, adjust=False).mean()
    ema_slow = series.ewm(span=slow, adjust=False).mean()
    macd_line = ema_fast - ema_slow
    signal_line = macd_line.ewm(span=signal, adjust=False).mean()
    histogram = macd_line - signal_line
    return macd_line, signal_line, histogram


INDICATOR_REGISTRY = {
    "SMA_20": lambda df: calc_sma(df, 20),
    "SMA_50": lambda df: calc_sma(df, 50),
    "RSI_14": lambda df: calc_rsi(df, 14),
    "Volatility_20": lambda df: calc_volatility(df, 20),
}


class IndicatorAccessor:
    """Helper class passed to strategy via Context to compute indicators easily on visible data."""

    def __init__(
        self,
        df_history: pd.DataFrame,
        custom_registry: Optional[Dict[str, Any]] = None,
    ):
        self._df = df_history
        self._custom_registry = custom_registry or {}

    def register(self, name: str, func: Any) -> None:
        """Register a custom indicator function dynamically."""
        self._custom_registry[name] = func

    def sma(self, period: int = 20) -> pd.Series:
        return calc_sma(self._df, period)

    def ema(self, period: int = 20) -> pd.Series:
        return calc_ema(self._df, period)

    def rsi(self, period: int = 14) -> pd.Series:
        return calc_rsi(self._df, period)

    def volatility(self, period: int = 20) -> pd.Series:
        return calc_volatility(self._df, period)

    def atr(self, period: int = 14) -> pd.Series:
        return calc_atr(self._df, period)

    def bollinger(self, period: int = 20, std_dev: float = 2.0) -> Tuple[pd.Series, pd.Series, pd.Series]:
        return calc_bollinger_bands(self._df, period, std_dev)

    def macd(self, fast: int = 12, slow: int = 26, signal: int = 9) -> Tuple[pd.Series, pd.Series, pd.Series]:
        return calc_macd(self._df, fast, slow, signal)

    def __getattr__(self, name: str) -> Any:
        if "_custom_registry" in self.__dict__ and name in self._custom_registry:
            custom_fn = self._custom_registry[name]
            return lambda *args, **kwargs: custom_fn(self._df, *args, **kwargs)
        raise AttributeError(f"'IndicatorAccessor' object has no indicator named '{name}'")

    def __getitem__(self, name: str) -> Any:
        return getattr(self, name)

from typing import Optional, Dict, Any, List
from pydantic import BaseModel, Field, model_validator


class DatasetConfig(BaseModel):
    source: str = Field("local_csv", description="Type of dataset source: 'local_csv', 's3', etc.")
    file_path: Optional[str] = Field(None, description="Direct path to CSV data file.")
    ticker: Optional[str] = Field(None, description="Asset ticker symbol (e.g. 'AAPL').")
    start_date: Optional[str] = Field(None, description="Optional ISO start date filter.")
    end_date: Optional[str] = Field(None, description="Optional ISO end date filter.")


class StrategyConfig(BaseModel):
    source_code: Optional[str] = Field(None, description="Python source code containing Strategy subclass.")
    file_path: Optional[str] = Field(None, description="Path to .py file containing Strategy subclass.")
    class_name: Optional[str] = Field(None, description="Exact name of Strategy class. Auto-detected if omitted.")
    parameters: Dict[str, Any] = Field(default_factory=dict, description="Custom parameters injected into Strategy.")

    @model_validator(mode="after")
    def check_source_or_file(self):
        if not self.source_code and not self.file_path:
            raise ValueError("Either 'source_code' or 'file_path' must be provided for StrategyConfig.")
        return self


class BacktestConfig(BaseModel):
    initial_capital: float = Field(10000.0, description="Initial balance.")
    window: int = Field(20, description="Number of bars visible in historical window.", ge=1)
    commission_rate: float = Field(0.0, description="Trading commission percentage (e.g. 0.0005 = 0.05%).", ge=0.0)
    slippage_bps: float = Field(0.0, description="Slippage in basis points (e.g. 2 = 0.02%).", ge=0.0)


class MarketplaceIndicator(BaseModel):
    id: Optional[str] = Field(None, description="Marketplace identifier.")
    name: str = Field(..., description="Function name accessible via ctx.indicators.<name>.")
    source_code: Optional[str] = Field(None, description="Python source code containing def calculate(data, **params).")
    file_path: Optional[str] = Field(None, description="Path to .py file containing indicator function.")
    default_params: Dict[str, Any] = Field(default_factory=dict, description="Default parameters for indicator.")
    author: Optional[str] = Field(None, description="Author or marketplace provider.")

    @model_validator(mode="after")
    def check_source_or_file(self):
        if not self.source_code and not self.file_path:
            raise ValueError(f"Either 'source_code' or 'file_path' must be provided for indicator '{self.name}'.")
        return self


class AnalyticsModule(BaseModel):
    id: Optional[str] = Field(None, description="Marketplace identifier.")
    name: str = Field(..., description="Metric group or pack name.")
    source_code: Optional[str] = Field(
        None,
        description="Python code containing def analyze(trades, equity_curve, market_data, initial_capital).",
    )
    file_path: Optional[str] = Field(None, description="Path to .py file containing analyze function.")
    author: Optional[str] = Field(None, description="Author or marketplace provider.")

    @model_validator(mode="after")
    def check_source_or_file(self):
        if not self.source_code and not self.file_path:
            raise ValueError(f"Either 'source_code' or 'file_path' must be provided for analytics module '{self.name}'.")
        return self


class JobConfig(BaseModel):
    job_id: Optional[str] = Field(None, description="Unique identifier for the backtest run.")
    dataset: DatasetConfig = Field(..., description="Dataset input configuration.")
    strategy: StrategyConfig = Field(..., description="Strategy code and parameters.")
    marketplace_indicators: List[MarketplaceIndicator] = Field(
        default_factory=list,
        description="External/marketplace indicators loaded dynamically into ctx.indicators.",
    )
    analytics_modules: List[AnalyticsModule] = Field(
        default_factory=list,
        description="External/marketplace post-processing analytics modules.",
    )
    backtest: BacktestConfig = Field(default_factory=BacktestConfig, description="Backtesting environment settings.")
    output_dir: Optional[str] = Field(None, description="Directory where JSON and log artifacts are saved.")


class JobSummary(BaseModel):
    job_id: str
    status: str
    execution_time_seconds: float
    metrics: Optional[Dict[str, Any]] = None
    error: Optional[str] = None

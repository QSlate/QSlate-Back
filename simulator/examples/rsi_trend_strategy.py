from simulator import Strategy, Context


class RsiTrendStrategy(Strategy):
    """Example modular strategy using indicators, parameters, and stop management."""

    # Configurable parameters (can be overridden from job_config.json)
    rsi_period: int = 14
    sma_period: int = 50
    rsi_oversold: float = 30.0
    rsi_overbought: float = 70.0
    allocation_usd: float = 1500.0
    stop_loss_pct: float = 2.0
    take_profit_pct: float = 5.0
    timeout_bars: int = 48  # 48 hours for 1H data

    def on_init(self, ctx: Context) -> None:
        print(f"Initializing {self.__class__.__name__} with allocation ${self.allocation_usd}")

    def on_bar(self, ctx: Context) -> None:
        # 1. Compute indicators
        rsi_series = ctx.indicators.rsi(self.rsi_period)
        sma_series = ctx.indicators.sma(self.sma_period)

        if len(rsi_series) < max(self.rsi_period, self.sma_period) + 1:
            return

        current_rsi = rsi_series.iloc[-1]
        current_sma = sma_series.iloc[-1]
        current_price = ctx.price

        # Record telemetry along the way
        ctx.record(rsi_val=float(current_rsi), sma_val=float(current_sma))

        # 2. Entry signal: oversold in an uptrend
        if not ctx.has_position:
            if current_rsi < self.rsi_oversold and current_price > current_sma:
                sl_price = current_price * (1.0 - self.stop_loss_pct / 100.0)
                tp_price = current_price * (1.0 + self.take_profit_pct / 100.0)
                print(f"[{ctx.candle.timestamp}] BUY Signal at ${current_price:.2f} (RSI: {current_rsi:.1f})")
                ctx.buy(
                    usd_amount=self.allocation_usd,
                    stop_loss=sl_price,
                    take_profit=tp_price,
                    timeout_bars=self.timeout_bars,
                )

        # 3. Exit signal: overbought
        elif ctx.has_position:
            if current_rsi > self.rsi_overbought:
                print(f"[{ctx.candle.timestamp}] CLOSE Signal at ${current_price:.2f} (RSI: {current_rsi:.1f})")
                ctx.close_all(reason="RSI Overbought")

    def on_stop(self, ctx: Context) -> None:
        print("Simulation ended. Final portfolio equity:", round(ctx.equity, 2))

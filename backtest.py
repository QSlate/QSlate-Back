import yfinance as yf
import pandas as pd
import numpy as np
from datetime import datetime, timedelta

# ==========================================
# 1. DATA FETCHER
# ==========================================
def fetch_hourly_data(ticker):
    max_days = 720
    end_date = datetime.now()
    start_date = end_date - timedelta(days=max_days)

    str_start = start_date.strftime('%Y-%m-%d')
    str_end = end_date.strftime('%Y-%m-%d')

    print(f"Fetching hourly data for {ticker} from {str_start} to {str_end}...")

    data = yf.download(
        tickers=ticker,
        start=str_start,
        end=str_end,
        interval="1h",
        auto_adjust=True
    )

    if data.empty:
        print("Error: No data fetched. Check the ticker or connection.")
        return

    data.index = data.index.strftime('%Y-%m-%d %H:%M:%S')

    file_name = f"DATA_1H_{ticker}.csv"
    data.to_csv(file_name)

    print("-" * 30)
    print(f"File created: {file_name}")
    print(f"Total rows: {len(data)}")

# ==========================================
# 2. INDICATORS REGISTRY
# ==========================================
def calc_sma(df, period):
    return df['Close'].rolling(window=period).mean()

def calc_rsi(df, period=14):
    delta = df['Close'].diff()
    gain = (delta.where(delta > 0, 0)).rolling(window=period).mean()
    loss = (-delta.where(delta < 0, 0)).rolling(window=period).mean()
    rs = gain / loss
    return 100 - (100 / (1 + rs))

def calc_volatility(df, period=20):
    return df['Close'].pct_change().rolling(window=period).std() * 100

INDICATOR_REGISTRY = {
    'SMA_20': lambda df: calc_sma(df, 20),
    'SMA_50': lambda df: calc_sma(df, 50),
    'RSI_14': lambda df: calc_rsi(df, 14),
    'Volatility_20': lambda df: calc_volatility(df, 20)
}

# ==========================================
# 3. BACKTEST ENGINE
# ==========================================
def run_backtest(csv_file, strategy_function, initial_capital=10000, window=10, requested_indicators=None):
    df = pd.read_csv(csv_file)
    df.rename(columns={df.columns[0]: 'Datetime'}, inplace=True)
    df['Datetime'] = pd.to_datetime(df['Datetime'], format='mixed', errors='coerce', utc=True)
    df = df.dropna(subset=['Datetime']).set_index('Datetime')

    for col in ['Open', 'High', 'Low', 'Close']:
        df[col] = pd.to_numeric(df.get(col), errors='coerce')
    df = df.sort_index()

    if requested_indicators:
        print(f"⚙️ Calculating indicators: {', '.join(requested_indicators)}...")
        for ind_name in requested_indicators:
            if ind_name in INDICATOR_REGISTRY:
                df[ind_name] = INDICATOR_REGISTRY[ind_name](df)
            else:
                print(f"⚠️ Ignored unknown indicator: {ind_name}")
        df = df.dropna()

    capital = initial_capital
    open_trades = []
    trade_history = []
    id_counter = 1

    def close_trade(trade, exit_price, reason, exit_date):
        nonlocal capital
        if trade['type'] == 'long':
            pnl_pct = (exit_price - trade['entry_price']) / trade['entry_price']
        else:
            pnl_pct = (trade['entry_price'] - exit_price) / trade['entry_price']

        pnl_usd = trade['size_usd'] * pnl_pct * trade['leverage']
        trade.update({
            'status': 'Closed',
            'exit_date': exit_date,
            'exit_price': round(exit_price, 2),
            'pnl_usd': round(pnl_usd, 2),
            'exit_reason': reason
        })
        capital += trade['size_usd'] + pnl_usd
        trade_history.append(trade)

    print(f"🚀 Starting session with ${initial_capital}...")

    for i in range(window, len(df)):
        current_date = df.index[i]
        candle = df.iloc[i]
        close_price = candle['Close']

        # 1. Automatic Management (Timeout, SL, TP)
        surviving_trades = []
        for trade in open_trades:
            trade_closed = False

            if trade.get('timeout'):
                if (current_date - trade['entry_date']).total_seconds() / 3600 >= trade['timeout']:
                    close_trade(trade, close_price, f"Timeout ({trade['timeout']}h)", current_date)
                    trade_closed = True

            if not trade_closed:
                if trade['type'] == 'long':
                    if trade.get('sl') and candle['Low'] <= trade['sl']:
                        close_trade(trade, trade['sl'], "Stop Loss", current_date)
                        trade_closed = True
                    elif trade.get('tp') and candle['High'] >= trade['tp']:
                        close_trade(trade, trade['tp'], "Take Profit", current_date)
                        trade_closed = True
                elif trade['type'] == 'short':
                    if trade.get('sl') and candle['High'] >= trade['sl']:
                        close_trade(trade, trade['sl'], "Stop Loss", current_date)
                        trade_closed = True
                    elif trade.get('tp') and candle['Low'] <= trade['tp']:
                        close_trade(trade, trade['tp'], "Take Profit", current_date)
                        trade_closed = True

            if not trade_closed:
                surviving_trades.append(trade)

        open_trades = surviving_trades

        # 2. Strategy Call
        visible_data = df.iloc[i - window : i + 1]
        instructions = strategy_function(visible_data, open_trades, capital)

        # Normalize and validate strategy instructions
        if instructions is None:
            instructions = []
        elif isinstance(instructions, dict):
            # Allow a single instruction dict to be returned
            instructions = [instructions]

        # At this point, instructions should be an iterable of dicts
        try:
            iter(instructions)
        except TypeError:
            raise TypeError(
                "strategy_function must return an iterable of instruction dicts, "
                "a single instruction dict, or None."
            )

        normalized_instructions = []
        for idx, inst in enumerate(instructions):
            if not isinstance(inst, dict):
                raise TypeError(
                    f"strategy_function returned a non-dict instruction at index {idx}: "
                    f"{type(inst).__name__}"
                )
            if 'action' not in inst:
                raise KeyError(
                    f"strategy_function returned an instruction without an 'action' key at index {idx}."
                )
            normalized_instructions.append(inst)

        instructions = normalized_instructions
        # 3. Instruction Execution
        for inst in instructions:
            if inst['action'] == 'CLOSE':
                for trade in open_trades:
                    if trade['id'] == inst['id']:
                        close_trade(trade, close_price, inst.get('reason', 'Strategy Request'), current_date)
                        open_trades.remove(trade)
                        break

            elif inst['action'] == 'OPEN' and capital >= inst['size_usd']:
                size = inst['size_usd']
                new_trade = {
                    'id': f"TRD_{id_counter}",
                    'entry_date': current_date,
                    'type': inst['type'],
                    'entry_price': close_price,
                    'size_usd': size,
                    'leverage': inst.get('leverage', 1),
                    'sl': inst.get('sl'),
                    'tp': inst.get('tp'),
                    'timeout': inst.get('timeout'),
                    'status': 'Open'
                }
                open_trades.append(new_trade)
                capital -= size
                id_counter += 1

    # End of Data - Force Close
    final_price = df['Close'].iloc[-1]
    final_date = df.index[-1]
    for trade in open_trades:
        close_trade(trade, final_price, 'End of Data', final_date)

    return pd.DataFrame(trade_history)

# ==========================================
# 4. STRATEGY EXAMPLE
# ==========================================
def my_rsi_strategy(history, open_trades, remaining_capital):
    instructions = []
    current_price = history['Close'].iloc[-1]
    current_rsi = history['RSI_14'].iloc[-1]
    current_sma = history['SMA_50'].iloc[-1]

    if len(open_trades) == 0 and remaining_capital >= 1000:
        if current_rsi < 30 and current_price > current_sma:
            instructions.append({
                'action': 'OPEN',
                'type': 'long',
                'size_usd': 1000,
                'sl': current_price * 0.98,
                'tp': current_price * 1.05
            })

    return instructions

# ==========================================
# 5. STATS & REPORT REGISTRY
# ==========================================
def calc_max_drawdown(df, init_cap):
    if df.empty: return 0.0
    df_sorted = df.sort_values('exit_date')
    # Track equity, starting at init_cap
    equity_curve = pd.concat([pd.Series([init_cap]), init_cap + df_sorted['pnl_usd'].cumsum()]).reset_index(drop=True)
    peak = equity_curve.cummax()
    drawdown = (peak - equity_curve) / peak
    return drawdown.max() * 100

def calc_sharpe_ratio(df, init_cap):
    if df.empty or len(df) < 2: return 0.0
    returns = df['pnl_usd'] / init_cap
    return returns.mean() / returns.std() if returns.std() != 0 else 0.0

def calc_max_margin_usd(df, init_cap):
    if df.empty: return 0.0
    df_calc = df.copy()
    df_calc['leverage'] = df_calc['leverage'].replace(0, 1)
    df_calc['margin'] = df_calc['size_usd'] / df_calc['leverage']

    events_in = pd.DataFrame({'date': df_calc['entry_date'], 'val': df_calc['margin']})
    events_out = pd.DataFrame({'date': df_calc['exit_date'], 'val': -df_calc['margin']})
    timeline = pd.concat([events_in, events_out]).sort_values('date')
    return timeline['val'].cumsum().max()

def calc_margin_utilization(df, init_cap):
    if df.empty: return 0.0
    df_calc = df.copy()
    df_calc['leverage'] = df_calc['leverage'].replace(0, 1)
    df_calc['margin'] = df_calc['size_usd'] / df_calc['leverage']

    events_in = pd.DataFrame({'date': df_calc['entry_date'], 'margin_change': df_calc['margin'], 'pnl_change': 0.0})
    events_out = pd.DataFrame({'date': df_calc['exit_date'], 'margin_change': -df_calc['margin'], 'pnl_change': df_calc['pnl_usd']})
    
    timeline = pd.concat([events_in, events_out]).sort_values('date')
    timeline['running_margin'] = timeline['margin_change'].cumsum()
    timeline['running_equity'] = init_cap + timeline['pnl_change'].cumsum()
    
    # Calculate margin util dynamically against running equity, fallback to 100% if equity is <= 0
    timeline['margin_util'] = np.where(
        timeline['running_equity'] > 0, 
        (timeline['running_margin'] / timeline['running_equity']) * 100, 
        100.0
    )
    return timeline['margin_util'].max()

def calc_fitness(df, init_cap):
    if df.empty: return 0.0
    ret = ((df['pnl_usd'].sum()) / init_cap) * 100
    dd = calc_max_drawdown(df, init_cap)
    return ret / dd if dd != 0 else ret

STATS_REGISTRY = {
    "Total Trades": lambda df, cap: len(df),
    "Win Rate (%)": lambda df, cap: (df['pnl_usd'] > 0).mean() * 100 if not df.empty else 0.0,
    "Long Count": lambda df, cap: len(df[df['type'] == 'long']),
    "Short Count": lambda df, cap: len(df[df['type'] == 'short']),
    "Final Capital ($)": lambda df, cap: cap + df['pnl_usd'].sum(),
    "Returns (%)": lambda df, cap: (df['pnl_usd'].sum() / cap) * 100,
    "Turnover": lambda df, cap: df['size_usd'].sum() / cap if not df.empty else 0.0,
    "Max Drawdown (%)": calc_max_drawdown,
    "Sharpe Ratio": calc_sharpe_ratio,
    "Max Margin ($)": calc_max_margin_usd,
    "Margin Util. (%)": calc_margin_utilization,
    "Fitness Score": calc_fitness
}

def generate_report(df_trades, initial_capital=10000, requested_stats=None, custom_stats=None):
    if df_trades.empty:
        # Return a Series to keep the return type consistent with the non-empty case
        return pd.Series({"Message": "No trades to analyze."})

    # Combine default registry and custom stats if any
    available_stats = STATS_REGISTRY.copy()
    if custom_stats:
        available_stats.update(custom_stats)

    if requested_stats is None:
        requested_stats = list(available_stats.keys())

    results = {}
    for stat_name in requested_stats:
        if stat_name in available_stats:
            calc_function = available_stats[stat_name]
            value = calc_function(df_trades, initial_capital)

            if isinstance(value, float):
                value = round(value, 3 if "Ratio" in stat_name or "Score" in stat_name else 2)

            results[stat_name] = value
        else:
            results[stat_name] = "Error: Unknown stat"

    return pd.Series(results)

# ==========================================
# 6. EXECUTION
# ==========================================
if __name__ == "__main__":
    # 1. Fetch data
    # fetch_hourly_data("AAPL")  # Uncomment to download data

    # 2. Run Backtest
    my_indicators = ['SMA_50', 'RSI_14']

    # Ensure you have 'DATA_1H_AAPL.csv' in your directory before running
    try:
        df_history = run_backtest(
            csv_file="DATA_1H_AAPL.csv",
            strategy_function=my_rsi_strategy,
            initial_capital=10000,
            window=5,
            requested_indicators=my_indicators
        )

        # 3. Generate Report
        my_stats = ["Total Trades", "Win Rate (%)", "Returns (%)", "Max Drawdown (%)", "Max Margin ($)"]
        express_report = generate_report(df_history, initial_capital=10000, requested_stats=my_stats)

        print("\n--- CUSTOM REPORT ---")
        print(express_report.to_string())

    except FileNotFoundError:
        print("\n⚠️ Note: Please run fetch_hourly_data('AAPL') first to generate the CSV file.")
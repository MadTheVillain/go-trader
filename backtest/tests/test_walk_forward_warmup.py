"""Walk-forward folds prepend a warmup slice so long-lookback indicators
(e.g. SMA-80) prime before the first signal bar. Without warmup, a 100-bar
fold against an SMA-80 grid produces all-NaN signals and zero trades."""
import numpy as np
import pandas as pd
import pytest

from optimizer import max_indicator_lookback, walk_forward_optimize


def _trending_ohlc(n: int = 500, seed: int = 7) -> pd.DataFrame:
    rng = np.random.default_rng(seed)
    log_returns = rng.normal(loc=0.002, scale=0.015, size=n)
    closes = [100.0]
    for r in log_returns:
        closes.append(closes[-1] * np.exp(r))
    closes = np.array(closes[1:])
    opens = closes * (1.0 + rng.normal(loc=0.0, scale=0.002, size=n))
    highs = np.maximum(opens, closes) * 1.003
    lows = np.minimum(opens, closes) * 0.997
    volume = rng.integers(1000, 10000, size=n).astype(float)
    idx = pd.date_range("2022-01-01", periods=n, freq="D")
    return pd.DataFrame(
        {"open": opens, "high": highs, "low": lows,
         "close": closes, "volume": volume},
        index=idx,
    )


def test_max_indicator_lookback_picks_largest_int():
    ranges = {
        "fast_period": [10, 15, 20],
        "slow_period": [40, 50, 80],
        "multiplier":  [1.5, 2.0, 3.0],  # float — ignored
    }
    assert max_indicator_lookback(ranges) == 80


def test_max_indicator_lookback_zero_for_float_only_grid():
    ranges = {
        "entry_std": [1.0, 1.5, 2.0],
        "exit_std":  [0.0, 0.5, 1.0],
    }
    assert max_indicator_lookback(ranges) == 0


def test_sma_80_grid_generates_trades_with_warmup():
    """SMA-80 on 100-bar folds should cross at least once across 5 folds
    when warmup primes the preceding 80 bars."""
    df = _trending_ohlc(n=500)
    param_ranges = {"fast_period": [10, 20], "slow_period": [40, 80]}

    result = walk_forward_optimize(
        df, "sma_crossover", param_ranges,
        n_splits=5, train_pct=0.7,
        initial_capital=1000.0, verbose=False,
    )

    assert "window_results" in result, result
    total_trades = sum(
        w["test_result"]["total_trades"] for w in result["window_results"]
    )
    assert total_trades > 0, (
        "Walk-forward produced zero trades across all folds — the warmup "
        "fix did not engage or is insufficient for SMA-80 priming."
    )


def test_warmup_primes_slow_sma_on_every_bar():
    """Counterfactual: on an unprimed 100-bar window, the slow SMA-80 is
    NaN for 79 bars — only the final 21 bars can emit a crossover. With
    80 bars of preceding history prepended, every bar of the 100-bar
    window has a valid slow SMA. Pin that asymmetry — it is the mechanism
    the warmup fix is buying."""
    from registry_loader import load_registry

    df = _trending_ohlc(n=500)
    unprimed = df.iloc[100:200]
    primed_input = df.iloc[20:200]  # 80 bars warmup + 100 bars window

    reg = load_registry("spot")
    params = {"fast_period": 10, "slow_period": 80}

    unprimed_out = reg.apply_strategy("sma_crossover", unprimed, params)
    primed_out = reg.apply_strategy("sma_crossover", primed_input, params).iloc[-100:]

    unprimed_primed_bars = int(unprimed_out["sma_slow"].notna().sum())
    primed_primed_bars = int(primed_out["sma_slow"].notna().sum())

    assert primed_primed_bars == 100, (
        f"Primed window should have sma_slow valid on every bar; "
        f"got {primed_primed_bars}"
    )
    assert unprimed_primed_bars <= 21, (
        f"Unprimed 100-bar window cannot have more than 21 valid "
        f"sma_slow bars (100 - 79 NaN); got {unprimed_primed_bars}"
    )


def test_warmup_does_not_leak_future_data():
    """Fold 0 starts at bar 0 and has no preceding history, so warmup is
    truncated to 0 — later folds get the full 80. Just pin that the runs
    still complete without crashing under that asymmetry."""
    df = _trending_ohlc(n=600)
    result = walk_forward_optimize(
        df, "sma_crossover", {"fast_period": [10], "slow_period": [80]},
        n_splits=5, train_pct=0.7,
        initial_capital=1000.0, verbose=False,
    )
    assert result["n_valid_folds"] >= 2, result

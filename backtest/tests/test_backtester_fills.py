"""
Regression tests for #302 C1: look-ahead bias in the backtester.

Live scheduler behavior: a signal computed on the close of bar t is read after
the bar closes and filled as a market order that lands at ~bar t+1's open.
The backtester must match this — filling at bar t's close on the same bar the
signal was produced creates a free look-ahead.
"""
import pandas as pd
import pytest

from backtester import Backtester


def _make_df(opens, highs, lows, closes, signals):
    n = len(closes)
    idx = pd.date_range("2024-01-01", periods=n, freq="D")
    return pd.DataFrame(
        {
            "open": opens,
            "high": highs,
            "low": lows,
            "close": closes,
            "signal": signals,
        },
        index=idx,
    )


def test_fill_uses_next_bar_open_not_signal_bar_close():
    """A buy signal on bar 2 must fill at bar 3's open, not bar 2's close."""
    # Crafted prices: bar 2 close (=100) is distinct from bar 3 open (=110).
    # If the backtester incorrectly fills at close, the entry price is 100.
    # Correct behavior (fill at next bar's open) puts the entry at 110.
    opens = [100, 100, 100, 110, 110, 110]
    highs = [101, 101, 101, 111, 111, 111]
    lows = [99, 99, 99, 109, 109, 109]
    closes = [100, 100, 100, 110, 110, 110]
    signals = [0, 0, 1, 0, 0, -1]

    df = _make_df(opens, highs, lows, closes, signals)
    bt = Backtester(initial_capital=1000.0, commission_pct=0.0, slippage_pct=0.0)
    results = bt.run(df, strategy_name="lookahead-check", save=False)

    assert len(results["trades"]) == 1
    trade = results["trades"][0]
    assert trade["entry_price"] == pytest.approx(110.0, rel=1e-9), (
        "Entry price must come from the bar *after* the signal bar's open "
        "(110), not the signal bar's close (100). A 100 here is look-ahead bias."
    )


def test_exit_uses_next_bar_open_not_signal_bar_close():
    """Exit on sell signal must fill at the following bar's open."""
    opens = [100, 100, 100, 100, 200, 300]
    highs = [101, 101, 101, 101, 201, 301]
    lows = [99, 99, 99, 99, 199, 299]
    closes = [100, 100, 100, 100, 200, 300]
    signals = [0, 1, 0, -1, 0, 0]

    df = _make_df(opens, highs, lows, closes, signals)
    bt = Backtester(initial_capital=1000.0, commission_pct=0.0, slippage_pct=0.0)
    results = bt.run(df, strategy_name="lookahead-exit", save=False)

    assert len(results["trades"]) == 1
    trade = results["trades"][0]
    # Entry is buy at bar 2's open (next bar after signal bar 1) → 100.
    assert trade["entry_price"] == pytest.approx(100.0, rel=1e-9)
    # Exit is sell at bar 4's open (next bar after signal bar 3) → 200,
    # NOT bar 3's close (= 100, which would show a 0% trade — look-ahead-free
    # exit sees the big gap up).
    assert trade["exit_price"] == pytest.approx(200.0, rel=1e-9), (
        "Exit must fill at the bar following the sell-signal bar's open (200), "
        "not the signal bar's close (100)."
    )


def test_falls_back_to_close_when_open_column_missing():
    """Legacy demos/tests feed only a 'close' column — preserve that path."""
    closes = [100, 100, 100, 110, 110, 110]
    signals = [0, 0, 1, 0, 0, -1]
    idx = pd.date_range("2024-01-01", periods=len(closes), freq="D")
    df = pd.DataFrame({"close": closes, "signal": signals}, index=idx)

    bt = Backtester(initial_capital=1000.0, commission_pct=0.0, slippage_pct=0.0)
    results = bt.run(df, strategy_name="legacy-close-only", save=False)

    assert len(results["trades"]) == 1
    # With no open column we fall back to close — signal shifted by 1 bar means
    # entry is at bar 3's close (=110), not bar 2's close (=100).
    assert results["trades"][0]["entry_price"] == pytest.approx(110.0, rel=1e-9)


def test_signal_on_final_bar_never_fills():
    """A signal emitted on the last bar has no following bar to fill on."""
    opens = [100, 100, 100]
    closes = [100, 100, 100]
    signals = [0, 0, 1]
    idx = pd.date_range("2024-01-01", periods=3, freq="D")
    df = pd.DataFrame(
        {"open": opens, "close": closes, "signal": signals}, index=idx
    )

    bt = Backtester(initial_capital=1000.0, commission_pct=0.0, slippage_pct=0.0)
    results = bt.run(df, strategy_name="last-bar-signal", save=False)

    assert results["total_trades"] == 0, (
        "Signal on the last bar has no next bar to fill on — must not trade."
    )

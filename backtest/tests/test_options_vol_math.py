"""
Regression tests for #302 C2: historical volatility must use log returns and
variance around the sample mean, matching ``numpy.std(log_returns, ddof=0)``
annualized by ``sqrt(365)``.
"""
import math

import numpy as np
import pytest

from backtest_options import calc_historical_vol


def _numpy_vol(closes, window):
    closes = np.asarray(closes[-(window + 1):], dtype=float)
    log_returns = np.log(closes[1:] / closes[:-1])
    return float(np.std(log_returns, ddof=0) * math.sqrt(365))


def test_matches_numpy_for_random_walk():
    rng = np.random.default_rng(42)
    # 120 days of a lognormal random walk — realistic-ish price path.
    log_returns = rng.normal(loc=0.0005, scale=0.03, size=120)
    closes = [100.0]
    for r in log_returns:
        closes.append(closes[-1] * math.exp(r))

    window = 30
    got = calc_historical_vol(closes, window=window)
    expected = _numpy_vol(closes, window=window)
    assert got == pytest.approx(expected, rel=1e-9), (
        "calc_historical_vol must match numpy.std(log_returns, ddof=0) * sqrt(365)."
    )


def test_trending_window_vol_matches_reference():
    """A strongly trending window has non-zero mean return — this is where the
    old ``sum(r**2)/n`` formula overstates vol. Verify the new formula matches
    the numpy reference (variance around the sample mean) within float tolerance.
    """
    # Monotonic 1%/day rally — mean log return is clearly non-zero.
    closes = [100.0 * (1.01 ** i) for i in range(60)]
    got = calc_historical_vol(closes, window=30)
    expected = _numpy_vol(closes, window=30)
    assert got == pytest.approx(expected, rel=1e-9)

    # Realised vol of a perfectly trending series is ~0 around the mean.
    # (Exactly 0 would fail downstream Black-Scholes; accept < 1%.)
    assert got < 0.01


def test_short_history_returns_default():
    assert calc_historical_vol([100.0, 101.0, 102.0], window=14) == 0.5


def test_flat_prices_give_zero_vol():
    closes = [100.0] * 50
    assert calc_historical_vol(closes, window=14) == pytest.approx(0.0)

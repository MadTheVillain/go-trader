# Delta-Neutral Funding Rate Strategy

**Issue:** #65
**Date:** 2026-04-01
**Approach:** Signal-only (A) — strategy function outputs enter/exit signals based on funding rate data. No cross-platform execution orchestration.

## Strategy Function

**Location:** `shared_strategies/spot/strategies.py`
**Registry name:** `delta_neutral_funding`
**Also registered in:** `shared_strategies/futures/strategies.py`

### Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| `entry_threshold` | `0.01` | 7d avg funding rate % per 8h to enter (0.01% = ~13% APY) |
| `exit_threshold` | `0.005` | 7d avg funding below this triggers exit |
| `drift_threshold` | `2.0` | Delta drift % that triggers rebalance indicator |
| `current_funding_rate` | `0.0` | Current funding rate, injected by check script |
| `avg_funding_rate_7d` | `0.0` | 7-day average funding rate, injected by check script |

### Signal Logic

- `1` (enter): `avg_funding_rate_7d > entry_threshold` (transition from below to above)
- `-1` (exit): `avg_funding_rate_7d < exit_threshold`
- `0` (hold): otherwise

### Indicator Columns

| Column | Description |
|--------|-------------|
| `funding_rate` | Current funding rate |
| `avg_funding_7d` | 7-day average funding rate |
| `funding_apy` | Annualized rate: `avg * 3 * 365 * 100` |
| `delta_drift_pct` | Placeholder (0.0 in signal-only mode) |
| `rebalance_needed` | 1.0 if drift > threshold, 0.0 otherwise |

The function receives a standard OHLCV DataFrame for registry interface consistency. Core logic uses the injected funding rate params. DataFrame is passed through with signal/indicator columns added.

## Hyperliquid Adapter Changes

**File:** `platforms/hyperliquid/adapter.py`

### New Methods

**`get_funding_rate(symbol: str) -> float`**
- Calls `self._info.meta()` to get current predicted funding rate for the symbol
- Returns the funding rate as a float (e.g., 0.0001 = 0.01%)

**`get_funding_history(symbol: str, days: int = 7) -> list[dict]`**
- Calls `self._info.funding_history(symbol, start_time)` with `start_time = now - days * 86400 * 1000`
- Returns list of `{"time": timestamp_ms, "rate": float}` dicts

## Check Script Changes

**File:** `shared_scripts/check_hyperliquid.py`

When `strategy_name == "delta_neutral_funding"`:

1. Fetch current funding rate via `adapter.get_funding_rate(symbol)`
2. Fetch 7-day history via `adapter.get_funding_history(symbol, days=7)`
3. Compute 7-day average from history
4. Call `apply_strategy("delta_neutral_funding", df, {"current_funding_rate": rate, "avg_funding_rate_7d": avg})`

No changes to JSON output structure. Standard `signal` + `indicators` map.

## Go Side Changes

**`scheduler/init.go`:**
- Add `"delta_neutral_funding": "dnf"` to `knownShortNames`
- Add to `defaultSpotStrategies` fallback list

**No changes needed:**
- `executor.go` — reuses existing `RunHyperliquidCheck`
- `main.go` — dispatches like any perps strategy
- `config.go` — no new config fields

## Files Changed

1. `shared_strategies/spot/strategies.py` — add `delta_neutral_funding` strategy function
2. `shared_strategies/futures/strategies.py` — register same strategy
3. `platforms/hyperliquid/adapter.py` — add `get_funding_rate()`, `get_funding_history()`
4. `shared_scripts/check_hyperliquid.py` — funding rate fetch + param injection for this strategy
5. `scheduler/init.go` — `knownShortNames` + `defaultSpotStrategies` entries

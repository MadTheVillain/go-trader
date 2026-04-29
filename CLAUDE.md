# go-trader Project Context

## Environment
- Go 1.26.2 — `brew install go@1.26`. Not in PATH; use `/opt/homebrew/bin/go` directly.
- Python venv at `.venv/bin/python3` (used by executor.go at runtime); deps via `uv` (`pyproject.toml` / `uv.lock`).
- In git worktrees, `.venv` is NOT copied — use the main repo's venv: `<main-repo>/.venv/bin/python3`.

## Quick Flow
- **New server:** tell your agent `install https://github.com/richkuo/go-trader and init`.

## Setup
- `uv sync` — install Python deps into `.venv`
- Copy `scheduler/config.example.json` → `scheduler/config.json` and fill in API keys

## Repo Structure
- `scheduler/` — Go scheduler (single `package main`); all .go files compile together. Key files:
  - `executor.go` — Python subprocess runner; `pythonSemaphore` caps concurrency at 4, `scriptTimeout=30s`, SIGKILLs process group on timeout.
  - `server.go` — `/status`, `/health`, `/history`; `DefaultStatusPort=8099`, auto-fallback up to 5 ports; precedence: `--status-port` > `cfg.StatusPort` > default.
  - `discord.go` — `discordgo.Session` wrapper; `SendMessage`/`SendDM`/`AskDM`; `FormatCategorySummary`. Summary cols: `Init | Value | PnL | PnL% | DD | Wallet% | Tf | Int | #T | W/L` (DD rendered `%.0f%%`); `Book Sharpe` footer.
  - `init.go` — `go-trader init` wizard + `--json` mode; `generateConfig(InitOptions)` is pure/testable. Holds `bidirectionalPerpsStrategies`, `knownShortNames`, `defaultSpotStrategies` / `defaultPerpsStrategies` / `defaultFuturesStrategies`.
  - `config.go` / `config_migration.go` — `Config`, `StrategyConfig`; `LoadConfig` infers `Platform` from ID prefix; `CurrentConfigVersion=8`; `RiskFreeRate *float64` (nil → `DefaultAnnualRiskFreeRate`); `StopLossPct` HL-perps-only `[0,50]`.
  - `state.go` / `state_presence.go` / `db.go` — SQLite-only state (`modernc.org/sqlite`); `OpenStateDB`, `SaveStateWithDB`, `LoadStateWithDB`. Tables: `app_state`, `strategies`, `positions`, `closed_positions`, `option_positions`, `closed_option_positions`, `trades`, `portfolio_risk`, `kill_switch_events`, `correlation_snapshot`. `InsertTrade` writes immediately. `CheckStatePresence` warns on missing DB (`GO_TRADER_ALLOW_MISSING_STATE=1` for first-run).
  - `risk.go` / `strategy_interval.go` — `CheckRisk` takes `*PlatformRiskAssist`; `perpsMarginDrawdownInputs` for leverage-aware perps DD. `effectiveStrategyIntervalSeconds` accelerates checks in DD warn band; `WarningSent` repeats warnings every cycle while in band.
  - `kill_switch_close.go` + per-platform closers (`hyperliquid_balance.go`, `okx_close.go`, `robinhood_close.go`, `topstep_close.go`, `robinhood_pending_close.go`, `operator_required_close.go`) — portfolio kill switch + per-strategy CB drains.
  - `*_marks.go`, `deribit.go` — native Go price fetchers (HL `allMids`, OKX swap tickers, Deribit ticker); base URL exposed as `var xxxMainnetURL` for httptest stubs.
  - `sharpe.go`, `failure_alerts.go`, `correlation.go`, `leaderboard.go`, `notifier.go`, `telegram.go`, `updater.go`, `pricer.go` (+ `ibkr_pricer.go`), `shared_wallet.go`, `version.go`, `prompt.go`, `balance.go`, `portfolio.go`, `options.go`, `logger.go`, `tradingview_export.go`, `config_reload.go`.
- `shared_scripts/` — Python entry-points: `check_strategy.py` (spot), `check_options.py` (`--platform=deribit|ibkr|robinhood|okx`), `check_price.py`, `check_hyperliquid.py`, `check_topstep.py`, `check_robinhood.py`, `check_okx.py` (`--inst-type=spot|swap`), `check_balance.py`, `fetch_futures_marks.py`, plus per-platform `close_*.py` / `fetch_*_balance.py` / `fetch_*_positions.py`.
- `platforms/<name>/adapter.py` — one `*ExchangeAdapter` class per file: `deribit`, `ibkr`, `binanceus`, `hyperliquid`, `topstep`, `robinhood`, `okx`, `luno`.
- `shared_tools/` — `pricing.py`, `exchange_base.py`, `data_fetcher.py`, `storage.py`, `htf_filter.py`.
- `shared_strategies/` — `registry.py` is the single source of truth (`@register_strategy`, `build_registry(platform)`, `PLATFORM_ORDER`). Cross-platform modules: `adx_trend.py`, `amd_ifvg.py`, `chart_patterns.py`, `donchian_breakout.py`, `liquidity_sweeps.py`, `range_scalper.py`, `session_breakout.py`, `sweep_squeeze_combo.py`. `spot/strategies.py` / `futures/strategies.py` / `options/strategies.py` are thin shims — **do not edit to add strategies**.
- `backtest/` — `backtester.py`, `optimizer.py`, `reporter.py`, `registry_loader.py`, `run_backtest.py`, `backtest_options.py`, `backtest_theta.py`, `tests/`.
- `SKILL.md`, `AGENTS.md` — agent operations guides; `scripts/install-service.sh` — systemd installer; `.github/workflows/` — CI, release, Codex/Claude bots.

## Key Patterns
- Run git from repo root (not `scheduler/`). Prefer `go -C scheduler build .` over `cd scheduler && ...` to avoid cwd drift.
- **New platform:** (1) `platforms/<name>/adapter.py` + `__init__.py`, (2) `shared_scripts/check_<name>.py`, (3) `executor.go`, (4) `config.go` (prefix + validation), (5) `fees.go`, (6) `main.go` (dispatch), (7) `init.go` (wizard + generateConfig), (8) `pyproject.toml`.
- **New options-on-existing-platform:** extend adapter with `get_vol_metrics`, `get_real_expiry`, `get_real_strike`, `get_premium_and_greeks`; add to `check_options.py` + `CalculateOptionFee` + init `OptionPlatforms`.
- Adapters loaded via `importlib`; class detected by `endswith("ExchangeAdapter")` (one per file). Check scripts must use only public adapter methods.
- Subprocess contract: scripts always emit JSON to stdout (even on error); exit 1 on error; Go parses regardless of exit code.
- **State locking:** `mu sync.RWMutex` guards `state`. Per-strategy loop has 6 phases: RLock(read inputs) → Lock(CheckRisk) → no lock(subprocess) → Lock(execute signal) → RLock/no lock/Lock(marks) → RLock(status). Audit: `grep -n "mu\.\(R\)\?Lock\(\)\|mu\.\(R\)\?Unlock\(\)" scheduler/main.go`.
- Platform dispatch: use `s.Platform == "ibkr"`, never ID prefix. ID → platform map: `hl-` HL, `ibkr-` IBKR, `deribit-` Deribit, `ts-` TopStep, `rh-` Robinhood, `okx-` OKX, `luno-` Luno, else BinanceUS.
- Strategy types: `spot`, `options`, `perps`, `futures`. Perps paper reuses `ExecuteSpotSignal`; live calls `RunHyperliquidExecute` first. Futures use `ExecuteFuturesSignal`.
- **Live exec guard:** every dispatch in main.go must use `liveExecFailed`; when `runXxxExecuteOrder` returns `ok2=false`, set true and skip state update.
- **Skip-reason guards:** live order helpers (`runHyperliquidExecuteOrder`, `runOKXExecuteOrder`, `runRobinhoodExecuteOrder`, `runTopStepExecuteOrder`) must check the same conditions as `ExecuteXxxSignal` BEFORE spawning the executor — otherwise on-chain fills land with no Trade record. Use `PerpsOrderSkipReason` / `SpotOrderSkipReason` / `FuturesOrderSkipReason`. Capture `posSide` alongside `posQty` in Phase 1 RLock — `Position.Quantity` is always positive.
- **Bidirectional perps:** `StrategyConfig.AllowShorts` per-strategy opt-in. Strategies emitting `signal=-1` as short entries (`triple_ema_bidir`) must be in `bidirectionalPerpsStrategies` in init.go. `ExecutePerpsSignal(..., allowShorts, ...)` and `PerpsOrderSkipReason(signal, posSide, allowShorts)` must thread `sc.AllowShorts`. Live flip sizing: `perpsLiveOrderSize`; catastrophic flip degrades to close-only. Threads `pos.AvgCost` from Phase 1 snapshot.
- `dueStrategies` is value-copied from `cfg.Strategies` — mutations don't persist. Update `cfg.Strategies` first.
- Notification features: add methods to `MultiNotifier`, don't access `backends` directly.
- Hyperliquid sys.path conflict: SDK package `hyperliquid` clashes with `platforms/hyperliquid/` — add `platforms/hyperliquid/` directly to sys.path, then `from adapter import HyperliquidExchangeAdapter`.
- HL funding rates: `info.meta_and_asset_ctxs()` (NOT `info.meta()`); response uses parallel arrays.
- Fees: `CalculatePlatformSpotFee(platform, value)` (HL 0.035%, RH 0%, BinanceUS 0.1%); `CalculatePlatformFuturesFee(sc, contracts)`.
- Position ownership: `Position.OwnerStrategyID`; `syncHyperliquidAccountPositions` reconciles with owner only.
- **State is SQLite-only** (`scheduler/state.db`, `cfg.DBFile`). Legacy JSON paths removed. Trades persist immediately via `RecordTrade` → `StateDB.InsertTrade`. `ClosedPositions` flushed atomically by `SaveState`. Leaderboard built on-demand (`BuildLeaderboardMessages`).
- **Trades schema (close legs):** `trades.is_close INTEGER` / `realized_pnl REAL` flag close legs at insert time. `LifetimeTradeStatsAll(db)` runs one COUNT/SUM per cycle for `#T` / `W/L` columns; wins use strict `realized_pnl > 0` so breakeven closes don't inflate W. `FormatCategorySummary` falls back to `RiskState` counters when a strategy is absent from the lifetime map (and logs a stderr WARN if `RiskState.TotalTrades > 0`). `backfillTradeCloseFlags` parses legacy `Details` strings (`PnL: $X` / `PnL=$X`); idempotent via `details LIKE '%PnL%'` guard.
- **Live execute fills:** HL/OKX/RH/TopStep `--execute` scripts emit `fillFee` (and per-leg `fillFees`) plus exchange order IDs (`fillOID` / `exchange_order_id`); Go threads these into `Trade.Fees` and `Trade.ExchangeOrderID` BEFORE `RecordTrade`. Python truthy filter (`if oid:`) — never `is not None` — so empty strings and numeric 0 don't land. TopStep precedence: prefer `orderId` even when numeric 0, fall back to `id` only when `orderId` is missing. Tests live in `shared_scripts/test_check_execute_fees.py`.
- **HL kill-switch fill accounting:** `forceCloseKillSwitchPositions(s, sc, prices, hlFills, hlLiveAll, hlVirtualQty, logger)` records real on-chain close fills, not pre-trade estimates. Shared-coin fills are split by **virtual quantity at snapshot time** (not capital weight): `main.go` calls `snapshotHyperliquidVirtualQuantities(state.Strategies, hlLiveAll)` under the RLock BEFORE the close mutates state, and threads the snapshot through `forceCloseKillSwitchPositions` → `applyHyperliquidKillSwitchCloseFill` → `hyperliquidKillSwitchFillShare(sc, coin, fillSz, fillFee, hlLiveAll, virtualQty)`. `hyperliquidKillSwitchFillShare` fails closed (returns `0, 0`) when `sc.ID` isn't among peers or has no virtual qty — never let a misconfigured caller claim the entire portfolio fill.
- **Trade.Side on close legs:** close trades record buy/sell semantics, not the literal string `"close"`. Use `closeTradeSide(positionSide)` (perps/spot — `"short"` → `"buy"`, else `"sell"`) and `optionCloseTradeSide(action)` (options — `"sell"` → `"buy"`, else `"sell"`). Applies in `forceCloseAllPositions`, `recordPerpsStopLossClose`, `executeOptionClose`, and `CheckThetaHarvest`. Default-to-`"sell"` preserves legacy behavior for missing/unknown sides.
- **TradingView export:** if the user says "export data to TradingView", first ask which strategy IDs to export or whether to export all strategies. Use `./go-trader export tradingview --strategy <id> --output <file>` for one or more selected strategies, or `./go-trader export tradingview --all --output <file>`. Exports are sourced from SQLite trades and may require `tradingview_export.symbol_overrides` for platforms/symbols without a safe built-in TradingView mapping.
- **Map iteration:** ALWAYS `sort.Strings(keys)` before formatting any operator-facing or test-asserted output. Go map iteration is randomized.
- `cfg.Discord.Channels` / `Telegram.Channels` / `DMChannels` are `map[string]string`; keys: `spot`, `options`, `<platform>`, `<platform>-paper`. `cfg.Discord.OwnerID` from `DISCORD_OWNER_ID` env var (priority over config).
- `cfg.SummaryFrequency map[string]string`: Go duration / alias (`hourly`, `daily`, `every`/`per_check`/`always`) / empty (legacy). `ShouldPostSummary(..., hasTrades)` — `hasTrades=true` always forces a post.
- `cfg.Correlation` (`Enabled=false`, `MaxConcentrationPct=60`, `MaxSameDirectionPct=75`); `cfg.AutoUpdate`: `off|daily|heartbeat`.
- **Strategy registry:** every implementation lives ONCE in `shared_strategies/registry.py`. Spot/futures/options shims build fresh `STRATEGY_REGISTRY` via `build_registry()`. Cross-platform: create `shared_strategies/<name>.py`, register in registry with `platforms=(...)`. New strategy checklist: (1) register in `registry.py` + `PLATFORM_ORDER`, (2) `knownShortNames` + defaults in `init.go`, (3) `DEFAULT_PARAM_RANGES` in `backtest/optimizer.py`. Use `variants={"futures":{...}}` rather than duplicating. Before refactoring registry: snapshot `--list-json` output and `diff` after — Go `discoverStrategies` depends on byte-identical output.
- `apply_strategy(name, df, params)` — config params merged UNDER runtime params (runtime priority).
- `check_strategy.py` uses manual `sys.argv` parsing — filter `--`-prefixed args before indexing. Other check scripts use argparse.
- `StrategyConfig.HTFFilter` — Go appends `--htf-filter`; not applied to options or `delta_neutral_funding`.
- `delta_neutral_funding` is perps-only — function in `spot/strategies.py` but registered only for futures.
- Perps vs futures at Position level: both `Multiplier > 0`; only perps `Leverage > 0`. Use both fields for leverage-aware metrics.
- Per-strategy DD: spot/options/futures use peak-relative; perps uses `unrealized_loss / deployed_margin` when positions open.
- **Test pattern:** extract pure helpers (`perpsLiveOrderSize`, `*OrderSkipReason`, `parseXxxCloseOutput`) from subprocess-spawning wrappers — Go CI lacks `.venv/bin/python3` so any test calling `RunPythonScript` / `RunHyperliquidExecute` / `RunHyperliquidClose` fails CI.
- Per-strategy circuit-breaker pending close: `RiskState.PendingCircuitCloses map[string]*PendingCircuitClose` keyed by `PlatformPendingClose{Hyperliquid,OKX,Robinhood,TopStep,OKXSpot,RobinhoodOptions}`. Use `setPendingCircuitClose`/`clearPendingCircuitClose`/`getPendingCircuitClose` accessors. `CheckRisk` takes `*PlatformRiskAssist`. RH enqueue runs only from drain's stuck-CB recovery (avoids TOTP cost).
- **Operator-required CBs** (`OKXSpot`, `RobinhoodOptions`): `OperatorRequired=true`; `drainOperatorRequiredPendingCloses` emits one CRITICAL log + notifier per cycle until CB resets.
- **Kill switch:** `planKillSwitchClose(KillSwitchCloseInputs)` returns pure `KillSwitchClosePlan` with `OnChainConfirmedFlat`. Adding new platform = add fields + close/fetcher pair, not signature change. OKX-spot / RH-options surface as warnings, don't block confirmed-flat. Per-platform timeouts (`HLCloseTimeout`, `RHCloseTimeout`, ...). Auto-reset on confirmed-flat: clears virtual state, resumes trading next cycle.
- **`initial_capital` immutable:** `SaveState` refuses stale in-memory overwrites. ONLY write path: `StateDB.SetInitialCapital(strategyID, value)`.
- SQLite column rename migration: use `PRAGMA table_info` to detect three states (neither, legacy-only, new-only); idempotent. `UnmarshalPendingCircuitClosesJSON` accepts both new map and legacy `{"coins":[...]}` shapes.
- Per-trade stop-loss: `StopLossPct` (HL perps only) places reduce-only trigger; OID stored on `Position.StopLossOID`. `HyperliquidLiveCloser` close path takes `cancelStopLossOIDs []int64`.
- **SIGHUP hot reload:** `applyHotReloadConfig` (config_reload.go) re-applies a subset of config without restart. `validateHotReloadCompatible` blocks shape changes (strategy add/remove, script/args/type/platform/HTFFilter, AllowShorts mid-run, kill-switch identity, DB path); `validateHotReloadStateCompatible` blocks per-strategy `leverage` changes when positions are open. Notifier reload re-routes Discord/Telegram channels in place; guard new backends behind nil-checks.
- Drain-failure alerts: `shouldNotifyDrainFailure(key, throttleMap)` + `formatDrainFailureAlert` (failure_alerts.go); throttles per (strategy, platform, symbol, direction).
- Sharpe: per-strategy + book-level annualized; rendered in leaderboard col + `Book Sharpe` footer. `RiskFreeRate` baseline from config (explicit 0 respected). Strategy-interval speedup: when DD > `warn_threshold_pct`, returns `strategyDrawdownFastIntervalSeconds`.
- Adding a per-strategy config flag: (1) field on `StrategyConfig`, (2) main.go `run*Check` appends CLI flag, (3) parse in each Python check script, (4) `InitOptions` + wizard + `generateConfig`.
- Test helpers: name with platform/feature prefix (`stubHLLiveCloser`, not `stubCloser`) — `package main` is shared across `*_test.go`.

## Pull Requests
- Reference related issue with `Closes #<N>` in body.
- In GitHub comments avoid `#N` for list items (auto-links to issues/PRs); use `1.` instead.
- Fetch latest bot review: `gh api repos/richkuo/go-trader/issues/<N>/comments --jq '[.[] | select(.user.login=="codex[bot]" or .user.login=="claude[bot]")] | last | .body'` (top-level summary lives on issues endpoint, not pulls).
- Before merging long-running PR: `git fetch origin main && git diff origin/main..HEAD -- <paths>` to catch silent reverts; rebase if unexpected deletions.
- Replace default `🤖 Generated with [Claude Code]...` footer with metadata (model + effort), e.g. `LLM: Claude Sonnet 4.6 | high`. No `Co-Authored-By` trailer.

### PR review format (`@claude review`)
When invoked to review a PR, the top-level review comment MUST take exactly one of two shapes — no preamble, no closing remarks:
1. **Approve:** a single line starting with `LGTM — ` followed by a one-sentence rationale (what was checked + why it's safe to merge).
2. **Changes requested:** a numbered list (`1.`, `2.`, …) where each item is one sentence naming the concrete change, with `file:line` when applicable. Order by severity (blockers first). No "nice to haves" mixed in — if it's not blocking, leave it out or put it under a final `Optional:` line.
Inline `pull_request_review_comment` threads are exempt from this format; this rule governs only the top-level review summary.

## Build & Deploy
- Build: `cd scheduler && /opt/homebrew/bin/go build -o ../go-trader .` — always rebuild before smoke-testing.
- Restart: `systemctl restart go-trader`. Service file changes: `systemctl daemon-reload && systemctl restart go-trader`.
- Config-only changes (no rebuild needed): `kill -HUP $(pgrep go-trader)` — `config_reload.go` re-reads `cfg.ConfigPath` without dropping state or sessions.
- Python script changes: take effect next scheduler cycle (no rebuild).

## Backtest
- `.venv/bin/python3 backtest/run_backtest.py --strategy <n> --symbol BTC/USDT --timeframe 1h --mode single`
- `.venv/bin/python3 backtest/backtest_options.py --underlying BTC --since YYYY-MM-DD --capital 10000`
- `.venv/bin/python3 backtest/backtest_theta.py --underlying BTC --since YYYY-MM-DD --capital 10000`

## Testing
- **New functionality must include tests.** Go: `_test.go`. Python: `test_*.py`. Bug fixes: regression test when feasible.
- `python3 -m py_compile <file>` from repo root for syntax check.
- `cd scheduler && /opt/homebrew/bin/go build .` (compile) / `/opt/homebrew/bin/go test ./...` (unit tests; must run from `scheduler/` — repo root has no go.mod).
- `cd scheduler && /opt/homebrew/bin/gofmt -w <file>.go` after editing.
- Multi-line Go edits with tabs: Edit tool may fail; use heredoc form: `python3 << 'PYEOF'` / `content=open(f).read()` / `open(f,'w').write(content.replace(old,new,1))` / `PYEOF`.
- Strategy listing: `cd shared_strategies/spot && ../../.venv/bin/python3 strategies.py --list-json` (worktrees: absolute path to main repo's venv).
- Smoke tests:
  - `./go-trader --once` / `./go-trader --config scheduler/config.json`
  - Interactive init: `printf "answer1\nanswer2\n" | ./go-trader init`
  - JSON init: `./go-trader init --json '{"assets":["BTC"],"enableSpot":true,"spotStrategies":["sma_crossover"],"spotCapital":1000,"spotDrawdown":10}' --output /tmp/test.json`
  - Status port override: `./go-trader --once --status-port 9100` — verify `[server] Status endpoint at http://localhost:<port>/status`.
- Pytest: `uv run pytest shared_strategies/ -v`; `shared_tools/`; `platforms/`; `backtest/` (run when modifying strategies). `shared_scripts/test_*.py` is NOT in default `testpaths` — invoke explicitly.
- Strategy tests must assert actual signal values (`assert (result["signal"] == 1).any()`), not just column existence. Smoke tests iterating registered strategies need a `DatetimeIndex` (`amd_ifvg` reads `index.hour`, `vwap_reversion` buckets by `index.date`).
- Python test imports: use `importlib.util.spec_from_file_location` (avoids two `strategies.py` collisions).
- Go tests: always check `json.Unmarshal` errors — silent discard masks struct tag/type regressions.
- Pure helpers (`perpsLiveOrderSize`, `*OrderSkipReason`, `effectiveStrategyIntervalSeconds`, `parseXxxCloseOutput`, Sharpe math in `sharpe_test.go`) are testable without subprocesses — use this pattern for any subprocess-spawning live helper.

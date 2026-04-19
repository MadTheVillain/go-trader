package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Tests for planKillSwitchClose — the orchestration seam for #341. The 7
// tests in hyperliquid_balance_test.go cover forceCloseHyperliquidLive in
// isolation; these cover the "latch until flat" wiring that is the actual
// fix. Without them, the load-bearing
// `killSwitchFired && plan.OnChainConfirmedFlat` guard around
// forceCloseAllPositions could regress silently — exactly the shape of the
// original #341 bug (virtual state mutated without confirming on-chain
// closure).

// stubHLLiveCloser returns a HyperliquidLiveCloser that records every invocation
// and maps coin → canned error. Missing keys yield a synthetic success.
func stubHLLiveCloser(errs map[string]error) (HyperliquidLiveCloser, *[]string) {
	var calls []string
	closer := func(symbol string) (*HyperliquidCloseResult, error) {
		calls = append(calls, symbol)
		if err, ok := errs[symbol]; ok && err != nil {
			return nil, err
		}
		return &HyperliquidCloseResult{
			Close:    &HyperliquidClose{Symbol: symbol, Fill: &HyperliquidCloseFill{TotalSz: 1.0, AvgPx: 100}},
			Platform: "hyperliquid",
		}, nil
	}
	return closer, &calls
}

// stubHLStateFetcher returns an HLStateFetcher that replays a fixed response list
// and records invocation count. errOnce > 0 means the Nth call errors.
func stubHLStateFetcher(positions []HLPosition, err error) (HLStateFetcher, *int) {
	var calls int
	fetcher := func(addr string) ([]HLPosition, error) {
		calls++
		if err != nil {
			return nil, err
		}
		return positions, nil
	}
	return fetcher, &calls
}

// Happy path: HL configured, on-chain state already fetched, one live strategy
// with an open position. Plan reports ConfirmedFlat, closer called once,
// Discord message is the success shape. This is the test that locks in the
// main.go gate: if plan.OnChainConfirmedFlat regresses to false here, the
// kill switch stops clearing virtual state even when the exchange confirmed
// the close.
func TestPlanKillSwitchClose_HappyPath(t *testing.T) {
	hlLive := []StrategyConfig{
		{ID: "hl-ema-eth", Platform: "hyperliquid", Type: "perps",
			Args: []string{"ema_crossover", "ETH", "1h", "--mode=live"}},
	}
	positions := []HLPosition{{Coin: "ETH", Size: 0.5, EntryPrice: 3000}}
	closer, calls := stubHLLiveCloser(nil)
	fetcher, fetchCalls := stubHLStateFetcher(nil, nil)

	plan := planKillSwitchClose("0xaddr", true, positions, hlLive,
		"portfolio drawdown 25.0% exceeds limit 20.0%",
		time.Second, closer, fetcher)

	if !plan.OnChainConfirmedFlat {
		t.Fatalf("expected ConfirmedFlat, got plan=%+v", plan)
	}
	if len(plan.CloseReport.ClosedCoins) != 1 || plan.CloseReport.ClosedCoins[0] != "ETH" {
		t.Errorf("ClosedCoins = %v, want [ETH]", plan.CloseReport.ClosedCoins)
	}
	if *fetchCalls != 0 {
		t.Errorf("fetcher must not be called when state already fetched, got %d", *fetchCalls)
	}
	if len(*calls) != 1 || (*calls)[0] != "ETH" {
		t.Errorf("closer calls = %v, want [ETH]", *calls)
	}
	if !strings.Contains(plan.DiscordMessage, "PORTFOLIO KILL SWITCH") ||
		strings.Contains(plan.DiscordMessage, "LATCHED") {
		t.Errorf("expected success-shaped message, got: %s", plan.DiscordMessage)
	}
	if !strings.Contains(plan.DiscordMessage, "Virtual state cleared") {
		t.Errorf("expected 'Virtual state cleared' in message, got: %s", plan.DiscordMessage)
	}
}

// Close failure: closer errors for one coin. Plan must NOT be ConfirmedFlat
// — caller must keep virtual state intact and retry next cycle. Discord
// message must be the "LATCHED, RETRYING" shape with the specific error
// surfaced. This is the test that prevents regression of the retry loop
// (the whole point of latch-until-flat).
func TestPlanKillSwitchClose_CloseError(t *testing.T) {
	hlLive := []StrategyConfig{
		{ID: "hl-ema-eth", Platform: "hyperliquid", Type: "perps",
			Args: []string{"ema_crossover", "ETH", "1h", "--mode=live"}},
	}
	positions := []HLPosition{{Coin: "ETH", Size: 0.5}}
	closer, _ := stubHLLiveCloser(map[string]error{"ETH": fmt.Errorf("hl rate limited")})
	fetcher, _ := stubHLStateFetcher(nil, nil)

	plan := planKillSwitchClose("0xaddr", true, positions, hlLive,
		"portfolio drawdown 25.0% exceeds limit 20.0%",
		time.Second, closer, fetcher)

	if plan.OnChainConfirmedFlat {
		t.Fatal("expected NOT ConfirmedFlat on close error — kill switch would clear virtual state while on-chain is still live")
	}
	if got, ok := plan.CloseReport.Errors["ETH"]; !ok || got == nil {
		t.Errorf("expected ETH error in report, got %v", plan.CloseReport.Errors)
	}
	if !strings.Contains(plan.DiscordMessage, "LATCHED, RETRYING") {
		t.Errorf("expected LATCHED message on close error, got: %s", plan.DiscordMessage)
	}
	if !strings.Contains(plan.DiscordMessage, "Virtual state preserved") {
		t.Errorf("expected 'Virtual state preserved' in latched message, got: %s", plan.DiscordMessage)
	}
	if !strings.Contains(plan.DiscordMessage, "hl rate limited") {
		t.Errorf("error detail missing from message, got: %s", plan.DiscordMessage)
	}
}

// Opportunistic fetch: HL configured but main.go didn't fetch state this
// cycle (no due strategies, no shared wallet). planKillSwitchClose must
// re-fetch — otherwise the kill switch reports "no live HL exposure" without
// checking, which is the false-reassurance case the PR review flagged.
func TestPlanKillSwitchClose_OpportunisticFetch(t *testing.T) {
	hlLive := []StrategyConfig{
		{ID: "hl-ema-eth", Platform: "hyperliquid", Type: "perps",
			Args: []string{"ema_crossover", "ETH", "1h", "--mode=live"}},
	}
	positions := []HLPosition{{Coin: "ETH", Size: 0.5}}
	closer, calls := stubHLLiveCloser(nil)
	fetcher, fetchCalls := stubHLStateFetcher(positions, nil)

	plan := planKillSwitchClose("0xaddr", false, nil, hlLive,
		"drawdown reason", time.Second, closer, fetcher)

	if *fetchCalls != 1 {
		t.Fatalf("fetcher should be called once, got %d", *fetchCalls)
	}
	if !plan.OnChainConfirmedFlat {
		t.Errorf("expected ConfirmedFlat after successful fetch + close, got plan=%+v", plan)
	}
	if len(*calls) != 1 || (*calls)[0] != "ETH" {
		t.Errorf("closer calls = %v, want [ETH] (fetched positions should feed the closer)", *calls)
	}
}

// Opportunistic fetch failure: HL configured, fetch errors. Plan must NOT be
// ConfirmedFlat — we cannot verify on-chain state, so caller must not clear
// virtual state. This is the test that guards against silent desync when HL
// API is flaky.
func TestPlanKillSwitchClose_FetchFailure(t *testing.T) {
	hlLive := []StrategyConfig{
		{ID: "hl-ema-eth", Platform: "hyperliquid", Type: "perps",
			Args: []string{"ema_crossover", "ETH", "1h", "--mode=live"}},
	}
	closer, calls := stubHLLiveCloser(nil)
	fetcher, fetchCalls := stubHLStateFetcher(nil, fmt.Errorf("hl 503"))

	plan := planKillSwitchClose("0xaddr", false, nil, hlLive,
		"drawdown reason", time.Second, closer, fetcher)

	if *fetchCalls != 1 {
		t.Fatalf("fetcher should be called once on fetch failure, got %d", *fetchCalls)
	}
	if plan.OnChainConfirmedFlat {
		t.Fatal("expected NOT ConfirmedFlat on fetch failure — cannot verify on-chain state")
	}
	if len(*calls) != 0 {
		t.Errorf("closer must not be invoked when fetch failed, got calls=%v", *calls)
	}
	if !strings.Contains(plan.DiscordMessage, "LATCHED, RETRYING") {
		t.Errorf("expected LATCHED message on fetch failure, got: %s", plan.DiscordMessage)
	}
	if !strings.Contains(plan.DiscordMessage, "Could not fetch") {
		t.Errorf("expected fetch-failure detail in message, got: %s", plan.DiscordMessage)
	}
}

// False-reassurance case: HL configured but no live HL strategies are
// configured, yet the wallet still has on-chain positions (previous deploy,
// manual trade). planKillSwitchClose must fetch state, detect the positions,
// block virtual state mutation, and surface them in the Discord message so
// the operator manually intervenes. Before the fix, the plan would report
// "no live HL exposure" — the original #341 review's HIGH finding.
func TestPlanKillSwitchClose_UnconfiguredPositionBlocksReset(t *testing.T) {
	positions := []HLPosition{{Coin: "ETH", Size: 0.517}}
	closer, calls := stubHLLiveCloser(nil)
	fetcher, _ := stubHLStateFetcher(positions, nil)

	plan := planKillSwitchClose("0xaddr", false, nil,
		[]StrategyConfig{}, // NO live HL strategies configured
		"drawdown reason", time.Second, closer, fetcher)

	if plan.OnChainConfirmedFlat {
		t.Fatal("expected NOT ConfirmedFlat — on-chain position exists for unconfigured coin")
	}
	if len(plan.Unconfigured) != 1 || plan.Unconfigured[0].Coin != "ETH" {
		t.Errorf("expected Unconfigured=[ETH], got %v", plan.Unconfigured)
	}
	if len(*calls) != 0 {
		t.Errorf("closer must not be invoked for unconfigured coin, got %v", *calls)
	}
	if !strings.Contains(plan.DiscordMessage, "manual intervention required") {
		t.Errorf("message must call out manual intervention, got: %s", plan.DiscordMessage)
	}
	if !strings.Contains(plan.DiscordMessage, "ETH szi=0.517000") {
		t.Errorf("message must include coin+szi detail, got: %s", plan.DiscordMessage)
	}
}

// No HL at all: hlAddr="" and no live HL strategies. Kill switch should
// proceed normally (ConfirmedFlat=true) so spot/options/futures-only users
// don't regress. Message must not imply HL was checked.
func TestPlanKillSwitchClose_NoHLConfigured(t *testing.T) {
	closer, calls := stubHLLiveCloser(nil)
	fetcher, fetchCalls := stubHLStateFetcher(nil, nil)

	plan := planKillSwitchClose("", false, nil, nil,
		"drawdown reason", time.Second, closer, fetcher)

	if !plan.OnChainConfirmedFlat {
		t.Fatal("expected ConfirmedFlat when HL is not configured at all")
	}
	if *fetchCalls != 0 {
		t.Errorf("fetcher must not be called when hlAddr is empty, got %d", *fetchCalls)
	}
	if len(*calls) != 0 {
		t.Errorf("closer must not be called, got %v", *calls)
	}
	if !strings.Contains(plan.DiscordMessage, "HL not configured") {
		t.Errorf("expected 'HL not configured' in message, got: %s", plan.DiscordMessage)
	}
}

// Stable error ordering (bot review #3): when multiple coins fail with the
// same errors, the Discord message must be byte-identical across calls.
// Without sort, Go map iteration randomization produces different messages
// for identical failures — confusing for operator triage.
func TestPlanKillSwitchClose_DeterministicErrorOrder(t *testing.T) {
	hlLive := []StrategyConfig{
		{ID: "hl-btc", Platform: "hyperliquid", Type: "perps",
			Args: []string{"sma", "BTC", "1h", "--mode=live"}},
		{ID: "hl-eth", Platform: "hyperliquid", Type: "perps",
			Args: []string{"sma", "ETH", "1h", "--mode=live"}},
		{ID: "hl-sol", Platform: "hyperliquid", Type: "perps",
			Args: []string{"sma", "SOL", "1h", "--mode=live"}},
		{ID: "hl-doge", Platform: "hyperliquid", Type: "perps",
			Args: []string{"sma", "DOGE", "1h", "--mode=live"}},
	}
	positions := []HLPosition{
		{Coin: "BTC", Size: 0.01}, {Coin: "ETH", Size: 0.1},
		{Coin: "SOL", Size: 1.0}, {Coin: "DOGE", Size: 100},
	}
	errs := map[string]error{
		"BTC": fmt.Errorf("err"), "ETH": fmt.Errorf("err"),
		"SOL": fmt.Errorf("err"), "DOGE": fmt.Errorf("err"),
	}
	var prev string
	for i := 0; i < 10; i++ {
		closer, _ := stubHLLiveCloser(errs)
		fetcher, _ := stubHLStateFetcher(nil, nil)
		plan := planKillSwitchClose("0xaddr", true, positions, hlLive,
			"reason", time.Second, closer, fetcher)
		if prev != "" && plan.DiscordMessage != prev {
			t.Fatalf("message should be deterministic across calls\niter %d: %s\nprev: %s", i, plan.DiscordMessage, prev)
		}
		prev = plan.DiscordMessage
	}
	// Also verify coins are alphabetically sorted in the output.
	btcPos := strings.Index(prev, "BTC:")
	dogePos := strings.Index(prev, "DOGE:")
	ethPos := strings.Index(prev, "ETH:")
	solPos := strings.Index(prev, "SOL:")
	if !(btcPos < dogePos && dogePos < ethPos && ethPos < solPos) {
		t.Errorf("expected alphabetical ordering BTC < DOGE < ETH < SOL, got positions btc=%d doge=%d eth=%d sol=%d in: %s",
			btcPos, dogePos, ethPos, solPos, prev)
	}
}

// Not-killSwitchFired guard: the caller only invokes planKillSwitchClose
// when killSwitchFired==true, but we still verify that a pure-data call
// with all-zero inputs returns a sensible default — specifically
// OnChainConfirmedFlat=true so a mistaken invocation from a future
// refactor wouldn't spuriously latch.
func TestPlanKillSwitchClose_ZeroInputsAreSafe(t *testing.T) {
	closer, _ := stubHLLiveCloser(nil)
	fetcher, _ := stubHLStateFetcher(nil, nil)
	plan := planKillSwitchClose("", false, nil, nil, "", time.Second, closer, fetcher)
	if !plan.OnChainConfirmedFlat {
		t.Errorf("zero inputs should yield ConfirmedFlat=true, got %+v", plan)
	}
}

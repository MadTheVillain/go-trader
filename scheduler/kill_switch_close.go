package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// KillSwitchClosePlan is the output of planKillSwitchClose — everything the
// main loop needs to apply virtual-state mutation and send notifications.
// The plan is pure data (no goroutines, no I/O callbacks), so the main loop
// can gate virtual state mutation on OnChainConfirmedFlat under its own
// mutex without re-running any logic.
type KillSwitchClosePlan struct {
	// OnChainConfirmedFlat is the load-bearing correctness signal. True means
	// the caller MAY clear virtual state. False means at least one live HL
	// exposure could not be confirmed closed — caller MUST leave virtual
	// state intact and let the next cycle retry.
	OnChainConfirmedFlat bool

	// CloseReport is the per-coin outcome of the close attempt. Zero value
	// when no close was attempted (HL not configured, HL configured but no
	// live strategies, fetch failure).
	CloseReport HyperliquidLiveCloseReport

	// Unconfigured lists on-chain positions for coins no configured live HL
	// strategy trades. Non-empty when HYPERLIQUID_ACCOUNT_ADDRESS is set but
	// len(hlLiveAll) == 0 and the wallet holds positions. The kill switch
	// refuses to unilaterally liquidate unconfigured positions (could be a
	// manual hedge or another bot's inventory); it surfaces them instead.
	Unconfigured []HLPosition

	// DiscordMessage is the formatted notification string; empty when no
	// Discord message should be sent. Caller checks notifier.HasBackends()
	// before delivering.
	DiscordMessage string

	// LogLines are the stderr lines to print ([CRITICAL]/[INFO]). Built here
	// rather than printed directly so tests can assert messaging.
	LogLines []string
}

// HLStateFetcher re-fetches Hyperliquid on-chain positions for the kill-switch
// opportunistic-fetch path. Exposed as a function type so tests can stub the
// HTTP call. The default wraps fetchHyperliquidState.
type HLStateFetcher func(accountAddress string) ([]HLPosition, error)

// defaultHLStateFetcher wraps fetchHyperliquidState for production use. The
// kill-switch path discards the balance field — only positions are needed.
func defaultHLStateFetcher(addr string) ([]HLPosition, error) {
	_, pos, err := fetchHyperliquidState(addr)
	return pos, err
}

// planKillSwitchClose runs the kill-switch close logic without touching any
// mutable state — no locks, no virtual state mutation, no Discord delivery.
// The caller applies mutations based on the returned plan.
//
// Extracted from main.go so the latch-until-flat flow (the actual #341 fix)
// can be unit-tested with fake closer + fake fetcher. Without this seam, the
// load-bearing `if killSwitchFired && OnChainConfirmedFlat` gate around
// forceCloseAllPositions would regress silently — exactly the kind of bug
// #341 was.
//
// Inputs capture the cycle's state at the point of the kill-switch firing:
//   - hlAddr: HYPERLIQUID_ACCOUNT_ADDRESS value ("" means HL not configured)
//   - hlStateFetched / hlPositions: whether main.go already fetched state
//     earlier in the cycle (for shared-wallet balance or due reconcile)
//   - hlLiveAll: every configured live HL strategy
//   - portfolioReason: CheckPortfolioRisk's reason string
//   - closeTimeout: overall budget for the whole close loop (caller passes
//     ~90s in production; tests pass context.Background deadline via 0)
//   - closer: submits the actual HL close (defaultHyperliquidLiveCloser or
//     a fake)
//   - fetcher: performs the opportunistic re-fetch when main.go didn't fetch
//     but HL is configured (defaultHLStateFetcher or a fake)
func planKillSwitchClose(
	hlAddr string,
	hlStateFetched bool,
	hlPositions []HLPosition,
	hlLiveAll []StrategyConfig,
	portfolioReason string,
	closeTimeout time.Duration,
	closer HyperliquidLiveCloser,
	fetcher HLStateFetcher,
) KillSwitchClosePlan {
	plan := KillSwitchClosePlan{OnChainConfirmedFlat: true}

	// Opportunistic fetch: operator could have removed all HL strategies
	// from config while the wallet still holds positions from a previous
	// deploy or manual trade. Kill switch must not report "no exposure"
	// without actually checking (#341 review, false-reassurance case).
	if !hlStateFetched && hlAddr != "" {
		pos, err := fetcher(hlAddr)
		if err != nil {
			plan.LogLines = append(plan.LogLines,
				fmt.Sprintf("[CRITICAL] hl-close: kill switch unable to fetch HL state: %v — cannot confirm on-chain flat", err))
			plan.OnChainConfirmedFlat = false
		} else {
			hlPositions = pos
			hlStateFetched = true
		}
	}

	switch {
	case hlStateFetched && len(hlLiveAll) > 0:
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		plan.CloseReport = forceCloseHyperliquidLive(ctx, hlPositions, hlLiveAll, closer)
		if !plan.CloseReport.ConfirmedFlat() {
			plan.OnChainConfirmedFlat = false
		}
		if len(plan.CloseReport.ClosedCoins) > 0 {
			plan.LogLines = append(plan.LogLines,
				fmt.Sprintf("[CRITICAL] hl-close: confirmed close for %v", plan.CloseReport.ClosedCoins))
		}
		if len(plan.CloseReport.AlreadyFlat) > 0 {
			plan.LogLines = append(plan.LogLines,
				fmt.Sprintf("[INFO] hl-close: already flat on-chain: %v", plan.CloseReport.AlreadyFlat))
		}
		for _, coin := range plan.CloseReport.SortedErrorCoins() {
			plan.LogLines = append(plan.LogLines,
				fmt.Sprintf("[CRITICAL] hl-close: %s failed: %v (kill switch will retry next cycle)", coin, plan.CloseReport.Errors[coin]))
		}

	case hlStateFetched && len(hlLiveAll) == 0:
		for _, p := range hlPositions {
			if p.Size != 0 {
				plan.Unconfigured = append(plan.Unconfigured, p)
			}
		}
		if len(plan.Unconfigured) > 0 {
			plan.OnChainConfirmedFlat = false
			for _, p := range plan.Unconfigured {
				plan.LogLines = append(plan.LogLines,
					fmt.Sprintf("[CRITICAL] hl-close: on-chain position for unconfigured coin %s (szi=%.6f) — manual intervention required, kill switch will retry next cycle", p.Coin, p.Size))
			}
		}
	}

	plan.DiscordMessage = formatKillSwitchMessage(hlAddr, plan, portfolioReason)
	return plan
}

// formatKillSwitchMessage builds the Discord notification string from a plan.
// Split out so tests can call it directly and so main.go delivery stays a
// one-liner. Returns two distinct shapes: "PORTFOLIO KILL SWITCH" on
// confirmed-flat, "PORTFOLIO KILL SWITCH (LATCHED, RETRYING)" otherwise.
func formatKillSwitchMessage(hlAddr string, plan KillSwitchClosePlan, portfolioReason string) string {
	if plan.OnChainConfirmedFlat {
		summary := "no live HL exposure"
		if len(plan.CloseReport.ClosedCoins) > 0 {
			summary = fmt.Sprintf("HL closes: %v", plan.CloseReport.ClosedCoins)
		} else if hlAddr == "" {
			summary = "HL not configured"
		}
		return fmt.Sprintf("**PORTFOLIO KILL SWITCH**\n%s\n%s. Virtual state cleared. Manual reset required.", portfolioReason, summary)
	}

	var errSummary string
	switch {
	case len(plan.CloseReport.Errors) > 0:
		parts := make([]string, 0, len(plan.CloseReport.Errors))
		for _, coin := range plan.CloseReport.SortedErrorCoins() {
			parts = append(parts, fmt.Sprintf("%s: %v", coin, plan.CloseReport.Errors[coin]))
		}
		errSummary = "Live close errors — " + strings.Join(parts, "; ")
	case len(plan.Unconfigured) > 0:
		names := make([]string, 0, len(plan.Unconfigured))
		for _, p := range plan.Unconfigured {
			names = append(names, fmt.Sprintf("%s szi=%.6f", p.Coin, p.Size))
		}
		sort.Strings(names)
		errSummary = "On-chain HL positions for unconfigured coins (manual intervention required) — " + strings.Join(names, "; ")
	default:
		errSummary = "Could not fetch HL on-chain state to confirm flat"
	}
	return fmt.Sprintf("**PORTFOLIO KILL SWITCH (LATCHED, RETRYING)**\n%s\n%s. Virtual state preserved. Next cycle will retry.", portfolioReason, errSummary)
}

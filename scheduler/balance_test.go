package main

import (
	"testing"
)

func TestResolveCapitalPct_NoCapitalPct(t *testing.T) {
	strategies := []StrategyConfig{
		{ID: "test-1", Capital: 1000, Platform: "binanceus"},
		{ID: "test-2", Capital: 2000, Platform: "hyperliquid"},
	}
	// Should be a no-op when no strategies have CapitalPct.
	resolveCapitalPct(strategies)
	if strategies[0].Capital != 1000 {
		t.Errorf("expected capital=1000, got %g", strategies[0].Capital)
	}
	if strategies[1].Capital != 2000 {
		t.Errorf("expected capital=2000, got %g", strategies[1].Capital)
	}
}

func TestResolveCapitalPct_FallbackCapital(t *testing.T) {
	// When balance fetch fails (unsupported platform without Python), should keep fallback capital.
	strategies := []StrategyConfig{
		{ID: "test-pct", Capital: 500, CapitalPct: 0.5, Platform: "binanceus"},
	}
	// binanceus doesn't have a Go-native balance fetch and check_balance.py won't work in tests,
	// so it should fall back to the existing capital value.
	resolveCapitalPct(strategies)
	if strategies[0].Capital != 500 {
		t.Errorf("expected fallback capital=500, got %g", strategies[0].Capital)
	}
}

func TestResolveCapitalPct_NoFallbackCapital(t *testing.T) {
	// When balance fetch fails and no fallback capital, should remain 0.
	strategies := []StrategyConfig{
		{ID: "test-no-fallback", Capital: 0, CapitalPct: 0.5, Platform: "binanceus"},
	}
	resolveCapitalPct(strategies)
	if strategies[0].Capital != 0 {
		t.Errorf("expected capital=0 (no fallback), got %g", strategies[0].Capital)
	}
}

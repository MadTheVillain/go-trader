package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPrecomputeLeaderboard(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")

	cfg := &Config{
		StateFile: stateFile,
		Strategies: []StrategyConfig{
			{ID: "sma-btc", Type: "spot", Capital: 1000, Platform: "binanceus", Args: []string{"sma_crossover", "BTC/USDT", "1h"}},
			{ID: "rsi-eth", Type: "spot", Capital: 500, Platform: "binanceus", Args: []string{"rsi_divergence", "ETH/USDT", "1h"}},
			{ID: "hl-sma-btc", Type: "perps", Capital: 2000, Platform: "hyperliquid", Args: []string{"sma_crossover", "BTC/USDT", "1h"}},
			{ID: "deribit-ccall-btc", Type: "options", Capital: 1000, Platform: "deribit", Args: []string{"covered_call", "BTC/USDT"}},
			{ID: "ts-breakout-es", Type: "futures", Capital: 5000, Platform: "topstep", Args: []string{"breakout", "ES", "15m"}},
		},
	}

	state := NewAppState()
	for _, sc := range cfg.Strategies {
		ss := NewStrategyState(sc)
		// Give each strategy different PnL by adjusting cash.
		switch sc.ID {
		case "sma-btc":
			ss.Cash = 1100 // +10%
		case "rsi-eth":
			ss.Cash = 450 // -10%
		case "hl-sma-btc":
			ss.Cash = 2200 // +10%
		case "deribit-ccall-btc":
			ss.Cash = 1050 // +5%
		case "ts-breakout-es":
			ss.Cash = 4800 // -4%
		}
		state.Strategies[sc.ID] = ss
	}

	prices := map[string]float64{
		"BTC/USDT": 50000,
		"ETH/USDT": 3000,
	}

	err := PrecomputeLeaderboard(cfg, state, prices)
	if err != nil {
		t.Fatalf("PrecomputeLeaderboard failed: %v", err)
	}

	// Verify file was written.
	path := leaderboardPath(cfg)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read leaderboard file: %v", err)
	}

	var lb LeaderboardData
	if err := json.Unmarshal(data, &lb); err != nil {
		t.Fatalf("Failed to parse leaderboard: %v", err)
	}

	// Verify we have category messages.
	if _, ok := lb.Messages["spot"]; !ok {
		t.Error("Missing spot leaderboard message")
	}
	if _, ok := lb.Messages["perps"]; !ok {
		t.Error("Missing perps leaderboard message")
	}
	if _, ok := lb.Messages["options"]; !ok {
		t.Error("Missing options leaderboard message")
	}
	if _, ok := lb.Messages["futures"]; !ok {
		t.Error("Missing futures leaderboard message")
	}
	if _, ok := lb.Messages["top10"]; !ok {
		t.Error("Missing top10 leaderboard message")
	}
	if _, ok := lb.Messages["bottom10"]; !ok {
		t.Error("Missing bottom10 leaderboard message")
	}

	// Verify timestamp is recent.
	if lb.Timestamp.IsZero() {
		t.Error("Leaderboard timestamp is zero")
	}

	// Spot message should contain strategy IDs and PnL data.
	spotMsg := lb.Messages["spot"]
	if spotMsg == "" {
		t.Fatal("Spot message is empty")
	}
	if !containsStr(spotMsg, "sma-btc") {
		t.Error("Spot message should contain sma-btc")
	}
	if !containsStr(spotMsg, "Spot Leaderboard") {
		t.Error("Spot message should contain title")
	}
	if !containsStr(spotMsg, "TOTAL") {
		t.Error("Spot message should contain TOTAL row")
	}
	if !containsStr(spotMsg, "winning") {
		t.Error("Spot message should contain winning/losing/flat counts")
	}
}

func TestLoadLeaderboard(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")
	cfg := &Config{StateFile: stateFile}

	// No file yet — should error.
	_, err := LoadLeaderboard(cfg)
	if err == nil {
		t.Error("Expected error when leaderboard file doesn't exist")
	}

	// Write a valid file.
	lb := LeaderboardData{
		Messages: map[string]string{
			"spot": "test message",
		},
	}
	data, _ := json.Marshal(lb)
	path := leaderboardPath(cfg)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	loaded, err := LoadLeaderboard(cfg)
	if err != nil {
		t.Fatalf("LoadLeaderboard failed: %v", err)
	}
	if loaded.Messages["spot"] != "test message" {
		t.Errorf("Expected 'test message', got %q", loaded.Messages["spot"])
	}
}

func TestFmtSignedDollar(t *testing.T) {
	tests := []struct {
		input float64
		want  string
	}{
		{100, "$+100"},
		{-50, "$-50"},
		{0, "$+0"},
		{1234, "$+1,234"},
		{-9876, "$-9,876"},
	}
	for _, tt := range tests {
		got := fmtSignedDollar(tt.input)
		if got != tt.want {
			t.Errorf("fmtSignedDollar(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFmtSignedPct(t *testing.T) {
	tests := []struct {
		input float64
		want  string
	}{
		{10.5, "+10.5%"},
		{-3.2, "-3.2%"},
		{0, "+0.0%"},
	}
	for _, tt := range tests {
		got := fmtSignedPct(tt.input)
		if got != tt.want {
			t.Errorf("fmtSignedPct(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNewAppState(t *testing.T) {
	state := NewAppState()
	if state.CycleCount != 0 {
		t.Errorf("CycleCount = %d, want 0", state.CycleCount)
	}
	if state.Strategies == nil {
		t.Error("Strategies map should not be nil")
	}
	if len(state.Strategies) != 0 {
		t.Errorf("Strategies should be empty, got %d", len(state.Strategies))
	}
}

func TestNewStrategyState(t *testing.T) {
	cfg := StrategyConfig{
		ID:             "test-spot-btc",
		Type:           "spot",
		Platform:       "binanceus",
		Capital:        1000,
		MaxDrawdownPct: 60,
	}
	s := NewStrategyState(cfg)
	if s.ID != "test-spot-btc" {
		t.Errorf("ID = %q, want %q", s.ID, "test-spot-btc")
	}
	if s.Type != "spot" {
		t.Errorf("Type = %q, want %q", s.Type, "spot")
	}
	if s.Platform != "binanceus" {
		t.Errorf("Platform = %q, want %q", s.Platform, "binanceus")
	}
	if s.Cash != 1000 {
		t.Errorf("Cash = %g, want 1000", s.Cash)
	}
	if s.InitialCapital != 1000 {
		t.Errorf("InitialCapital = %g, want 1000", s.InitialCapital)
	}
	if s.Positions == nil {
		t.Error("Positions should not be nil")
	}
	if s.OptionPositions == nil {
		t.Error("OptionPositions should not be nil")
	}
	if s.TradeHistory == nil {
		t.Error("TradeHistory should not be nil")
	}
	if s.RiskState.PeakValue != 1000 {
		t.Errorf("RiskState.PeakValue = %g, want 1000", s.RiskState.PeakValue)
	}
	if s.RiskState.MaxDrawdownPct != 60 {
		t.Errorf("RiskState.MaxDrawdownPct = %g, want 60", s.RiskState.MaxDrawdownPct)
	}
}

func TestLoadStateMissingFile(t *testing.T) {
	state, err := LoadState(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err != nil {
		t.Fatalf("LoadState should not error for missing file: %v", err)
	}
	if state.CycleCount != 0 {
		t.Errorf("CycleCount = %d, want 0", state.CycleCount)
	}
	if state.Strategies == nil {
		t.Error("Strategies should be initialized")
	}
}

func TestLoadStateNilMapsFixed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	// Write state with nil maps (simulated by omitting fields)
	raw := `{"cycle_count": 1, "strategies": {"s1": {"id": "s1"}}}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}

	state, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}

	s := state.Strategies["s1"]
	if s.Positions == nil {
		t.Error("Positions should be initialized, not nil")
	}
	if s.OptionPositions == nil {
		t.Error("OptionPositions should be initialized, not nil")
	}
	if s.TradeHistory == nil {
		t.Error("TradeHistory should be initialized, not nil")
	}
}

func TestLoadStateInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadState(path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestValidateState(t *testing.T) {
	state := NewAppState()
	state.Strategies["s1"] = &StrategyState{
		ID:             "s1",
		InitialCapital: -100, // invalid
		Cash:           -50,  // negative
		Positions: map[string]*Position{
			"BTC/USDT": {Quantity: 0.01, Side: "long"},
			"ETH/USDT": {Quantity: 0, Side: "long"},   // invalid: zero
			"SOL/USDT": {Quantity: -1, Side: "short"}, // invalid: negative
		},
		OptionPositions: map[string]*OptionPosition{
			"valid":   {Action: "buy", OptionType: "call", Quantity: 1},
			"badact":  {Action: "invalid", OptionType: "call", Quantity: 1},
			"badtype": {Action: "sell", OptionType: "invalid", Quantity: 1},
			"badqty":  {Action: "buy", OptionType: "put", Quantity: 0},
		},
		TradeHistory: []Trade{},
	}

	ValidateState(state)

	s := state.Strategies["s1"]
	if s.InitialCapital != 0 {
		t.Errorf("InitialCapital should be reset to 0, got %g", s.InitialCapital)
	}
	if s.Cash != 0 {
		t.Errorf("Cash should be clamped to 0, got %g", s.Cash)
	}
	if _, ok := s.Positions["BTC/USDT"]; !ok {
		t.Error("valid position BTC/USDT should remain")
	}
	if _, ok := s.Positions["ETH/USDT"]; ok {
		t.Error("zero-quantity position should be removed")
	}
	if _, ok := s.Positions["SOL/USDT"]; ok {
		t.Error("negative-quantity position should be removed")
	}
	if _, ok := s.OptionPositions["valid"]; !ok {
		t.Error("valid option should remain")
	}
	if _, ok := s.OptionPositions["badact"]; ok {
		t.Error("invalid-action option should be removed")
	}
	if _, ok := s.OptionPositions["badtype"]; ok {
		t.Error("invalid-type option should be removed")
	}
	if _, ok := s.OptionPositions["badqty"]; ok {
		t.Error("zero-quantity option should be removed")
	}
}

// writeTestJSON writes a JSON state file for migration tests.
func writeTestJSON(t *testing.T, path string, state *AppState) {
	t.Helper()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write state: %v", err)
	}
}

func TestLoadStateWithDB_SQLitePrimary(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	jsonPath := filepath.Join(dir, "state.json")

	db, err := OpenStateDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Seed SQLite with data.
	original := &AppState{
		CycleCount: 10,
		Strategies: map[string]*StrategyState{
			"test": {ID: "test", Type: "spot", Cash: 500, InitialCapital: 1000,
				Positions: make(map[string]*Position), OptionPositions: make(map[string]*OptionPosition), TradeHistory: []Trade{}},
		},
	}
	if err := db.SaveState(original); err != nil {
		t.Fatal(err)
	}

	// Write different data to JSON (should be ignored).
	jsonState := NewAppState()
	jsonState.CycleCount = 99
	writeTestJSON(t, jsonPath, jsonState)

	cfg := &Config{StateFile: jsonPath}
	loaded, err := LoadStateWithDB(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CycleCount != 10 {
		t.Errorf("CycleCount = %d, want 10 (from SQLite, not JSON's 99)", loaded.CycleCount)
	}
}

func TestLoadStateWithDB_JSONFallbackAndMigration(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	jsonPath := filepath.Join(dir, "state.json")

	// Write data to JSON only (SQLite is empty).
	jsonState := &AppState{
		CycleCount: 7,
		Strategies: map[string]*StrategyState{
			"s1": {ID: "s1", Type: "spot", Cash: 300, InitialCapital: 500,
				Positions: make(map[string]*Position), OptionPositions: make(map[string]*OptionPosition), TradeHistory: []Trade{}},
		},
	}
	writeTestJSON(t, jsonPath, jsonState)

	db, err := OpenStateDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := &Config{StateFile: jsonPath}
	loaded, err := LoadStateWithDB(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CycleCount != 7 {
		t.Errorf("CycleCount = %d, want 7 (from JSON fallback)", loaded.CycleCount)
	}
	if _, ok := loaded.Strategies["s1"]; !ok {
		t.Error("strategy s1 should be loaded from JSON")
	}

	// Verify one-time migration: SQLite should now have the data.
	dbState, err := db.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if dbState == nil {
		t.Fatal("SQLite should have data after migration")
	}
	if dbState.CycleCount != 7 {
		t.Errorf("SQLite CycleCount = %d, want 7 (migrated from JSON)", dbState.CycleCount)
	}
}

func TestLoadStateWithDB_BothEmpty(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	jsonPath := filepath.Join(dir, "state.json") // does not exist

	db, err := OpenStateDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := &Config{StateFile: jsonPath}
	loaded, err := LoadStateWithDB(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CycleCount != 0 {
		t.Errorf("CycleCount = %d, want 0 (fresh start)", loaded.CycleCount)
	}
	if len(loaded.Strategies) != 0 {
		t.Errorf("strategies = %d, want 0", len(loaded.Strategies))
	}
}

func TestSaveStateWithDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")

	db, err := OpenStateDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	state := &AppState{
		CycleCount: 3,
		Strategies: map[string]*StrategyState{
			"test": {ID: "test", Type: "spot", Cash: 800, InitialCapital: 1000,
				Positions: make(map[string]*Position), OptionPositions: make(map[string]*OptionPosition), TradeHistory: []Trade{}},
		},
	}

	cfg := &Config{}
	if err := SaveStateWithDB(state, cfg, db); err != nil {
		t.Fatal(err)
	}

	// Verify SQLite has data.
	dbState, err := db.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if dbState.CycleCount != 3 {
		t.Errorf("SQLite CycleCount = %d, want 3", dbState.CycleCount)
	}
}

func TestSaveStateWithDB_Error(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")

	// Create a broken StateDB by closing it before use.
	db, err := OpenStateDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	state := &AppState{CycleCount: 5, Strategies: make(map[string]*StrategyState)}
	cfg := &Config{}
	err = SaveStateWithDB(state, cfg, db)
	if err == nil {
		t.Error("expected error when SQLite is closed")
	}
}

func TestNewStrategyState_ConfigInitialCapital(t *testing.T) {
	// When config has InitialCapital set, it should be used instead of Capital.
	cfg := StrategyConfig{
		ID:             "hl-sma-btc",
		Type:           "perps",
		Platform:       "hyperliquid",
		Capital:        600,
		InitialCapital: 505,
		MaxDrawdownPct: 10,
	}
	s := NewStrategyState(cfg)
	if s.InitialCapital != 505 {
		t.Errorf("InitialCapital = %g, want 505 (from config)", s.InitialCapital)
	}
	if s.Cash != 600 {
		t.Errorf("Cash = %g, want 600 (from Capital)", s.Cash)
	}
}

func TestNewStrategyState_NoConfigInitialCapital(t *testing.T) {
	// When config has no InitialCapital, it should fall back to Capital.
	cfg := StrategyConfig{
		ID:             "hl-sma-btc",
		Type:           "perps",
		Platform:       "hyperliquid",
		Capital:        600,
		MaxDrawdownPct: 10,
	}
	s := NewStrategyState(cfg)
	if s.InitialCapital != 600 {
		t.Errorf("InitialCapital = %g, want 600 (from Capital fallback)", s.InitialCapital)
	}
}

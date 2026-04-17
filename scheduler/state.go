package main

import (
	"fmt"
	"time"
)

// maxTradeHistory is the maximum number of trades to retain per strategy.
const maxTradeHistory = 1000

// tradeRecorder is the package-level hook for immediate trade persistence (#289).
// main.go sets this to StateDB.InsertTrade after OpenStateDB; when nil (tests,
// early boot, or persistence failure), RecordTrade still appends in-memory and
// the cycle-end SaveStateWithDB acts as a safety net.
var tradeRecorder func(strategyID string, trade Trade) error

// RecordTrade appends a trade to a strategy's in-memory TradeHistory and, when
// the tradeRecorder hook is set, immediately persists it to SQLite so trades
// survive mid-cycle crashes (#289). Persistence errors are logged but do not
// abort execution — in-memory state remains intact and the end-of-cycle save
// will catch up the row on the next successful flush.
func RecordTrade(s *StrategyState, trade Trade) {
	if trade.StrategyID == "" {
		trade.StrategyID = s.ID
	}
	s.TradeHistory = append(s.TradeHistory, trade)
	if tradeRecorder == nil {
		return
	}
	if err := tradeRecorder(s.ID, trade); err != nil {
		fmt.Printf("[state] WARN: immediate trade persist failed for %s: %v\n", s.ID, err)
	}
}

// ReconciliationGap tracks the drift between virtual per-strategy positions and
// the actual on-chain position for a coin that is traded by multiple strategies
// on the same shared wallet (#258). When two strategies trade the same coin,
// per-strategy reconciliation is skipped (to prevent phantom circuit breakers)
// and this gap is computed instead.
type ReconciliationGap struct {
	Coin       string    `json:"coin"`
	OnChainQty float64   `json:"on_chain_qty"` // signed: positive = long, negative = short
	VirtualQty float64   `json:"virtual_qty"`  // sum of all strategies' positions (signed)
	DeltaQty   float64   `json:"delta_qty"`    // computed: VirtualQty - OnChainQty
	Strategies []string  `json:"strategies"`   // strategy IDs configured to trade this coin
	UpdatedAt  time.Time `json:"updated_at"`
}

// AppState holds all persistent state across restarts.
type AppState struct {
	CycleCount          int                       `json:"cycle_count"`
	LastCycle           time.Time                 `json:"last_cycle"`
	Strategies          map[string]*StrategyState `json:"strategies"`
	PortfolioRisk       PortfolioRiskState        `json:"portfolio_risk"`
	CorrelationSnapshot *CorrelationSnapshot      `json:"correlation_snapshot,omitempty"`
	// ReconciliationGaps is ephemeral — recomputed each sync cycle, not persisted to SQLite.
	ReconciliationGaps      map[string]*ReconciliationGap `json:"reconciliation_gaps,omitempty"`
	LastTop10Summary        time.Time                     `json:"last_top10_summary,omitempty"`
	LastLeaderboardPostDate string                        `json:"last_leaderboard_post_date,omitempty"`
}

// StrategyState is the per-strategy persistent state.
type StrategyState struct {
	ID              string                     `json:"id"`
	Type            string                     `json:"type"`
	Platform        string                     `json:"platform,omitempty"`
	Cash            float64                    `json:"cash"`
	InitialCapital  float64                    `json:"initial_capital"`
	Positions       map[string]*Position       `json:"positions"`
	OptionPositions map[string]*OptionPosition `json:"option_positions"`
	TradeHistory    []Trade                    `json:"trade_history"`
	RiskState       RiskState                  `json:"risk_state"`
}

func NewStrategyState(cfg StrategyConfig) *StrategyState {
	initCap := cfg.Capital
	if cfg.InitialCapital > 0 {
		initCap = cfg.InitialCapital
	}
	return &StrategyState{
		ID:              cfg.ID,
		Type:            cfg.Type,
		Platform:        cfg.Platform,
		Cash:            cfg.Capital,
		InitialCapital:  initCap,
		Positions:       make(map[string]*Position),
		OptionPositions: make(map[string]*OptionPosition),
		TradeHistory:    []Trade{},
		RiskState: RiskState{
			PeakValue:      cfg.Capital,
			MaxDrawdownPct: cfg.MaxDrawdownPct,
		},
	}
}

func NewAppState() *AppState {
	return &AppState{
		CycleCount: 0,
		Strategies: make(map[string]*StrategyState),
	}
}

// ValidateState checks loaded state for invalid entries and removes or clamps them (#39).
// Logs warnings for each corrected field rather than refusing to start.
func ValidateState(state *AppState) {
	for id, s := range state.Strategies {
		if s.InitialCapital <= 0 {
			fmt.Printf("[WARN] state: strategy %s has invalid initial_capital=%g, resetting to 0\n", id, s.InitialCapital)
			s.InitialCapital = 0
		}
		if s.Cash < 0 {
			fmt.Printf("[WARN] state: strategy %s has negative cash=%g, clamping to 0\n", id, s.Cash)
			s.Cash = 0
		}
		for sym, pos := range s.Positions {
			if pos.Quantity <= 0 {
				fmt.Printf("[WARN] state: strategy %s position %s has invalid quantity=%g, removing\n", id, sym, pos.Quantity)
				delete(s.Positions, sym)
				continue
			}
			// Migrate legacy positions: stamp ownership if missing.
			if pos.OwnerStrategyID == "" {
				pos.OwnerStrategyID = id
			}
		}
		for key, op := range s.OptionPositions {
			valid := true
			if op.Action != "buy" && op.Action != "sell" {
				fmt.Printf("[WARN] state: strategy %s option %s has invalid action=%q, removing\n", id, key, op.Action)
				valid = false
			}
			if op.OptionType != "call" && op.OptionType != "put" {
				fmt.Printf("[WARN] state: strategy %s option %s has invalid option_type=%q, removing\n", id, key, op.OptionType)
				valid = false
			}
			if op.Quantity <= 0 {
				fmt.Printf("[WARN] state: strategy %s option %s has invalid quantity=%g, removing\n", id, key, op.Quantity)
				valid = false
			}
			if !valid {
				delete(s.OptionPositions, key)
			}
		}
	}
}

// LoadStateWithDB loads state from SQLite. Returns a fresh AppState when the DB is empty.
func LoadStateWithDB(cfg *Config, sdb *StateDB) (*AppState, error) {
	state, err := sdb.LoadState()
	if err != nil {
		return nil, fmt.Errorf("sqlite load: %w", err)
	}
	if state != nil {
		fmt.Println("[state] Loaded from SQLite")
		return state, nil
	}
	return NewAppState(), nil
}

// SaveStateWithDB saves state to SQLite.
func SaveStateWithDB(state *AppState, cfg *Config, sdb *StateDB) error {
	return sdb.SaveState(state)
}

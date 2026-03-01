package alert

import (
	"fmt"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/your_github_user_or_org/zapalert/backend"
	"github.com/your_github_user_or_org/zapalert/internal/level"
)

// AlertLevel is an alias used by escalation configuration.
type AlertLevel = level.AlertLevel

// Config controls escalation behavior.
type Config struct {
	Enabled               bool
	Window                time.Duration
	BucketCount           int
	DefaultBaseAlertLevel AlertLevel
	ErrorBaseAlertLevel   AlertLevel
	Ladder                []AlertLevel
	Rules                 []Rule
	SnapshotCacheTTL      time.Duration
}

// Rule defines escalation criteria for methods.
//
// MethodPattern is a regular expression. Empty matches all methods.
type Rule struct {
	MethodPattern       string
	BaseLevel           AlertLevel
	CountThresholds     map[AlertLevel]int
	PercentThresholds   map[AlertLevel]float64
	MinimumRequestCount int
	Cooldown            time.Duration
	Deescalate          bool
}

type compiledRule struct {
	rule Rule
	re   *regexp.Regexp
}

type ruleState struct {
	level         AlertLevel
	lastEscalated time.Time
	lastEvaluated time.Time
	hasEscalated  bool
	initialized   bool
}

type snapshotCacheEntry struct {
	metrics backend.Metrics
	expires time.Time
}

// Engine evaluates alert levels from rolling metrics and rules.
type Engine struct {
	cfg       Config
	backend   backend.Backend
	rules     []compiledRule
	ladderPos map[AlertLevel]int

	mu          sync.Mutex
	states      map[string]ruleState
	snapshot    map[string]snapshotCacheEntry
	nextStateGC time.Time
	now         func() time.Time
}

// NewEngine creates an escalation engine.
func NewEngine(cfg Config, b backend.Backend) (*Engine, error) {
	if b == nil {
		return nil, fmt.Errorf("backend must not be nil")
	}
	cfg, compiled, ladderPos, err := validateAndCompile(cfg)
	if err != nil {
		return nil, err
	}

	return &Engine{
		cfg:       cfg,
		backend:   b,
		rules:     compiled,
		ladderPos: ladderPos,
		states:    make(map[string]ruleState),
		snapshot:  make(map[string]snapshotCacheEntry),
		now:       time.Now,
	}, nil
}

// ObserveRequest records a request outcome for percentage-based rules.
func (e *Engine) ObserveRequest(method string, success bool, at time.Time) error {
	if method == "" {
		return fmt.Errorf("method must not be empty")
	}
	if !e.cfg.Enabled {
		return nil
	}
	if err := e.backend.IncrRequest(method, success, at); err != nil {
		return err
	}
	e.invalidateSnapshot(method)
	return nil
}

// RecordAlert increments alert counters and returns the effective alert level.
//
// Escalation uses post-increment metrics, i.e. the current alert event is included.
func (e *Engine) RecordAlert(method string, base AlertLevel, at time.Time) (AlertLevel, error) {
	if method == "" {
		return e.defaultBase(base), fmt.Errorf("method must not be empty")
	}
	if !e.cfg.Enabled {
		return e.defaultBase(base), nil
	}

	base = e.defaultBase(base)
	if err := e.backend.IncrAlert(method, base, at); err != nil {
		return base, err
	}
	e.invalidateSnapshot(method)

	metrics, err := e.snapshotMetrics(method, at)
	if err != nil {
		return base, err
	}
	return e.evaluate(method, base, metrics, at), nil
}

func (e *Engine) defaultBase(base AlertLevel) AlertLevel {
	if base != "" {
		return base
	}
	return e.cfg.DefaultBaseAlertLevel
}

func (e *Engine) snapshotMetrics(method string, at time.Time) (backend.Metrics, error) {
	if e.cfg.SnapshotCacheTTL <= 0 {
		return e.backend.Snapshot(method, at)
	}

	e.mu.Lock()
	if entry, ok := e.snapshot[method]; ok && at.Before(entry.expires) {
		metrics := entry.metrics
		e.mu.Unlock()
		return metrics, nil
	}
	e.mu.Unlock()

	metrics, err := e.backend.Snapshot(method, at)
	if err != nil {
		return backend.Metrics{}, err
	}

	e.mu.Lock()
	e.snapshot[method] = snapshotCacheEntry{metrics: metrics, expires: at.Add(e.cfg.SnapshotCacheTTL)}
	e.mu.Unlock()
	return metrics, nil
}

func (e *Engine) invalidateSnapshot(method string) {
	if e.cfg.SnapshotCacheTTL <= 0 {
		return
	}
	e.mu.Lock()
	delete(e.snapshot, method)
	e.mu.Unlock()
}

func (e *Engine) evaluate(method string, base AlertLevel, metrics backend.Metrics, at time.Time) AlertLevel {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.gcLocked(at)

	effective := base
	for idx, compiled := range e.rules {
		if !compiled.matches(method, base) {
			continue
		}

		candidate := e.ruleCandidate(base, compiled.rule, metrics)
		stateKey := fmt.Sprintf("%d|%s", idx, method)
		state := e.states[stateKey]

		if !state.initialized {
			state = ruleState{
				level:         candidate,
				lastEvaluated: at,
				initialized:   true,
			}
			if e.greater(candidate, base) {
				state.lastEscalated = at
				state.hasEscalated = true
			}
			e.states[stateKey] = state
			effective = e.maxLevel(effective, candidate)
			continue
		}

		resolved := state.level
		switch {
		case e.greater(candidate, state.level):
			resolved = candidate
			state.level = candidate
			state.lastEscalated = at
			state.hasEscalated = true
		case e.greater(state.level, candidate):
			if compiled.rule.Deescalate {
				if compiled.rule.Cooldown > 0 && state.hasEscalated && at.Sub(state.lastEscalated) < compiled.rule.Cooldown {
					resolved = state.level
				} else {
					resolved = candidate
					state.level = candidate
				}
			} else {
				if state.hasEscalated && at.Sub(state.lastEscalated) < e.cfg.Window {
					resolved = state.level
				} else {
					resolved = candidate
					state.level = candidate
				}
			}
		default:
			resolved = state.level
		}

		state.lastEvaluated = at
		e.states[stateKey] = state
		effective = e.maxLevel(effective, resolved)
	}
	return effective
}

func (e *Engine) ruleCandidate(base AlertLevel, rule Rule, metrics backend.Metrics) AlertLevel {
	candidate := base
	for _, step := range sortedCountThresholds(rule.CountThresholds) {
		if metrics.AlertCount < step.threshold {
			continue
		}
		next, ok := e.nextLevel(step.level)
		if !ok {
			continue
		}
		candidate = e.maxLevel(candidate, next)
	}

	if metrics.RequestTotal >= rule.MinimumRequestCount {
		for _, step := range sortedPercentThresholds(rule.PercentThresholds) {
			if metrics.FailureRate < step.threshold {
				continue
			}
			next, ok := e.nextLevel(step.level)
			if !ok {
				continue
			}
			candidate = e.maxLevel(candidate, next)
		}
	}

	return candidate
}

func (e *Engine) maxLevel(a, b AlertLevel) AlertLevel {
	if e.greater(b, a) {
		return b
	}
	return a
}

func (e *Engine) greater(a, b AlertLevel) bool {
	return e.rank(a) > e.rank(b)
}

func (e *Engine) rank(l AlertLevel) int {
	if idx, ok := e.ladderPos[l]; ok {
		return idx + 1
	}
	return 0
}

func (e *Engine) nextLevel(l AlertLevel) (AlertLevel, bool) {
	pos, ok := e.ladderPos[l]
	if !ok {
		return "", false
	}
	next := pos + 1
	if next >= len(e.cfg.Ladder) {
		return "", false
	}
	return e.cfg.Ladder[next], true
}

func (e *Engine) gcLocked(now time.Time) {
	if !e.nextStateGC.IsZero() && now.Before(e.nextStateGC) {
		return
	}
	maxAge := 2 * e.cfg.Window
	for key, state := range e.states {
		if now.Sub(state.lastEvaluated) > maxAge {
			delete(e.states, key)
		}
	}
	for method, entry := range e.snapshot {
		if now.After(entry.expires) {
			delete(e.snapshot, method)
		}
	}
	e.nextStateGC = now.Add(e.cfg.Window)
}

func validateAndCompile(cfg Config) (Config, []compiledRule, map[AlertLevel]int, error) {
	if !cfg.Enabled {
		cfg.Enabled = false
		if cfg.DefaultBaseAlertLevel == "" {
			cfg.DefaultBaseAlertLevel = AlertLevel("NONE")
		}
		if cfg.ErrorBaseAlertLevel == "" {
			cfg.ErrorBaseAlertLevel = cfg.DefaultBaseAlertLevel
		}
		return cfg, nil, nil, nil
	}

	if cfg.Window <= 0 {
		return Config{}, nil, nil, fmt.Errorf("escalation window must be > 0")
	}
	if cfg.BucketCount <= 0 {
		return Config{}, nil, nil, fmt.Errorf("escalation bucket count must be > 0")
	}
	if cfg.Window/time.Duration(cfg.BucketCount) <= 0 {
		return Config{}, nil, nil, fmt.Errorf("escalation bucket size must be > 0")
	}
	if len(cfg.Ladder) == 0 {
		return Config{}, nil, nil, fmt.Errorf("escalation ladder must not be empty")
	}

	ladderPos := make(map[AlertLevel]int, len(cfg.Ladder))
	for idx, lvl := range cfg.Ladder {
		if lvl == "" {
			return Config{}, nil, nil, fmt.Errorf("ladder level at index %d is empty", idx)
		}
		if _, exists := ladderPos[lvl]; exists {
			return Config{}, nil, nil, fmt.Errorf("ladder level %q is duplicated", lvl)
		}
		ladderPos[lvl] = idx
	}

	if cfg.DefaultBaseAlertLevel == "" {
		cfg.DefaultBaseAlertLevel = AlertLevel("NONE")
	}
	if cfg.ErrorBaseAlertLevel == "" {
		cfg.ErrorBaseAlertLevel = cfg.DefaultBaseAlertLevel
	}

	compiled := make([]compiledRule, 0, len(cfg.Rules))
	for i, r := range cfg.Rules {
		if r.MinimumRequestCount < 0 {
			return Config{}, nil, nil, fmt.Errorf("rule %d minimum request count must be >= 0", i)
		}
		if r.Cooldown < 0 {
			return Config{}, nil, nil, fmt.Errorf("rule %d cooldown must be >= 0", i)
		}
		if err := validateThresholdLevels(i, "count", r.CountThresholds, ladderPos); err != nil {
			return Config{}, nil, nil, err
		}
		if err := validateThresholdLevels(i, "percent", r.PercentThresholds, ladderPos); err != nil {
			return Config{}, nil, nil, err
		}
		if err := validateCountThresholds(i, r.CountThresholds); err != nil {
			return Config{}, nil, nil, err
		}
		if err := validatePercentThresholds(i, r.PercentThresholds); err != nil {
			return Config{}, nil, nil, err
		}

		var re *regexp.Regexp
		if r.MethodPattern != "" {
			compiledRE, err := regexp.Compile(r.MethodPattern)
			if err != nil {
				return Config{}, nil, nil, fmt.Errorf("rule %d invalid method pattern: %w", i, err)
			}
			re = compiledRE
		}
		compiled = append(compiled, compiledRule{rule: r, re: re})
	}

	return cfg, compiled, ladderPos, nil
}

func validateCountThresholds(idx int, thresholds map[AlertLevel]int) error {
	for lvl, threshold := range thresholds {
		if threshold <= 0 {
			return fmt.Errorf("rule %d count threshold for %q must be > 0", idx, lvl)
		}
	}
	return nil
}

func validatePercentThresholds(idx int, thresholds map[AlertLevel]float64) error {
	for lvl, threshold := range thresholds {
		if threshold <= 0 || threshold > 1 {
			return fmt.Errorf("rule %d percent threshold for %q must be in (0, 1]", idx, lvl)
		}
	}
	return nil
}

func validateThresholdLevels[T any](idx int, kind string, thresholds map[AlertLevel]T, ladderPos map[AlertLevel]int) error {
	for lvl := range thresholds {
		pos, ok := ladderPos[lvl]
		if !ok {
			return fmt.Errorf("rule %d %s threshold level %q is not present in ladder", idx, kind, lvl)
		}
		if pos+1 >= len(ladderPos) {
			return fmt.Errorf("rule %d %s threshold level %q has no higher level to escalate to", idx, kind, lvl)
		}
	}
	return nil
}

func (r compiledRule) matches(method string, base AlertLevel) bool {
	if r.rule.BaseLevel != "" && r.rule.BaseLevel != base {
		return false
	}
	if r.re == nil {
		return true
	}
	return r.re.MatchString(method)
}

type countThreshold struct {
	level     AlertLevel
	threshold int
}

func sortedCountThresholds(in map[AlertLevel]int) []countThreshold {
	out := make([]countThreshold, 0, len(in))
	for level, threshold := range in {
		out = append(out, countThreshold{level: level, threshold: threshold})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].threshold < out[j].threshold
	})
	return out
}

type percentThreshold struct {
	level     AlertLevel
	threshold float64
}

func sortedPercentThresholds(in map[AlertLevel]float64) []percentThreshold {
	out := make([]percentThreshold, 0, len(in))
	for level, threshold := range in {
		out = append(out, percentThreshold{level: level, threshold: threshold})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].threshold < out[j].threshold
	})
	return out
}

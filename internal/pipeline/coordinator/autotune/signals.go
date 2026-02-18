package autotune

import (
	"sort"
	"strings"
	"sync"
)

const (
	autoTuneSignalRPCWindowSize      = 64
	autoTuneSignalCommitWindowSize   = 64
	autoTuneDefaultChainRuntimeGroup = 1
)

// AutoTuneRuntimeSignals are chain-local inputs consumed by auto-tune input routing.
type AutoTuneRuntimeSignals struct {
	RPCErrorRateBps      int
	DBCommitLatencyP95Ms int
}

// AutoTuneSignalSource exposes the latest chain-local runtime signals.
type AutoTuneSignalSource interface {
	// Snapshot returns chain-local values for the given runtime chain/network pair.
	Snapshot(chain, network string) AutoTuneRuntimeSignals
}

// AutoTuneSignalSink records chain-local runtime events for downstream auto-tune inputs.
type AutoTuneSignalSink interface {
	RecordRPCResult(chain, network string, isError bool)
	RecordDBCommitLatencyMs(chain, network string, latencyMs int64)
}

// AutoTuneSignalCollector combines read/write access to chain-local runtime signal telemetry.
type AutoTuneSignalCollector interface {
	AutoTuneSignalSource
	AutoTuneSignalSink
}

// RuntimeSignalRegistry stores rolling telemetry for chain-local auto-tune signal inputs.
type RuntimeSignalRegistry struct {
	mu    sync.RWMutex
	byKey map[string]*autoTuneSignalSeries
}

// NewRuntimeSignalRegistry returns an empty chain-scoped signal registry.
func NewRuntimeSignalRegistry() *RuntimeSignalRegistry {
	return &RuntimeSignalRegistry{
		byKey: make(map[string]*autoTuneSignalSeries, autoTuneDefaultChainRuntimeGroup),
	}
}

// Snapshot returns the latest chain-local signal envelope for a chain/network pair.
func (r *RuntimeSignalRegistry) Snapshot(chain, network string) AutoTuneRuntimeSignals {
	if r == nil {
		return AutoTuneRuntimeSignals{}
	}
	key := autoTuneSignalKey(chain, network)

	r.mu.RLock()
	state, ok := r.byKey[key]
	r.mu.RUnlock()
	if !ok || state == nil {
		return AutoTuneRuntimeSignals{}
	}

	return AutoTuneRuntimeSignals{
		RPCErrorRateBps:      state.rpcRate.errorRateBps(),
		DBCommitLatencyP95Ms: state.dbCommitLatency.p95Ms(),
	}
}

// RecordRPCResult records one RPC outcome for the given chain/network.
func (r *RuntimeSignalRegistry) RecordRPCResult(chain, network string, isError bool) {
	if r == nil {
		return
	}
	key := autoTuneSignalKey(chain, network)
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.stateFor(key)
	state.rpcRate.record(isError)
}

// RecordDBCommitLatencyMs records a commit-latency sample for the given chain/network.
func (r *RuntimeSignalRegistry) RecordDBCommitLatencyMs(chain, network string, latencyMs int64) {
	if r == nil || latencyMs < 0 {
		return
	}
	key := autoTuneSignalKey(chain, network)
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.stateFor(key)
	state.dbCommitLatency.record(latencyMs)
}

func (r *RuntimeSignalRegistry) stateFor(key string) *autoTuneSignalSeries {
	state, ok := r.byKey[key]
	if !ok || state == nil {
		state = &autoTuneSignalSeries{}
		r.byKey[key] = state
	}
	return state
}

func autoTuneSignalKey(chain, network string) string {
	return strings.ToLower(strings.TrimSpace(chain)) + "/" + strings.ToLower(strings.TrimSpace(network))
}

type autoTuneSignalSeries struct {
	rpcRate         rpcErrorRateSeries
	dbCommitLatency commitLatencySeries
}

type rpcErrorRateSeries struct {
	samples     [autoTuneSignalRPCWindowSize]uint8
	nextIndex   int
	sampleCount int
	errorCount  int
}

func (s *rpcErrorRateSeries) record(isError bool) {
	if s == nil {
		return
	}
	old := s.samples[s.nextIndex]
	if s.sampleCount < autoTuneSignalRPCWindowSize {
		s.sampleCount++
	} else if old == 1 {
		s.errorCount--
	}

	var next byte
	if isError {
		next = 1
		s.errorCount++
	}
	s.samples[s.nextIndex] = next
	s.nextIndex++
	if s.nextIndex >= autoTuneSignalRPCWindowSize {
		s.nextIndex = 0
	}
}

func (s rpcErrorRateSeries) errorRateBps() int {
	if s.sampleCount == 0 {
		return 0
	}
	return (s.errorCount * 10_000) / s.sampleCount
}

type commitLatencySeries struct {
	commitP95Window [autoTuneSignalCommitWindowSize]int64
	nextIndex       int
	sampleCount     int
}

func (s *commitLatencySeries) record(latencyMs int64) {
	if s == nil {
		return
	}
	if latencyMs < 0 {
		return
	}

	s.commitP95Window[s.nextIndex] = latencyMs
	s.nextIndex++
	if s.nextIndex >= autoTuneSignalCommitWindowSize {
		s.nextIndex = 0
	}
	if s.sampleCount < autoTuneSignalCommitWindowSize {
		s.sampleCount++
	}
}

func (s commitLatencySeries) p95Ms() int {
	if s.sampleCount == 0 {
		return 0
	}
	samples := make([]int, 0, s.sampleCount)
	for i := 0; i < s.sampleCount; i++ {
		samples = append(samples, int(s.commitP95Window[i]))
	}
	sort.Ints(samples)
	idx := (len(samples)*95 + 99) / 100
	if idx > 0 {
		idx--
	}
	if idx >= len(samples) {
		idx = len(samples) - 1
	}
	return samples[idx]
}

package ingester

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

// CommitInterleaver coordinates cross-chain commit turn-taking so equivalent
// completion-order permutations converge to deterministic commit boundaries.
type CommitInterleaver interface {
	Acquire(ctx context.Context, chain model.Chain, network model.Network) (func(committed bool), error)
}

type interleaveTarget struct {
	chain   model.Chain
	network model.Network
}

type deterministicMandatoryChainInterleaver struct {
	maxSkewWait time.Duration
	order       []string
	orderIndex  map[string]int

	mu      sync.Mutex
	active  string
	next    int
	waiting map[string]int
	notify  chan struct{}
}

func NewDeterministicMandatoryChainInterleaver(maxSkewWait time.Duration) CommitInterleaver {
	return newDeterministicCommitInterleaver([]interleaveTarget{
		{chain: model.ChainSolana, network: model.NetworkDevnet},
		{chain: model.ChainBase, network: model.NetworkSepolia},
		{chain: model.ChainBTC, network: model.NetworkTestnet},
	}, maxSkewWait)
}

func newDeterministicCommitInterleaver(targets []interleaveTarget, maxSkewWait time.Duration) CommitInterleaver {
	order := make([]string, 0, len(targets))
	orderIndex := make(map[string]int, len(targets))

	for _, target := range targets {
		key := commitInterleaveKey(target.chain, target.network)
		if _, exists := orderIndex[key]; exists {
			continue
		}
		orderIndex[key] = len(order)
		order = append(order, key)
	}

	return &deterministicMandatoryChainInterleaver{
		maxSkewWait: maxSkewWait,
		order:       order,
		orderIndex:  orderIndex,
		waiting:     make(map[string]int, len(order)),
		notify:      make(chan struct{}),
	}
}

func (d *deterministicMandatoryChainInterleaver) Acquire(
	ctx context.Context,
	chain model.Chain,
	network model.Network,
) (func(committed bool), error) {
	key := commitInterleaveKey(chain, network)
	index, tracked := d.orderIndex[key]
	if !tracked {
		return func(bool) {}, nil
	}

	deadline := time.Time{}
	if d.maxSkewWait > 0 {
		deadline = time.Now().Add(d.maxSkewWait)
	}

	d.mu.Lock()
	d.waiting[key]++
	d.mu.Unlock()

	for {
		d.mu.Lock()
		if d.canAcquireLocked(key, deadline) {
			d.waiting[key]--
			d.active = key
			d.mu.Unlock()
			return d.releaseFunc(key, index), nil
		}
		waitCh := d.notify
		waitFor := d.waitDuration(deadline)
		d.mu.Unlock()

		if waitFor <= 0 {
			waitFor = time.Millisecond
		}

		timer := time.NewTimer(waitFor)
		select {
		case <-ctx.Done():
			timer.Stop()
			d.cancelWaiter(key)
			return nil, fmt.Errorf("commit interleaving: %w", ctx.Err())
		case <-waitCh:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (d *deterministicMandatoryChainInterleaver) canAcquireLocked(key string, deadline time.Time) bool {
	if d.active != "" || len(d.order) == 0 {
		return false
	}

	preferred := d.order[d.next]
	if key == preferred {
		return true
	}
	if d.waiting[preferred] > 0 {
		return false
	}
	if deadline.IsZero() {
		return true
	}
	return !time.Now().Before(deadline)
}

func (d *deterministicMandatoryChainInterleaver) waitDuration(deadline time.Time) time.Duration {
	if deadline.IsZero() {
		return 0
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0
	}
	return remaining
}

func (d *deterministicMandatoryChainInterleaver) cancelWaiter(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.waiting[key] > 0 {
		d.waiting[key]--
		d.signalLocked()
	}
}

func (d *deterministicMandatoryChainInterleaver) releaseFunc(key string, index int) func(committed bool) {
	released := false
	return func(committed bool) {
		d.mu.Lock()
		defer d.mu.Unlock()
		if released {
			return
		}
		released = true

		if d.active == key {
			d.active = ""
		}
		if committed && len(d.order) > 0 {
			d.next = (index + 1) % len(d.order)
		}
		d.signalLocked()
	}
}

func (d *deterministicMandatoryChainInterleaver) signalLocked() {
	close(d.notify)
	d.notify = make(chan struct{})
}

func commitInterleaveKey(chain model.Chain, network model.Network) string {
	return fmt.Sprintf("%s-%s", chain, network)
}

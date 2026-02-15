package ingester

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

type CommitInterleaver interface {
	Acquire(ctx context.Context, chain model.Chain, network model.Network) (func(), error)
}

type deterministicDualChainInterleaver struct {
	maxSkewWait time.Duration
	order       []string
	orderIndex  map[string]int

	mu      sync.Mutex
	active  string
	next    int
	waiting map[string]int
	notify  chan struct{}
}

func NewDeterministicDualChainInterleaver(maxSkewWait time.Duration) CommitInterleaver {
	order := []string{
		interleaveKey(model.ChainSolana, model.NetworkDevnet),
		interleaveKey(model.ChainBase, model.NetworkSepolia),
	}

	index := make(map[string]int, len(order))
	for i, key := range order {
		index[key] = i
	}

	return &deterministicDualChainInterleaver{
		maxSkewWait: maxSkewWait,
		order:       order,
		orderIndex:  index,
		waiting:     make(map[string]int, len(order)),
		notify:      make(chan struct{}),
	}
}

func (d *deterministicDualChainInterleaver) Acquire(
	ctx context.Context,
	chain model.Chain,
	network model.Network,
) (func(), error) {
	key := interleaveKey(chain, network)
	index, ok := d.orderIndex[key]
	if !ok {
		return func() {}, nil
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
		waitDuration := d.waitDuration(deadline)
		d.mu.Unlock()

		if waitDuration <= 0 {
			// Re-check promptly when skew budget expires to avoid stalling on racey deadlines.
			waitDuration = time.Millisecond
		}

		timer := time.NewTimer(waitDuration)
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

func (d *deterministicDualChainInterleaver) canAcquireLocked(key string, deadline time.Time) bool {
	if d.active != "" {
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

func (d *deterministicDualChainInterleaver) waitDuration(deadline time.Time) time.Duration {
	if deadline.IsZero() {
		return 0
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0
	}
	return remaining
}

func (d *deterministicDualChainInterleaver) cancelWaiter(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.waiting[key] > 0 {
		d.waiting[key]--
		d.signalLocked()
	}
}

func (d *deterministicDualChainInterleaver) releaseFunc(key string, index int) func() {
	return func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.active == key {
			d.active = ""
		}
		d.next = (index + 1) % len(d.order)
		d.signalLocked()
	}
}

func (d *deterministicDualChainInterleaver) signalLocked() {
	close(d.notify)
	d.notify = make(chan struct{})
}

func interleaveKey(chain model.Chain, network model.Network) string {
	return fmt.Sprintf("%s-%s", chain, network)
}

package pipeline

import (
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()

	p := &Pipeline{
		cfg: Config{
			Chain:   model.ChainBase,
			Network: model.NetworkMainnet,
		},
	}
	r.Register(p)

	got := r.Get(model.ChainBase, model.NetworkMainnet)
	if got != p {
		t.Fatalf("expected registered pipeline, got %v", got)
	}
}

func TestRegistry_GetNotFound(t *testing.T) {
	r := NewRegistry()

	got := r.Get(model.ChainSolana, model.NetworkDevnet)
	if got != nil {
		t.Fatalf("expected nil for unregistered pipeline, got %v", got)
	}
}

func TestRegistry_MultipleChains(t *testing.T) {
	r := NewRegistry()

	p1 := &Pipeline{cfg: Config{Chain: model.ChainBase, Network: model.NetworkMainnet}}
	p2 := &Pipeline{cfg: Config{Chain: model.ChainSolana, Network: model.NetworkDevnet}}
	r.Register(p1)
	r.Register(p2)

	if got := r.Get(model.ChainBase, model.NetworkMainnet); got != p1 {
		t.Error("expected p1 for base:mainnet")
	}
	if got := r.Get(model.ChainSolana, model.NetworkDevnet); got != p2 {
		t.Error("expected p2 for solana:devnet")
	}
	if got := r.Get(model.ChainBTC, model.NetworkTestnet); got != nil {
		t.Error("expected nil for unregistered btc:testnet")
	}
}

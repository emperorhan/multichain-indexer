package coordinator

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/store"
)

// Coordinator iterates over watched addresses and creates FetchJobs.
type Coordinator struct {
	chain           model.Chain
	network         model.Network
	watchedAddrRepo store.WatchedAddressRepository
	cursorRepo      store.CursorRepository
	batchSize       int
	interval        time.Duration
	jobCh           chan<- event.FetchJob
	logger          *slog.Logger
}

func New(
	chain model.Chain,
	network model.Network,
	watchedAddrRepo store.WatchedAddressRepository,
	cursorRepo store.CursorRepository,
	batchSize int,
	interval time.Duration,
	jobCh chan<- event.FetchJob,
	logger *slog.Logger,
) *Coordinator {
	return &Coordinator{
		chain:           chain,
		network:         network,
		watchedAddrRepo: watchedAddrRepo,
		cursorRepo:      cursorRepo,
		batchSize:       batchSize,
		interval:        interval,
		jobCh:           jobCh,
		logger:          logger.With("component", "coordinator"),
	}
}

func (c *Coordinator) Run(ctx context.Context) error {
	c.logger.Info("coordinator started", "chain", c.chain, "network", c.network, "interval", c.interval)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	// Run immediately on start, then on interval
	if err := c.tick(ctx); err != nil {
		panic(fmt.Sprintf("coordinator tick failed: %v", err))
	}

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("coordinator stopping")
			return ctx.Err()
		case <-ticker.C:
			if err := c.tick(ctx); err != nil {
				panic(fmt.Sprintf("coordinator tick failed: %v", err))
			}
		}
	}
}

func (c *Coordinator) tick(ctx context.Context) error {
	addresses, err := c.watchedAddrRepo.GetActive(ctx, c.chain, c.network)
	if err != nil {
		return err
	}

	groups := groupWatchedAddresses(c.chain, addresses)
	c.logger.Debug("creating fetch jobs", "address_count", len(addresses), "fan_in_group_count", len(groups))

	for _, group := range groups {
		candidates := make([]watchedAddressCandidate, 0, len(group.members))
		for _, member := range group.members {
			cursor, err := c.cursorRepo.Get(ctx, c.chain, c.network, member.Address)
			if err != nil {
				return fmt.Errorf("get cursor %s: %w", member.Address, err)
			}
			candidates = append(candidates, watchedAddressCandidate{
				address: member,
				cursor:  cursor,
			})
		}

		representative := selectRepresentativeCandidate(c.chain, group.identity, candidates)
		cursorValue, cursorSequence := resolveGroupCursor(c.chain, candidates)

		job := event.FetchJob{
			Chain:          c.chain,
			Network:        c.network,
			Address:        representative.address.Address,
			CursorValue:    cursorValue,
			CursorSequence: cursorSequence,
			BatchSize:      c.batchSize,
			WalletID:       representative.address.WalletID,
			OrgID:          representative.address.OrganizationID,
		}

		select {
		case c.jobCh <- job:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

type watchedAddressGroup struct {
	identity string
	members  []model.WatchedAddress
}

type watchedAddressCandidate struct {
	address model.WatchedAddress
	cursor  *model.AddressCursor
}

func groupWatchedAddresses(chain model.Chain, addresses []model.WatchedAddress) []watchedAddressGroup {
	if len(addresses) == 0 {
		return nil
	}

	groupsByIdentity := make(map[string][]model.WatchedAddress, len(addresses))
	for _, addr := range addresses {
		identity := canonicalWatchedAddressIdentity(chain, addr.Address)
		if identity == "" {
			continue
		}
		groupsByIdentity[identity] = append(groupsByIdentity[identity], addr)
	}

	identities := make([]string, 0, len(groupsByIdentity))
	for identity := range groupsByIdentity {
		identities = append(identities, identity)
	}
	sort.Strings(identities)

	groups := make([]watchedAddressGroup, 0, len(identities))
	for _, identity := range identities {
		members := append([]model.WatchedAddress(nil), groupsByIdentity[identity]...)
		sort.Slice(members, func(i, j int) bool {
			leftKey := stableAddressOrderKey(chain, members[i].Address)
			rightKey := stableAddressOrderKey(chain, members[j].Address)
			if leftKey != rightKey {
				return leftKey < rightKey
			}
			return strings.TrimSpace(members[i].Address) < strings.TrimSpace(members[j].Address)
		})
		groups = append(groups, watchedAddressGroup{
			identity: identity,
			members:  members,
		})
	}

	return groups
}

func selectRepresentativeCandidate(
	chain model.Chain,
	identity string,
	candidates []watchedAddressCandidate,
) watchedAddressCandidate {
	if len(candidates) == 0 {
		return watchedAddressCandidate{}
	}

	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if shouldReplaceRepresentative(chain, identity, best, candidate) {
			best = candidate
		}
	}
	return best
}

func shouldReplaceRepresentative(
	chain model.Chain,
	identity string,
	existing watchedAddressCandidate,
	incoming watchedAddressCandidate,
) bool {
	existingSeq := int64(-1)
	if existing.cursor != nil {
		existingSeq = existing.cursor.CursorSequence
	}
	incomingSeq := int64(-1)
	if incoming.cursor != nil {
		incomingSeq = incoming.cursor.CursorSequence
	}
	if existingSeq != incomingSeq {
		return incomingSeq > existingSeq
	}

	existingCanonical := canonicalWatchedAddressIdentity(chain, existing.address.Address) == identity &&
		strings.TrimSpace(existing.address.Address) == identity
	incomingCanonical := canonicalWatchedAddressIdentity(chain, incoming.address.Address) == identity &&
		strings.TrimSpace(incoming.address.Address) == identity
	if existingCanonical != incomingCanonical {
		return incomingCanonical
	}

	existingKey := stableAddressOrderKey(chain, existing.address.Address)
	incomingKey := stableAddressOrderKey(chain, incoming.address.Address)
	if existingKey != incomingKey {
		return incomingKey < existingKey
	}

	return strings.TrimSpace(incoming.address.Address) < strings.TrimSpace(existing.address.Address)
}

func resolveGroupCursor(chain model.Chain, candidates []watchedAddressCandidate) (*string, int64) {
	var (
		cursorValue *string
		cursorSeq   int64
		set         bool
	)

	for _, candidate := range candidates {
		if candidate.cursor == nil {
			continue
		}
		candidateValue := canonicalizeCursorValue(chain, candidate.cursor.CursorValue)
		candidateSeq := candidate.cursor.CursorSequence

		if !set || candidateSeq > cursorSeq || (candidateSeq == cursorSeq && shouldReplaceCursorValue(cursorValue, candidateValue)) {
			cursorValue = candidateValue
			cursorSeq = candidateSeq
			set = true
		}
	}
	if !set {
		return nil, 0
	}
	return cursorValue, cursorSeq
}

func shouldReplaceCursorValue(existing, incoming *string) bool {
	if existing == nil {
		return incoming != nil
	}
	if incoming == nil {
		return false
	}
	return *incoming < *existing
}

func stableAddressOrderKey(chain model.Chain, address string) string {
	trimmed := strings.TrimSpace(address)
	if isEVMChain(chain) {
		return strings.ToLower(trimmed)
	}
	return trimmed
}

func canonicalWatchedAddressIdentity(chain model.Chain, address string) string {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return ""
	}
	if !isEVMChain(chain) {
		return trimmed
	}

	withoutPrefix := strings.TrimPrefix(strings.TrimPrefix(trimmed, "0x"), "0X")
	if withoutPrefix == "" {
		return ""
	}
	if isHexString(withoutPrefix) {
		return "0x" + strings.ToLower(withoutPrefix)
	}
	if strings.HasPrefix(trimmed, "0x") || strings.HasPrefix(trimmed, "0X") {
		return "0x" + strings.ToLower(withoutPrefix)
	}
	return strings.ToLower(trimmed)
}

func canonicalizeCursorValue(chain model.Chain, cursor *string) *string {
	if cursor == nil {
		return nil
	}
	value := canonicalSignatureIdentity(chain, *cursor)
	if value == "" {
		return nil
	}
	return &value
}

func canonicalSignatureIdentity(chain model.Chain, hash string) string {
	trimmed := strings.TrimSpace(hash)
	if trimmed == "" {
		return ""
	}
	if !isEVMChain(chain) {
		return trimmed
	}

	withoutPrefix := strings.TrimPrefix(strings.TrimPrefix(trimmed, "0x"), "0X")
	if withoutPrefix == "" {
		return ""
	}
	if isHexString(withoutPrefix) {
		return "0x" + strings.ToLower(withoutPrefix)
	}
	if strings.HasPrefix(trimmed, "0x") || strings.HasPrefix(trimmed, "0X") {
		return "0x" + strings.ToLower(withoutPrefix)
	}
	return trimmed
}

func isEVMChain(chain model.Chain) bool {
	return chain == model.ChainBase || chain == model.ChainEthereum
}

func isHexString(v string) bool {
	for _, ch := range v {
		switch {
		case ch >= '0' && ch <= '9':
		case ch >= 'a' && ch <= 'f':
		case ch >= 'A' && ch <= 'F':
		default:
			return false
		}
	}
	return true
}

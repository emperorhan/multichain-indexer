import { BalanceEvent } from './types';

interface TokenBalanceMeta {
  owner: string;
  mint: string;
  amount: string;
}

type BalanceMapKey = string; // "owner|mint"

function balanceMapKey(owner: string, mint: string): BalanceMapKey {
  return `${owner}|${mint}`;
}

export type ScamSignal = 'zero_balance_withdrawal' | 'phantom_deposit';

export class BalanceValidator {
  /**
   * Build a lookup map from Solana transaction meta preTokenBalances/postTokenBalances.
   * Key: "owner|mint" → { pre: TokenBalanceMeta | null, post: TokenBalanceMeta | null }
   */
  static buildBalanceMap(
    meta: any,
  ): Map<BalanceMapKey, { pre: TokenBalanceMeta | null; post: TokenBalanceMeta | null }> {
    const map = new Map<BalanceMapKey, { pre: TokenBalanceMeta | null; post: TokenBalanceMeta | null }>();

    for (const tb of meta?.preTokenBalances || []) {
      if (!tb.owner || !tb.mint) continue;
      const key = balanceMapKey(tb.owner, tb.mint);
      const entry = map.get(key) || { pre: null, post: null };
      entry.pre = {
        owner: tb.owner,
        mint: tb.mint,
        amount: tb.uiTokenAmount?.amount || '0',
      };
      map.set(key, entry);
    }

    for (const tb of meta?.postTokenBalances || []) {
      if (!tb.owner || !tb.mint) continue;
      const key = balanceMapKey(tb.owner, tb.mint);
      const entry = map.get(key) || { pre: null, post: null };
      entry.post = {
        owner: tb.owner,
        mint: tb.mint,
        amount: tb.uiTokenAmount?.amount || '0',
      };
      map.set(key, entry);
    }

    return map;
  }

  /**
   * Validate a balance event against the on-chain balance map.
   * Returns the scam signal name if suspicious, or null if clean.
   */
  static validateEvent(
    event: BalanceEvent,
    balanceMap: Map<BalanceMapKey, { pre: TokenBalanceMeta | null; post: TokenBalanceMeta | null }>,
  ): ScamSignal | null {
    // Skip native tokens — fee deductions from zero balance are normal
    if (event.tokenType === 'NATIVE') return null;

    const key = balanceMapKey(event.address, event.contractAddress);
    const entry = balanceMap.get(key);

    const deltaNum = BigInt(event.delta || '0');

    // zero_balance_withdrawal: pre-balance is 0 or absent, but negative delta
    if (deltaNum < 0n) {
      if (!entry || !entry.pre || BigInt(entry.pre.amount) === 0n) {
        return 'zero_balance_withdrawal';
      }
    }

    // phantom_deposit: pre-balance exists but post-balance absent, yet positive delta
    if (deltaNum > 0n) {
      if (entry && entry.pre && !entry.post) {
        return 'phantom_deposit';
      }
    }

    return null;
  }
}

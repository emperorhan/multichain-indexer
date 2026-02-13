import { BalanceEvent, ParsedOuterInstruction } from './types';

export interface EventPlugin {
  readonly id: string;
  readonly priority: number; // higher = higher priority
  readonly programIds: ReadonlySet<string>;

  /**
   * Parse an outer instruction (with its inner instructions) into balance events.
   *
   * Returns:
   * - BalanceEvent[]: plugin claims ownership, inner ixs consumed. Empty array = "I own this, no events".
   * - null: plugin does not handle this instruction, try next plugin.
   */
  parse(
    outer: ParsedOuterInstruction,
    watchedSet: Set<string>,
    accountKeyMap: Map<number, string>,
    meta: any,
  ): BalanceEvent[] | null;
}

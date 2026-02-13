import { EventPlugin } from './interface';
import { BalanceEvent, ParsedOuterInstruction, ParsedInnerInstruction } from './types';

export class PluginDispatcher {
  /** programId → plugins sorted by priority DESC */
  private readonly programIndex: Map<string, EventPlugin[]>;
  /** All registered plugins sorted by priority DESC (for fallback) */
  private readonly fallbackPlugins: EventPlugin[];

  constructor(plugins: EventPlugin[]) {
    this.programIndex = new Map();
    // Sort by priority descending
    const sorted = [...plugins].sort((a, b) => b.priority - a.priority);
    this.fallbackPlugins = sorted;

    for (const plugin of sorted) {
      for (const pid of plugin.programIds) {
        const existing = this.programIndex.get(pid) || [];
        existing.push(plugin);
        this.programIndex.set(pid, existing);
      }
    }
  }

  dispatch(
    outerInstructions: ParsedOuterInstruction[],
    watchedSet: Set<string>,
    accountKeyMap: Map<number, string>,
    meta: any,
  ): BalanceEvent[] {
    const allEvents: BalanceEvent[] = [];

    for (const outer of outerInstructions) {
      // 1. Try matching plugins by outer programId (priority DESC)
      const candidates = this.programIndex.get(outer.programId) || [];
      let claimed = false;

      for (const plugin of candidates) {
        const result = plugin.parse(outer, watchedSet, accountKeyMap, meta);
        if (result !== null) {
          allEvents.push(...result);
          claimed = true; // All inner instructions consumed
          break;
        }
      }

      if (claimed) continue;

      // 2. Unclaimed: try outer instruction itself as fallback
      const outerEvents = this.fallbackSingleInstruction(
        outer.outerIndex,
        -1,
        outer.programId,
        outer.instruction,
        watchedSet,
        accountKeyMap,
        meta,
      );
      allEvents.push(...outerEvents);

      // 3. Try each inner instruction individually as fallback
      for (const inner of outer.innerInstructions) {
        const innerEvents = this.fallbackSingleInstruction(
          outer.outerIndex,
          inner.innerIndex,
          inner.programId,
          inner.instruction,
          watchedSet,
          accountKeyMap,
          meta,
        );
        allEvents.push(...innerEvents);
      }
    }

    return allEvents;
  }

  private fallbackSingleInstruction(
    outerIndex: number,
    innerIndex: number,
    programId: string,
    instruction: any,
    watchedSet: Set<string>,
    accountKeyMap: Map<number, string>,
    meta: any,
  ): BalanceEvent[] {
    const candidates = this.programIndex.get(programId) || [];

    // Build a synthetic ParsedOuterInstruction with no inner instructions
    const syntheticOuter: ParsedOuterInstruction = {
      outerIndex,
      programId,
      instruction,
      innerInstructions: [],
    };

    for (const plugin of candidates) {
      const result = plugin.parse(syntheticOuter, watchedSet, accountKeyMap, meta);
      if (result !== null) {
        // Override indices: these events come from a specific inner instruction
        return result.map((ev) => ({
          ...ev,
          outerInstructionIndex: outerIndex,
          innerInstructionIndex: innerIndex,
        }));
      }
    }

    return [];
  }
}

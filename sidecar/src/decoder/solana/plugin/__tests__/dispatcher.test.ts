import { describe, it, expect } from 'vitest';
import { PluginDispatcher } from '../dispatcher';
import type { EventPlugin } from '../interface';
import type { BalanceEvent, ParsedOuterInstruction } from '../types';

const SYSTEM_PROGRAM = '11111111111111111111111111111111';
const SPL_TOKEN_PROGRAM = 'TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA';
const MARINADE_PROGRAM = 'MarBpjdStaking111111111111111111111111111';

function makeOuter(
  outerIndex: number,
  programId: string,
  instruction: any,
  innerInstructions: Array<{ innerIndex: number; programId: string; instruction: any }> = [],
): ParsedOuterInstruction {
  return {
    outerIndex,
    programId,
    instruction,
    innerInstructions: innerInstructions.map((ix) => ({
      innerIndex: ix.innerIndex,
      programId: ix.programId,
      instruction: ix.instruction,
    })),
  };
}

const watched = new Set(['addr1']);
const accountKeyMap = new Map<number, string>();

describe('PluginDispatcher', () => {
  it('prevents duplicate events when protocol plugin consumes inner CPI instructions', () => {
    // Mock Marinade staking plugin: high priority, claims ownership of Marinade program
    const marinadePlugin: EventPlugin = {
      id: 'marinade_staking',
      priority: 50,
      programIds: new Set([MARINADE_PROGRAM]),
      parse(_outer, _watchedSet) {
        // Marinade plugin analyzes the outer instruction + inner CPI,
        // returns a single staking event covering the inner SPL transfer
        return [{
          outerInstructionIndex: _outer.outerIndex,
          innerInstructionIndex: -1,
          eventCategory: 'STAKE',
          eventAction: 'marinade_stake',
          programId: MARINADE_PROGRAM,
          address: 'addr1',
          contractAddress: 'mSOL_mint',
          delta: '1000',
          counterpartyAddress: '',
          tokenSymbol: 'mSOL',
          tokenName: 'Marinade Staked SOL',
          tokenDecimals: 9,
          tokenType: 'FUNGIBLE',
          metadata: {},
        }];
      },
    };

    // Generic SPL Token plugin (fallback)
    const splPlugin: EventPlugin = {
      id: 'generic_spl',
      priority: 10,
      programIds: new Set([SPL_TOKEN_PROGRAM]),
      parse(_outer, _watchedSet) {
        return [{
          outerInstructionIndex: _outer.outerIndex,
          innerInstructionIndex: -1,
          eventCategory: 'TRANSFER',
          eventAction: 'spl_transfer',
          programId: SPL_TOKEN_PROGRAM,
          address: 'addr1',
          contractAddress: 'some_mint',
          delta: '-1000',
          counterpartyAddress: 'addr2',
          tokenSymbol: '',
          tokenName: '',
          tokenDecimals: 6,
          tokenType: 'FUNGIBLE',
          metadata: {},
        }];
      },
    };

    const dispatcher = new PluginDispatcher([marinadePlugin, splPlugin]);

    // Outer: Marinade program, Inner: SPL Token CPI transfer
    const outerInstructions: ParsedOuterInstruction[] = [
      makeOuter(0, MARINADE_PROGRAM, { parsed: null }, [
        {
          innerIndex: 0,
          programId: SPL_TOKEN_PROGRAM,
          instruction: {
            parsed: { type: 'transfer', info: { source: 'ata1', destination: 'ata2', amount: '1000', authority: 'addr1' } },
          },
        },
      ]),
    ];

    const events = dispatcher.dispatch(outerInstructions, watched, accountKeyMap, {});

    // Key assertion: Only 1 event from Marinade plugin, NOT 2 (no duplicate from inner SPL)
    expect(events).toHaveLength(1);
    expect(events[0].eventCategory).toBe('STAKE');
    expect(events[0].eventAction).toBe('marinade_stake');
  });

  it('higher priority plugin wins over lower priority for same programId', () => {
    const lowPriority: EventPlugin = {
      id: 'low',
      priority: 10,
      programIds: new Set([SYSTEM_PROGRAM]),
      parse(_outer, _watchedSet) {
        return [{
          outerInstructionIndex: 0,
          innerInstructionIndex: -1,
          eventCategory: 'TRANSFER',
          eventAction: 'low_priority',
          programId: SYSTEM_PROGRAM,
          address: 'addr1',
          contractAddress: SYSTEM_PROGRAM,
          delta: '-100',
          counterpartyAddress: 'addr2',
          tokenSymbol: 'SOL',
          tokenName: 'Solana',
          tokenDecimals: 9,
          tokenType: 'NATIVE',
          metadata: {},
        }];
      },
    };

    const highPriority: EventPlugin = {
      id: 'high',
      priority: 50,
      programIds: new Set([SYSTEM_PROGRAM]),
      parse(_outer, _watchedSet) {
        return [{
          outerInstructionIndex: 0,
          innerInstructionIndex: -1,
          eventCategory: 'TRANSFER',
          eventAction: 'high_priority',
          programId: SYSTEM_PROGRAM,
          address: 'addr1',
          contractAddress: SYSTEM_PROGRAM,
          delta: '-100',
          counterpartyAddress: 'addr2',
          tokenSymbol: 'SOL',
          tokenName: 'Solana',
          tokenDecimals: 9,
          tokenType: 'NATIVE',
          metadata: {},
        }];
      },
    };

    const dispatcher = new PluginDispatcher([lowPriority, highPriority]);

    const outerInstructions: ParsedOuterInstruction[] = [
      makeOuter(0, SYSTEM_PROGRAM, {
        parsed: { type: 'transfer', info: { source: 'addr1', destination: 'addr2', lamports: 100 } },
      }),
    ];

    const events = dispatcher.dispatch(outerInstructions, watched, accountKeyMap, {});

    expect(events).toHaveLength(1);
    expect(events[0].eventAction).toBe('high_priority');
  });

  it('falls back to inner instruction processing when no plugin matches outer', () => {
    const systemPlugin: EventPlugin = {
      id: 'generic_system',
      priority: 10,
      programIds: new Set([SYSTEM_PROGRAM]),
      parse(outer, _watchedSet) {
        const parsed = outer.instruction.parsed;
        if (!parsed || parsed.type !== 'transfer') return null;
        return [{
          outerInstructionIndex: outer.outerIndex,
          innerInstructionIndex: -1,
          eventCategory: 'TRANSFER',
          eventAction: 'system_transfer',
          programId: SYSTEM_PROGRAM,
          address: 'addr1',
          contractAddress: SYSTEM_PROGRAM,
          delta: '-500',
          counterpartyAddress: 'addr2',
          tokenSymbol: 'SOL',
          tokenName: 'Solana',
          tokenDecimals: 9,
          tokenType: 'NATIVE',
          metadata: {},
        }];
      },
    };

    const dispatcher = new PluginDispatcher([systemPlugin]);

    // Outer: unknown program, Inner: system transfer
    const outerInstructions: ParsedOuterInstruction[] = [
      makeOuter(0, 'UnknownProgram11111111111111111', { parsed: null }, [
        {
          innerIndex: 0,
          programId: SYSTEM_PROGRAM,
          instruction: {
            parsed: { type: 'transfer', info: { source: 'addr1', destination: 'addr2', lamports: 500 } },
          },
        },
      ]),
    ];

    const events = dispatcher.dispatch(outerInstructions, watched, accountKeyMap, {});

    expect(events).toHaveLength(1);
    expect(events[0].eventAction).toBe('system_transfer');
    expect(events[0].outerInstructionIndex).toBe(0);
    expect(events[0].innerInstructionIndex).toBe(0);
  });

  it('plugin returning null passes control to next plugin', () => {
    const rejectingPlugin: EventPlugin = {
      id: 'rejector',
      priority: 50,
      programIds: new Set([SYSTEM_PROGRAM]),
      parse() {
        return null; // "not my responsibility"
      },
    };

    const acceptingPlugin: EventPlugin = {
      id: 'acceptor',
      priority: 10,
      programIds: new Set([SYSTEM_PROGRAM]),
      parse(outer) {
        return [{
          outerInstructionIndex: outer.outerIndex,
          innerInstructionIndex: -1,
          eventCategory: 'TRANSFER',
          eventAction: 'accepted',
          programId: SYSTEM_PROGRAM,
          address: 'addr1',
          contractAddress: SYSTEM_PROGRAM,
          delta: '-100',
          counterpartyAddress: 'addr2',
          tokenSymbol: 'SOL',
          tokenName: 'Solana',
          tokenDecimals: 9,
          tokenType: 'NATIVE',
          metadata: {},
        }];
      },
    };

    const dispatcher = new PluginDispatcher([rejectingPlugin, acceptingPlugin]);

    const outerInstructions: ParsedOuterInstruction[] = [
      makeOuter(0, SYSTEM_PROGRAM, {
        parsed: { type: 'transfer', info: { source: 'addr1', destination: 'addr2', lamports: 100 } },
      }),
    ];

    const events = dispatcher.dispatch(outerInstructions, watched, accountKeyMap, {});

    expect(events).toHaveLength(1);
    expect(events[0].eventAction).toBe('accepted');
  });

  it('plugin returning empty array claims ownership with no events', () => {
    const claimingPlugin: EventPlugin = {
      id: 'claimer',
      priority: 50,
      programIds: new Set([MARINADE_PROGRAM]),
      parse() {
        return []; // "I own this, but no events to emit"
      },
    };

    const splPlugin: EventPlugin = {
      id: 'generic_spl',
      priority: 10,
      programIds: new Set([SPL_TOKEN_PROGRAM]),
      parse() {
        return [{
          outerInstructionIndex: 0,
          innerInstructionIndex: -1,
          eventCategory: 'TRANSFER',
          eventAction: 'should_not_appear',
          programId: SPL_TOKEN_PROGRAM,
          address: 'addr1',
          contractAddress: 'mint',
          delta: '-500',
          counterpartyAddress: 'addr2',
          tokenSymbol: '',
          tokenName: '',
          tokenDecimals: 6,
          tokenType: 'FUNGIBLE',
          metadata: {},
        }];
      },
    };

    const dispatcher = new PluginDispatcher([claimingPlugin, splPlugin]);

    const outerInstructions: ParsedOuterInstruction[] = [
      makeOuter(0, MARINADE_PROGRAM, { parsed: null }, [
        {
          innerIndex: 0,
          programId: SPL_TOKEN_PROGRAM,
          instruction: { parsed: { type: 'transfer', info: { source: 'ata1', destination: 'ata2', amount: '500', authority: 'addr1' } } },
        },
      ]),
    ];

    const events = dispatcher.dispatch(outerInstructions, watched, accountKeyMap, {});

    // Empty array = claimed, inner instructions consumed, no duplicates
    expect(events).toHaveLength(0);
  });
});

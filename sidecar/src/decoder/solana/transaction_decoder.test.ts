import { describe, it, expect } from 'vitest';
import { decodeSolanaTransaction } from './transaction_decoder';
import {
  WATCHED_ADDRESS,
  OTHER_ADDRESS,
  solTransferTx,
  splTransferCheckedTx,
  failedTx,
  innerInstructionsTx,
  createAccountTx,
  noTransferTx,
  stringAccountKeysTx,
} from '../../__fixtures__/solana_transactions';

describe('decodeSolanaTransaction', () => {
  const watchedSet = new Set([WATCHED_ADDRESS]);

  it('decodes a SOL transfer with signed delta events', () => {
    const result = decodeSolanaTransaction(solTransferTx, 'sig1', watchedSet);

    expect(result.txHash).toBe('sig1');
    expect(result.blockCursor).toBe(100);
    expect(result.blockTime).toBe(1700000000);
    expect(result.feeAmount).toBe('5000');
    expect(result.feePayer).toBe(WATCHED_ADDRESS);
    expect(result.status).toBe('SUCCESS');
    expect(result.error).toBeUndefined();

    // Should have: 1 transfer event (sender) + 1 fee event
    const transferEvents = result.balanceEvents.filter((e) => e.eventCategory === 'TRANSFER');
    const feeEvents = result.balanceEvents.filter((e) => e.eventCategory === 'FEE');

    expect(transferEvents).toHaveLength(1);
    expect(feeEvents).toHaveLength(1);

    const transferEvent = transferEvents[0];
    expect(transferEvent.address).toBe(WATCHED_ADDRESS);
    expect(transferEvent.counterpartyAddress).toBe(OTHER_ADDRESS);
    expect(transferEvent.delta).toBe('-1000000000');
    expect(transferEvent.contractAddress).toBe('11111111111111111111111111111111');
    expect(transferEvent.tokenType).toBe('NATIVE');
    expect(transferEvent.eventAction).toBe('system_transfer');

    const feeEvent = feeEvents[0];
    expect(feeEvent.address).toBe(WATCHED_ADDRESS);
    expect(feeEvent.delta).toBe('-5000');
    expect(feeEvent.eventCategory).toBe('FEE');
    expect(feeEvent.outerInstructionIndex).toBe(-1);
  });

  it('returns FAILED status with error for failed tx', () => {
    const result = decodeSolanaTransaction(failedTx, 'sigFail', watchedSet);

    expect(result.status).toBe('FAILED');
    expect(result.error).toBeDefined();
    expect(result.balanceEvents).toHaveLength(0);
    expect(result.feeAmount).toBe('5000');
    expect(result.feePayer).toBe(WATCHED_ADDRESS);
  });

  it('extracts fee payer from object format account keys', () => {
    const result = decodeSolanaTransaction(solTransferTx, 'sig1', watchedSet);
    expect(result.feePayer).toBe(WATCHED_ADDRESS);
  });

  it('extracts fee payer from string format account keys', () => {
    const result = decodeSolanaTransaction(stringAccountKeysTx, 'sig2', watchedSet);
    expect(result.feePayer).toBe(WATCHED_ADDRESS);
  });

  it('handles blockTime of 0', () => {
    const txWithZeroBlockTime = {
      ...solTransferTx,
      blockTime: 0,
    };
    const result = decodeSolanaTransaction(txWithZeroBlockTime, 'sig3', watchedSet);
    expect(result.blockTime).toBe(0);
  });

  it('handles undefined blockTime', () => {
    const txWithNoBlockTime = {
      ...solTransferTx,
      blockTime: undefined,
    };
    const result = decodeSolanaTransaction(txWithNoBlockTime, 'sig4', watchedSet);
    expect(result.blockTime).toBe(0);
  });

  it('handles missing meta', () => {
    const txNoMeta = {
      slot: 100,
      transaction: {
        message: {
          accountKeys: [{ pubkey: WATCHED_ADDRESS }],
          instructions: [],
        },
      },
    };
    const result = decodeSolanaTransaction(txNoMeta, 'sig5', watchedSet);
    expect(result.status).toBe('SUCCESS');
    expect(result.feeAmount).toBe('0');
    expect(result.balanceEvents).toHaveLength(0);
  });

  it('filters events by watched addresses', () => {
    const nonWatchedSet = new Set(['differentAddr']);
    const result = decodeSolanaTransaction(solTransferTx, 'sig6', nonWatchedSet);
    // No events since neither from/to is watched
    expect(result.balanceEvents).toHaveLength(0);
  });

  it('includes inner instructions via fallback', () => {
    const result = decodeSolanaTransaction(innerInstructionsTx, 'sig7', watchedSet);
    // Inner system transfer + fee event
    const transferEvents = result.balanceEvents.filter((e) => e.eventCategory === 'TRANSFER');
    expect(transferEvents).toHaveLength(1);
    expect(transferEvents[0].address).toBe(WATCHED_ADDRESS);
    expect(transferEvents[0].delta).toBe('-500000000');
    expect(transferEvents[0].eventAction).toBe('system_transfer');
  });

  it('assigns correct instruction indices for inner instructions', () => {
    const result = decodeSolanaTransaction(innerInstructionsTx, 'sig8', watchedSet);
    const transferEvents = result.balanceEvents.filter((e) => e.eventCategory === 'TRANSFER');
    expect(transferEvents[0].outerInstructionIndex).toBe(0);
    expect(transferEvents[0].innerInstructionIndex).toBe(0);
  });

  it('handles createAccount instruction', () => {
    const result = decodeSolanaTransaction(createAccountTx, 'sig9', watchedSet);
    const transferEvents = result.balanceEvents.filter((e) => e.eventCategory === 'TRANSFER');
    expect(transferEvents).toHaveLength(1);
    expect(transferEvents[0].eventAction).toBe('system_create_account');
    expect(transferEvents[0].address).toBe(WATCHED_ADDRESS);
    expect(transferEvents[0].counterpartyAddress).toBe('newAccount111');
    expect(transferEvents[0].delta).toBe('-2039280');
  });

  it('returns no events for tx with no transfer instructions', () => {
    const result = decodeSolanaTransaction(noTransferTx, 'sig10', watchedSet);
    // Only a fee event (fee payer is watched)
    const transferEvents = result.balanceEvents.filter((e) => e.eventCategory === 'TRANSFER');
    expect(transferEvents).toHaveLength(0);
    expect(result.status).toBe('SUCCESS');
  });

  it('parses SPL transferChecked as signed delta events', () => {
    const result = decodeSolanaTransaction(splTransferCheckedTx, 'sig11', watchedSet);
    const transferEvents = result.balanceEvents.filter((e) => e.eventCategory === 'TRANSFER');
    expect(transferEvents).toHaveLength(1);
    expect(transferEvents[0].eventAction).toBe('spl_transfer_checked');
    expect(transferEvents[0].delta).toBe('-1000000');
    expect(transferEvents[0].address).toBe(WATCHED_ADDRESS);
    expect(transferEvents[0].contractAddress).toBe('EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v');
  });

  it('handles undefined innerInstructions (not empty array)', () => {
    const txNoInner = {
      ...solTransferTx,
      meta: {
        ...solTransferTx.meta,
        innerInstructions: undefined,
      },
    };
    const result = decodeSolanaTransaction(txNoInner, 'sigNoInner', watchedSet);
    expect(result.status).toBe('SUCCESS');
    // Should still detect the outer transfer
    const transferEvents = result.balanceEvents.filter((e) => e.eventCategory === 'TRANSFER');
    expect(transferEvents).toHaveLength(1);
  });

  it('handles programIdIndex as instruction reference', () => {
    const txWithProgramIdIndex = {
      slot: 900,
      blockTime: 1700008000,
      transaction: {
        message: {
          accountKeys: [
            { pubkey: WATCHED_ADDRESS },
            { pubkey: OTHER_ADDRESS },
            { pubkey: '11111111111111111111111111111111' },
          ],
          instructions: [
            {
              programIdIndex: 2,
              parsed: {
                type: 'transfer',
                info: {
                  source: WATCHED_ADDRESS,
                  destination: OTHER_ADDRESS,
                  lamports: 100000,
                },
              },
            },
          ],
        },
      },
      meta: {
        err: null,
        fee: 5000,
        preBalances: [2000000000, 500000000, 1],
        postBalances: [1999895000, 500100000, 1],
        innerInstructions: [],
      },
    };
    const result = decodeSolanaTransaction(txWithProgramIdIndex, 'sigPII', watchedSet);
    expect(result.status).toBe('SUCCESS');
    expect(result.feeAmount).toBe('5000');
  });

  it('handles deeply nested CPI (3 levels)', () => {
    const deepCPITx = {
      slot: 1000,
      blockTime: 1700009000,
      transaction: {
        message: {
          accountKeys: [
            { pubkey: WATCHED_ADDRESS },
            { pubkey: OTHER_ADDRESS },
            { pubkey: '11111111111111111111111111111111' },
          ],
          instructions: [
            {
              programId: 'OuterProgram1111111111111111111111111',
              parsed: null,
            },
          ],
        },
      },
      meta: {
        err: null,
        fee: 5000,
        preBalances: [2000000000, 500000000, 1],
        postBalances: [1499995000, 1000000000, 1],
        innerInstructions: [
          {
            index: 0,
            instructions: [
              {
                programId: 'MiddleProgram111111111111111111111111',
                parsed: null,
              },
              {
                programId: '11111111111111111111111111111111',
                parsed: {
                  type: 'transfer',
                  info: {
                    source: WATCHED_ADDRESS,
                    destination: OTHER_ADDRESS,
                    lamports: 500000000,
                  },
                },
              },
            ],
          },
        ],
      },
    };
    const result = decodeSolanaTransaction(deepCPITx, 'sigDeepCPI', watchedSet);
    const transferEvents = result.balanceEvents.filter((e) => e.eventCategory === 'TRANSFER');
    expect(transferEvents).toHaveLength(1);
    expect(transferEvents[0].delta).toBe('-500000000');
    expect(transferEvents[0].outerInstructionIndex).toBe(0);
    expect(transferEvents[0].innerInstructionIndex).toBe(1);
  });
});

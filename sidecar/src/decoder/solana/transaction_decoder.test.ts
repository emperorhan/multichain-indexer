import { describe, it, expect } from 'vitest';
import { decodeSolanaTransaction } from './transaction_decoder';
import { decodeSolanaTransactionBatch } from '../index';
import {
  WATCHED_ADDRESS,
  OTHER_ADDRESS,
  USDC_MINT,
  solTransferTx,
  splTransferCheckedTx,
  splTransferTx,
  splMintTx,
  splBurnTx,
  failedTx,
  innerInstructionsTx,
  createAccountTx,
  transferWithSeedTx,
  createAccountWithSeedTx,
  withdrawNonceAccountTx,
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

  it('parses system transferWithSeed instruction as deterministic signed delta', () => {
    const result = decodeSolanaTransaction(transferWithSeedTx, 'sig16', watchedSet);
    const transferEvents = result.balanceEvents.filter((e) => e.eventCategory === 'TRANSFER');

    expect(transferEvents).toHaveLength(1);
    expect(transferEvents[0].eventAction).toBe('system_transfer_with_seed');
    expect(transferEvents[0].address).toBe(WATCHED_ADDRESS);
    expect(transferEvents[0].delta).toBe('-222');
    expect(transferEvents[0].counterpartyAddress).toBe(OTHER_ADDRESS);
  });

  it('parses system createAccountWithSeed instruction as deterministic signed delta', () => {
    const result = decodeSolanaTransaction(createAccountWithSeedTx, 'sig17', watchedSet);
    const transferEvents = result.balanceEvents.filter((e) => e.eventCategory === 'TRANSFER');

    expect(transferEvents).toHaveLength(1);
    expect(transferEvents[0].eventAction).toBe('system_create_account_with_seed');
    expect(transferEvents[0].address).toBe(WATCHED_ADDRESS);
    expect(transferEvents[0].delta).toBe('-2039280');
  });

  it('parses system withdrawNonceAccount instruction as deterministic signed delta', () => {
    const result = decodeSolanaTransaction(withdrawNonceAccountTx, 'sig18', watchedSet);
    const transferEvents = result.balanceEvents.filter((e) => e.eventCategory === 'TRANSFER');

    expect(transferEvents).toHaveLength(1);
    expect(transferEvents[0].eventAction).toBe('system_withdraw_nonce');
    expect(transferEvents[0].address).toBe(WATCHED_ADDRESS);
    expect(transferEvents[0].delta).toBe('-120000');
    expect(transferEvents[0].counterpartyAddress).toBe('withdrawRecipient111');
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

  it('parses SPL transfer as signed delta events', () => {
    const result = decodeSolanaTransaction(splTransferTx, 'sig12', watchedSet);
    const transferEvents = result.balanceEvents.filter((e) => e.eventCategory === 'TRANSFER');
    expect(transferEvents).toHaveLength(1);
    expect(transferEvents[0].eventAction).toBe('spl_transfer');
    expect(transferEvents[0].address).toBe(WATCHED_ADDRESS);
  });

  it('parses SPL mint instruction as MINT class', () => {
    const result = decodeSolanaTransaction(splMintTx, 'sig13', watchedSet);
    const mintEvents = result.balanceEvents.filter((e) => e.eventCategory === 'MINT');
    expect(mintEvents).toHaveLength(1);

    const mintEvent = mintEvents[0];
    expect(mintEvent.eventAction).toBe('spl_mint');
    expect(mintEvent.address).toBe(WATCHED_ADDRESS);
    expect(mintEvent.contractAddress).toBe(USDC_MINT);
    expect(mintEvent.delta).toBe('2500000');
  });

  it('parses SPL burn instruction as BURN class', () => {
    const result = decodeSolanaTransaction(splBurnTx, 'sig14', watchedSet);
    const burnEvents = result.balanceEvents.filter((e) => e.eventCategory === 'BURN');
    expect(burnEvents).toHaveLength(1);

    const burnEvent = burnEvents[0];
    expect(burnEvent.eventAction).toBe('spl_burn');
    expect(burnEvent.address).toBe(WATCHED_ADDRESS);
    expect(burnEvent.contractAddress).toBe(USDC_MINT);
    expect(burnEvent.delta).toBe('-1250000');
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

  it('is replay-invariant for system instruction permutations', () => {
    const watchedAddresses = [WATCHED_ADDRESS];
    const standardOrder = decodeSolanaTransactionBatch(
      [
        { signature: 'sig16', rawJson: Buffer.from(JSON.stringify(transferWithSeedTx)) },
        { signature: 'sig17', rawJson: Buffer.from(JSON.stringify(createAccountWithSeedTx)) },
        { signature: 'sig18', rawJson: Buffer.from(JSON.stringify(withdrawNonceAccountTx)) },
      ],
      watchedAddresses,
    );
    const perturbedOrder = decodeSolanaTransactionBatch(
      [
        { signature: 'sig17', rawJson: Buffer.from(JSON.stringify(createAccountWithSeedTx)) },
        { signature: 'sig16', rawJson: Buffer.from(JSON.stringify(transferWithSeedTx)) },
        { signature: 'sig18', rawJson: Buffer.from(JSON.stringify(withdrawNonceAccountTx)) },
      ],
      watchedAddresses,
    );

    expect(standardOrder.errors).toHaveLength(0);
    expect(perturbedOrder.errors).toHaveLength(0);

    const standardFingerprint = canonicalBalanceEventsFingerprint(standardOrder.results);
    const perturbedFingerprint = canonicalBalanceEventsFingerprint(perturbedOrder.results);
    expect(standardFingerprint).toEqual(perturbedFingerprint);
  });
});

function canonicalBalanceEventsFingerprint(
  results: Array<{ txHash: string; balanceEvents: Array<{ eventCategory: string; eventAction: string; outerInstructionIndex: number; innerInstructionIndex: number; address: string; contractAddress: string; delta: string; tokenType: string }> }>,
): string[] {
  return results.flatMap((result) =>
    result.balanceEvents.map((event) => [
      result.txHash,
      event.eventCategory,
      event.eventAction,
      String(event.outerInstructionIndex),
      String(event.innerInstructionIndex),
      event.address,
      event.contractAddress,
      event.delta,
      event.tokenType,
    ].join('|')),
  ).sort();
}

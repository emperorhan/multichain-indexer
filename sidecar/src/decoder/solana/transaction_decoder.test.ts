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

  it('decodes a SOL transfer successfully', () => {
    const result = decodeSolanaTransaction(solTransferTx, 'sig1', watchedSet);

    expect(result.txHash).toBe('sig1');
    expect(result.blockCursor).toBe(100);
    expect(result.blockTime).toBe(1700000000);
    expect(result.feeAmount).toBe('5000');
    expect(result.feePayer).toBe(WATCHED_ADDRESS);
    expect(result.status).toBe('SUCCESS');
    expect(result.error).toBeUndefined();
    expect(result.transfers).toHaveLength(1);

    const transfer = result.transfers[0];
    expect(transfer.fromAddress).toBe(WATCHED_ADDRESS);
    expect(transfer.toAddress).toBe(OTHER_ADDRESS);
    expect(transfer.amount).toBe('1000000000');
    expect(transfer.contractAddress).toBe('11111111111111111111111111111111');
    expect(transfer.tokenType).toBe('NATIVE');
    expect(transfer.tokenSymbol).toBe('SOL');
    expect(transfer.transferType).toBe('systemTransfer');
  });

  it('returns FAILED status with error for failed tx', () => {
    const result = decodeSolanaTransaction(failedTx, 'sigFail', watchedSet);

    expect(result.status).toBe('FAILED');
    expect(result.error).toBeDefined();
    expect(result.transfers).toHaveLength(0);
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
    expect(result.transfers).toHaveLength(0);
  });

  it('filters transfers by watched addresses', () => {
    const nonWatchedSet = new Set(['differentAddr']);
    const result = decodeSolanaTransaction(solTransferTx, 'sig6', nonWatchedSet);
    // Transfer involves WATCHED_ADDRESS, not 'differentAddr'
    expect(result.transfers).toHaveLength(0);
  });

  it('includes inner instructions', () => {
    const result = decodeSolanaTransaction(innerInstructionsTx, 'sig7', watchedSet);
    expect(result.transfers).toHaveLength(1);
    expect(result.transfers[0].fromAddress).toBe(WATCHED_ADDRESS);
    expect(result.transfers[0].amount).toBe('500000000');
    expect(result.transfers[0].transferType).toBe('systemTransfer');
  });

  it('assigns correct instruction index order', () => {
    const result = decodeSolanaTransaction(innerInstructionsTx, 'sig8', watchedSet);
    // Outer instruction at index 0 (unparsed), inner at index 1
    expect(result.transfers[0].instructionIndex).toBe(1);
  });

  it('handles createAccount instruction', () => {
    const result = decodeSolanaTransaction(createAccountTx, 'sig9', watchedSet);
    expect(result.transfers).toHaveLength(1);
    expect(result.transfers[0].transferType).toBe('createAccount');
    expect(result.transfers[0].fromAddress).toBe(WATCHED_ADDRESS);
    expect(result.transfers[0].toAddress).toBe('newAccount111');
    expect(result.transfers[0].amount).toBe('2039280');
  });

  it('returns no transfers for tx with no transfer instructions', () => {
    const result = decodeSolanaTransaction(noTransferTx, 'sig10', watchedSet);
    expect(result.transfers).toHaveLength(0);
    expect(result.status).toBe('SUCCESS');
  });

  it('parses SPL transferChecked', () => {
    const result = decodeSolanaTransaction(splTransferCheckedTx, 'sig11', watchedSet);
    expect(result.transfers).toHaveLength(1);
    expect(result.transfers[0].transferType).toBe('transferChecked');
    expect(result.transfers[0].amount).toBe('1000000');
    expect(result.transfers[0].fromAddress).toBe(WATCHED_ADDRESS);
    expect(result.transfers[0].contractAddress).toBe('EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v');
  });
});

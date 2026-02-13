import { describe, it, expect } from 'vitest';
import { decodeSolanaTransactionBatch } from './index';
import {
  WATCHED_ADDRESS,
  solTransferTx,
  failedTx,
} from '../__fixtures__/solana_transactions';

describe('decodeSolanaTransactionBatch', () => {
  it('decodes multiple transactions', () => {
    const transactions = [
      {
        signature: 'sig1',
        rawJson: Buffer.from(JSON.stringify(solTransferTx)),
      },
      {
        signature: 'sig2',
        rawJson: Buffer.from(JSON.stringify(failedTx)),
      },
    ];

    const result = decodeSolanaTransactionBatch(transactions, [WATCHED_ADDRESS]);

    expect(result.results).toHaveLength(2);
    expect(result.errors).toHaveLength(0);
    expect(result.results[0].txHash).toBe('sig1');
    expect(result.results[0].status).toBe('SUCCESS');
    expect(result.results[1].txHash).toBe('sig2');
    expect(result.results[1].status).toBe('FAILED');
  });

  it('collects invalid JSON in errors array', () => {
    const transactions = [
      {
        signature: 'goodSig',
        rawJson: Buffer.from(JSON.stringify(solTransferTx)),
      },
      {
        signature: 'badSig',
        rawJson: Buffer.from('not valid json'),
      },
    ];

    const result = decodeSolanaTransactionBatch(transactions, [WATCHED_ADDRESS]);

    expect(result.results).toHaveLength(1);
    expect(result.results[0].txHash).toBe('goodSig');
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].signature).toBe('badSig');
    expect(result.errors[0].error).toBeDefined();
  });

  it('handles null parsed transaction as error', () => {
    const transactions = [
      {
        signature: 'nullSig',
        rawJson: Buffer.from('null'),
      },
    ];

    const result = decodeSolanaTransactionBatch(transactions, [WATCHED_ADDRESS]);

    expect(result.results).toHaveLength(0);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].signature).toBe('nullSig');
    expect(result.errors[0].error).toBe('null transaction response');
  });

  it('handles string rawJson', () => {
    const transactions = [
      {
        signature: 'stringSig',
        rawJson: JSON.stringify(solTransferTx) as any,
      },
    ];

    const result = decodeSolanaTransactionBatch(transactions, [WATCHED_ADDRESS]);

    expect(result.results).toHaveLength(1);
    expect(result.errors).toHaveLength(0);
    expect(result.results[0].txHash).toBe('stringSig');
  });

  it('handles Buffer rawJson', () => {
    const transactions = [
      {
        signature: 'bufSig',
        rawJson: Buffer.from(JSON.stringify(solTransferTx)),
      },
    ];

    const result = decodeSolanaTransactionBatch(transactions, [WATCHED_ADDRESS]);

    expect(result.results).toHaveLength(1);
    expect(result.results[0].txHash).toBe('bufSig');
  });

  it('returns empty results for empty batch', () => {
    const result = decodeSolanaTransactionBatch([], [WATCHED_ADDRESS]);

    expect(result.results).toHaveLength(0);
    expect(result.errors).toHaveLength(0);
  });
});

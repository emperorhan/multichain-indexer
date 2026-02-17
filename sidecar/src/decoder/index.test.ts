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
    expect(result.results[0].balanceEvents.length).toBeGreaterThan(0);
    expect(result.results[1].txHash).toBe('sig2');
    expect(result.results[1].status).toBe('FAILED');
    expect(result.results[1].balanceEvents).toHaveLength(0);
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

  it('routes base payloads to base decoder', () => {
    const baseWatched = '0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
    const transactions = [
      {
        signature: 'baseSig',
        rawJson: Buffer.from(JSON.stringify({
          chain: 'base',
          tx: {
            hash: '0xbaseTx',
            blockNumber: '0x10',
            transactionIndex: '0x0',
            from: baseWatched,
            to: '0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
            value: '0x1',
            gasPrice: '0x1',
          },
          receipt: {
            transactionHash: '0xbaseTx',
            blockNumber: '0x10',
            transactionIndex: '0x0',
            status: '0x1',
            from: baseWatched,
            gasUsed: '0x2',
            effectiveGasPrice: '0x3',
            logs: [],
          },
        })),
      },
    ];

    const result = decodeSolanaTransactionBatch(transactions, [baseWatched]);
    expect(result.errors).toHaveLength(0);
    expect(result.results).toHaveLength(1);
    expect(result.results[0].txHash).toBe('0xbaseTx');
    expect(result.results[0].metadata?.fee_execution_l2).toBe('6');
  });

  it('routes btc payloads to btc decoder', () => {
    const watched = 'tb1watched';
    const transactions = [
      {
        signature: 'btcSig',
        rawJson: Buffer.from(JSON.stringify({
          chain: 'btc',
          txid: 'btcTx',
          block_height: 7,
          block_time: 1700000007,
          fee_sat: '25',
          fee_payer: watched,
          vin: [
            { index: 0, address: watched, value_sat: '1000', txid: 'prev', vout: 0 },
          ],
          vout: [
            { index: 0, address: watched, value_sat: '200' },
            { index: 1, address: 'tb1external', value_sat: '775' },
          ],
        })),
      },
    ];

    const result = decodeSolanaTransactionBatch(transactions, [watched]);
    expect(result.errors).toHaveLength(0);
    expect(result.results).toHaveLength(1);
    expect(result.results[0].txHash).toBe('btcTx');
    expect(result.results[0].feeAmount).toBe('25');
    expect(result.results[0].balanceEvents.find((ev) => ev.eventAction === 'vin_spend')).toBeDefined();
    expect(result.results[0].balanceEvents.find((ev) => ev.eventAction === 'vout_receive')).toBeDefined();
  });

  it('routes ambiguous payload with tx+receipt to base decoder', () => {
    const baseWatched = '0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
    const transactions = [
      {
        signature: 'ambiguousSig',
        rawJson: Buffer.from(JSON.stringify({
          tx: {
            hash: '0xambiguous',
            blockNumber: '0x10',
            from: baseWatched,
            to: '0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
            value: '0x1',
          },
          receipt: {
            transactionHash: '0xambiguous',
            status: '0x1',
            gasUsed: '0x1',
            effectiveGasPrice: '0x1',
            logs: [],
          },
        })),
      },
    ];

    const result = decodeSolanaTransactionBatch(transactions, [baseWatched]);
    expect(result.errors).toHaveLength(0);
    expect(result.results).toHaveLength(1);
    // Should be decoded by base decoder since it has tx+receipt
    expect(result.results[0].txHash).toBe('0xambiguous');
  });

  it('handles empty watched addresses', () => {
    const transactions = [
      {
        signature: 'noWatchSig',
        rawJson: Buffer.from(JSON.stringify(solTransferTx)),
      },
    ];

    const result = decodeSolanaTransactionBatch(transactions, []);
    expect(result.errors).toHaveLength(0);
    expect(result.results).toHaveLength(1);
    // With no watched addresses, no balance events should be generated
    expect(result.results[0].balanceEvents).toHaveLength(0);
  });
});

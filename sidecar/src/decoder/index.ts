import { decodeSolanaTransaction, BalanceEventInfo, TransactionResult } from './solana/transaction_decoder';
import { decodeBaseTransaction } from './base/transaction_decoder';
import { decodeBTCTransaction } from './btc/transaction_decoder';

interface RawTxInput {
  signature: string;
  rawJson: Buffer;
}

interface DecodeError {
  signature: string;
  error: string;
}

interface DecodeBatchResponse {
  results: TransactionResult[];
  errors: DecodeError[];
}

export type { BalanceEventInfo, TransactionResult };

export function decodeSolanaTransactionBatch(
  transactions: RawTxInput[],
  watchedAddresses: string[]
): DecodeBatchResponse {
  const results: TransactionResult[] = [];
  const errors: DecodeError[] = [];
  const watchedSet = new Set(watchedAddresses);

  for (const tx of transactions) {
    try {
      const rawJson = typeof tx.rawJson === 'string'
        ? tx.rawJson
        : Buffer.isBuffer(tx.rawJson)
          ? tx.rawJson.toString('utf-8')
          : String(tx.rawJson);

      const parsed = JSON.parse(rawJson);
      if (!parsed) {
        errors.push({ signature: tx.signature, error: 'null transaction response' });
        continue;
      }

      const result = isBasePayload(parsed)
        ? decodeBaseTransaction(parsed, tx.signature, watchedSet)
        : isBTCPayload(parsed)
          ? decodeBTCTransaction(parsed, tx.signature, watchedSet)
        : decodeSolanaTransaction(parsed, tx.signature, watchedSet);
      results.push(result);
    } catch (err: any) {
      errors.push({
        signature: tx.signature,
        error: err.message || 'unknown decode error',
      });
    }
  }

  return { results, errors };
}

function isBasePayload(payload: unknown): boolean {
  if (!payload || typeof payload !== 'object') {
    return false;
  }
  const parsed = payload as Record<string, unknown>;
  if (parsed.chain === 'base' || parsed.chain === 'ethereum') {
    return true;
  }
  return parsed.tx !== undefined || parsed.receipt !== undefined;
}

function isBTCPayload(payload: unknown): boolean {
  if (!payload || typeof payload !== 'object') {
    return false;
  }
  const parsed = payload as Record<string, unknown>;
  if (parsed.chain === 'btc') {
    return true;
  }
  if (parsed.txid !== undefined && (parsed.vin !== undefined || parsed.vout !== undefined)) {
    return true;
  }
  return false;
}

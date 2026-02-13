import { decodeSolanaTransaction, BalanceEventInfo, TransactionResult } from './solana/transaction_decoder';

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

      const result = decodeSolanaTransaction(parsed, tx.signature, watchedSet);
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

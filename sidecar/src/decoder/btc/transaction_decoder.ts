import { BalanceEventInfo, TransactionResult } from '../solana/transaction_decoder';

const BTC_PROGRAM_ID = 'btc';
const BTC_ASSET_ID = 'BTC';
const BTC_DECIMALS = 8;

interface BTCEnvelope {
  chain?: string;
  txid?: string;
  block_height?: number | string;
  block_hash?: string;
  block_time?: number | string;
  confirmations?: number | string;
  fee_sat?: string | number;
  fee_payer?: string;
  vin?: BTCVin[];
  vout?: BTCVout[];
}

interface BTCVin {
  index?: number;
  txid?: string;
  vout?: number;
  address?: string;
  value_sat?: string | number;
  coinbase?: boolean;
}

interface BTCVout {
  index?: number;
  address?: string;
  value_sat?: string | number;
}

export function decodeBTCTransaction(
  rawPayload: unknown,
  signature: string,
  watchedAddresses: Set<string>,
): TransactionResult {
  const payload = normalizePayload(rawPayload);
  const txHash = normalizeString(payload.txid) || normalizeString(signature);
  const blockCursor = toInt(payload.block_height);
  const blockTime = toInt(payload.block_time);
  const confirmations = toInt(payload.confirmations);
  const finalityState = confirmations > 0 ? 'confirmed' : 'processed';
  const watched = new Set(Array.from(watchedAddresses).map(normalizeString).filter((v) => v.length > 0));

  let totalInputSat = 0n;
  let totalOutputSat = 0n;
  const payerCandidates: string[] = [];
  const events: BalanceEventInfo[] = [];

  for (const vin of payload.vin || []) {
    if (!vin || vin.coinbase) {
      continue;
    }
    const address = normalizeString(vin.address);
    const valueSat = toBigInt(vin.value_sat);
    const vinIndex = Number.isFinite(vin.index) ? Number(vin.index) : -1;
    totalInputSat += valueSat;
    if (address.length > 0) {
      payerCandidates.push(address);
    }
    if (!watched.has(address) || valueSat <= 0n) {
      continue;
    }

    const eventPath = `vin:${vinIndex >= 0 ? vinIndex : 0}`;
    events.push({
      outerInstructionIndex: vinIndex,
      innerInstructionIndex: -1,
      eventCategory: 'TRANSFER',
      eventAction: 'vin_spend',
      programId: BTC_PROGRAM_ID,
      address,
      contractAddress: BTC_ASSET_ID,
      delta: `-${valueSat.toString()}`,
      counterpartyAddress: '',
      tokenSymbol: BTC_ASSET_ID,
      tokenName: 'Bitcoin',
      tokenDecimals: BTC_DECIMALS,
      tokenType: 'NATIVE',
      metadata: {
        event_path: eventPath,
        utxo_path_type: 'vin',
        input_txid: normalizeString(vin.txid),
        input_vout: String(vin.vout ?? ''),
        finality_state: finalityState,
      },
    });
  }

  for (const vout of payload.vout || []) {
    if (!vout) {
      continue;
    }
    const address = normalizeString(vout.address);
    const valueSat = toBigInt(vout.value_sat);
    const voutIndex = Number.isFinite(vout.index) ? Number(vout.index) : -1;
    totalOutputSat += valueSat;
    if (!watched.has(address) || valueSat <= 0n) {
      continue;
    }

    const eventPath = `vout:${voutIndex >= 0 ? voutIndex : 0}`;
    events.push({
      outerInstructionIndex: voutIndex,
      innerInstructionIndex: -1,
      eventCategory: 'TRANSFER',
      eventAction: 'vout_receive',
      programId: BTC_PROGRAM_ID,
      address,
      contractAddress: BTC_ASSET_ID,
      delta: valueSat.toString(),
      counterpartyAddress: '',
      tokenSymbol: BTC_ASSET_ID,
      tokenName: 'Bitcoin',
      tokenDecimals: BTC_DECIMALS,
      tokenType: 'NATIVE',
      metadata: {
        event_path: eventPath,
        utxo_path_type: 'vout',
        finality_state: finalityState,
      },
    });
  }

  let feeSat = toBigInt(payload.fee_sat);
  if (feeSat <= 0n && totalInputSat > 0n && totalInputSat >= totalOutputSat) {
    feeSat = totalInputSat - totalOutputSat;
  }

  const feePayer = normalizeString(payload.fee_payer) || deterministicAddress(payerCandidates);
  const metadata: Record<string, string> = {
    finality_state: finalityState,
  };
  if (normalizeString(payload.block_hash).length > 0) {
    metadata.block_hash = normalizeString(payload.block_hash);
  }
  metadata.btc_fee_sat = feeSat.toString();
  if (feePayer.length > 0) {
    metadata.btc_fee_payer = feePayer;
  }

  return {
    txHash,
    blockCursor,
    blockTime,
    feeAmount: feeSat.toString(),
    feePayer,
    status: 'SUCCESS',
    balanceEvents: events,
    metadata,
  };
}

function normalizePayload(rawPayload: unknown): BTCEnvelope {
  if (!rawPayload || typeof rawPayload !== 'object') {
    throw new Error('invalid btc payload');
  }
  return rawPayload as BTCEnvelope;
}

function toInt(value: unknown): number {
  if (typeof value === 'number') {
    return Number.isFinite(value) ? Math.trunc(value) : 0;
  }
  if (typeof value === 'string') {
    const trimmed = value.trim();
    if (trimmed === '') {
      return 0;
    }
    const parsed = Number(trimmed);
    return Number.isFinite(parsed) ? Math.trunc(parsed) : 0;
  }
  return 0;
}

function normalizeString(value: unknown): string {
  return typeof value === 'string' ? value.trim() : '';
}

function toBigInt(value: unknown): bigint {
  if (typeof value === 'bigint') {
    return value;
  }
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) {
      return 0n;
    }
    return BigInt(Math.trunc(value));
  }
  if (typeof value !== 'string') {
    return 0n;
  }

  const trimmed = value.trim();
  if (trimmed === '') {
    return 0n;
  }
  try {
    return BigInt(trimmed);
  } catch {
    return 0n;
  }
}

function deterministicAddress(values: string[]): string {
  const uniq = Array.from(new Set(values.map((v) => normalizeString(v)).filter((v) => v.length > 0)));
  if (uniq.length === 0) {
    return '';
  }
  uniq.sort();
  return uniq[0];
}

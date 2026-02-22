import { BalanceEventInfo, TransactionResult } from '../solana/transaction_decoder';

const TRANSFER_TOPIC = '0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55aebf9f4f7b8';
const ZERO_PROGRAM_ID = '0x0000000000000000000000000000000000000000000000000000000000000000';
const ZERO_ADDRESS = '0x0000000000000000000000000000000000000000';

interface BaseEnvelope {
  chain?: string;
  evm_layer?: string;
  tx?: EvmTransaction;
  receipt?: EvmReceipt;
  blockTimestamp?: string | number;
}

interface EvmTransaction {
  hash?: string;
  blockNumber?: string;
  transactionIndex?: string;
  from?: string;
  to?: string;
  value?: string;
  gasPrice?: string;
}

interface EvmReceipt {
  transactionHash?: string;
  blockNumber?: string;
  transactionIndex?: string;
  status?: string;
  from?: string;
  to?: string;
  gasUsed?: string;
  effectiveGasPrice?: string;
  l1Fee?: string;
  l1GasUsed?: string;
  l1GasPrice?: string;
  logs?: EvmLog[];
}

interface EvmLog {
  address?: string;
  topics?: string[];
  data?: string;
  logIndex?: string;
  removed?: boolean;
}

export function decodeBaseTransaction(
  rawPayload: unknown,
  signature: string,
  watchedAddresses: Set<string>
): TransactionResult {
  const envelope = normalizeEnvelope(rawPayload);
  const tx = envelope.tx || {};
  const receipt = envelope.receipt || {};

  const txHash = tx.hash || receipt.transactionHash || signature;
  const blockCursor = hexToNumber(receipt.blockNumber || tx.blockNumber);
  const blockTime = parseTimestamp(envelope.blockTimestamp);
  const txIndex = hexToNumber(receipt.transactionIndex || tx.transactionIndex, -1);
  const feePayer = normalizeAddress(tx.from || receipt.from);

  const statusNum = toBigInt(receipt.status);
  const status = statusNum === 0n ? 'FAILED' : 'SUCCESS';
  const error = status === 'FAILED' ? 'execution reverted' : undefined;

  const gasUsed = toBigInt(receipt.gasUsed);
  const effectiveGasPrice = toBigInt(receipt.effectiveGasPrice || tx.gasPrice);
  const executionFee = gasUsed * effectiveGasPrice;
  const dataFee = toBigInt(receipt.l1Fee);
  const totalFee = executionFee + dataFee;
  const feeAmount = totalFee.toString();

  const evmLayer = envelope.evm_layer || '';
  const feeMetadata = buildFeeMetadata(executionFee, dataFee, gasUsed, effectiveGasPrice, receipt, evmLayer);
  const normalizedWatched = new Set(Array.from(watchedAddresses).map(normalizeAddress));
  const balanceEvents: BalanceEventInfo[] = [];

  // Reverted transactions: all state changes (value transfers + logs) are rolled back.
  // Only the gas fee is consumed. Return early with no balance events — the Go
  // normalizer emits fee events separately based on the FAILED status.
  if (status === 'FAILED') {
    return {
      txHash,
      blockCursor,
      blockTime,
      feeAmount,
      feePayer,
      status,
      error,
      balanceEvents: [],
      metadata: feeMetadata,
    };
  }

  const from = normalizeAddress(tx.from || receipt.from);
  const to = normalizeAddress(tx.to || receipt.to);
  const transferValue = toBigInt(tx.value);
  if (transferValue > 0n) {
    const transferPath = txIndex >= 0 ? `tx:${txIndex}` : 'tx:0';
    if (from !== '' && normalizedWatched.has(from)) {
      balanceEvents.push(buildNativeTransferEvent(from, to, transferValue, txIndex, transferPath, feeMetadata, true));
    }
    if (to !== '' && normalizedWatched.has(to)) {
      balanceEvents.push(buildNativeTransferEvent(to, from, transferValue, txIndex, transferPath, feeMetadata, false));
    }
  }

  for (const log of receipt.logs || []) {
    if (!log || log.removed) {
      continue;
    }
    const topics = log.topics || [];
    if (topics.length < 3 || normalizeTopic(topics[0]) !== TRANSFER_TOPIC) {
      continue;
    }

    const eventFrom = topicToAddress(topics[1]);
    const eventTo = topicToAddress(topics[2]);
    const value = toBigInt(log.data);
    if (value <= 0n) {
      continue;
    }

    const logIndex = hexToNumber(log.logIndex, 0);
    const eventPath = `log:${logIndex}`;
    const eventMetadata: Record<string, string> = {
      ...feeMetadata,
      event_path: eventPath,
      log_index: String(logIndex),
      base_log_index: String(logIndex),
      base_event_path: eventPath,
    };

    if (eventFrom === ZERO_ADDRESS) {
      if (eventTo !== '' && normalizedWatched.has(eventTo)) {
        balanceEvents.push(buildERC20MintEvent(
          eventTo,
          eventFrom,
          normalizeAddress(log.address),
          value,
          txIndex,
          eventMetadata,
        ));
      }
      continue;
    }

    if (eventTo === ZERO_ADDRESS) {
      if (eventFrom !== '' && normalizedWatched.has(eventFrom)) {
        balanceEvents.push(buildERC20BurnEvent(
          eventFrom,
          eventTo,
          normalizeAddress(log.address),
          value,
          txIndex,
          eventMetadata,
        ));
      }
      continue;
    }

    if (eventFrom !== '' && normalizedWatched.has(eventFrom)) {
      balanceEvents.push(buildERC20TransferEvent(
        eventFrom,
        eventTo,
        normalizeAddress(log.address),
        value,
        txIndex,
        eventMetadata,
        true
      ));
    }
    if (eventTo !== '' && normalizedWatched.has(eventTo)) {
      balanceEvents.push(buildERC20TransferEvent(
        eventTo,
        eventFrom,
        normalizeAddress(log.address),
        value,
        txIndex,
        eventMetadata,
        false
      ));
    }
  }

  return {
    txHash,
    blockCursor,
    blockTime,
    feeAmount,
    feePayer,
    status,
    error,
    balanceEvents,
    metadata: feeMetadata,
  };
}

function normalizeEnvelope(rawPayload: unknown): BaseEnvelope {
  if (!rawPayload || typeof rawPayload !== 'object') {
    throw new Error('invalid base payload');
  }
  const payload = rawPayload as BaseEnvelope;
  if (payload.tx || payload.receipt) {
    return payload;
  }
  // Support direct receipt payloads in tests and fallback clients.
  return {
    tx: payload as unknown as EvmTransaction,
    receipt: payload as unknown as EvmReceipt,
  };
}

function buildFeeMetadata(
  executionFee: bigint,
  dataFee: bigint,
  gasUsed: bigint,
  effectiveGasPrice: bigint,
  receipt: EvmReceipt,
  evmLayer: string
): Record<string, string> {
  const isL1 = evmLayer === 'l1';

  const metadata: Record<string, string> = isL1
    ? {
        gas_used: gasUsed.toString(),
        effective_gas_price: effectiveGasPrice.toString(),
      }
    : {
        fee_execution_l2: executionFee.toString(),
        base_gas_used: gasUsed.toString(),
        base_effective_gas_price: effectiveGasPrice.toString(),
      };

  if (!isL1 && dataFee > 0n) {
    metadata.fee_data_l1 = dataFee.toString();
  }

  if (receipt.transactionIndex) {
    const txIndex = hexToNumber(receipt.transactionIndex, 0);
    metadata.event_path = `tx:${txIndex}`;
  }

  return metadata;
}

function buildNativeTransferEvent(
  watchedAddress: string,
  counterparty: string,
  amount: bigint,
  txIndex: number,
  eventPath: string,
  metadata: Record<string, string>,
  isDebit: boolean
): BalanceEventInfo {
  return {
    outerInstructionIndex: txIndex,
    innerInstructionIndex: -1,
    eventCategory: 'TRANSFER',
    eventAction: 'native_transfer',
    programId: ZERO_PROGRAM_ID,
    address: watchedAddress,
    contractAddress: 'ETH',
    delta: isDebit ? `-${amount.toString()}` : amount.toString(),
    counterpartyAddress: counterparty,
    tokenSymbol: 'ETH',
    tokenName: 'Ether',
    tokenDecimals: 18,
    tokenType: 'NATIVE',
    metadata: {
      ...metadata,
      event_path: eventPath,
    },
  };
}

function buildERC20TransferEvent(
  watchedAddress: string,
  counterparty: string,
  contractAddress: string,
  amount: bigint,
  txIndex: number,
  metadata: Record<string, string>,
  isDebit: boolean
): BalanceEventInfo {
  return {
    outerInstructionIndex: txIndex,
    innerInstructionIndex: -1,
    eventCategory: 'TRANSFER',
    eventAction: 'erc20_transfer',
    programId: contractAddress || ZERO_PROGRAM_ID,
    address: watchedAddress,
    contractAddress: contractAddress || 'ETH',
    delta: isDebit ? `-${amount.toString()}` : amount.toString(),
    counterpartyAddress: counterparty,
    tokenSymbol: '',
    tokenName: '',
    tokenDecimals: 18,
    tokenType: 'FUNGIBLE',
    metadata,
  };
}

function buildERC20MintEvent(
  watchedAddress: string,
  counterparty: string,
  contractAddress: string,
  amount: bigint,
  txIndex: number,
  metadata: Record<string, string>,
): BalanceEventInfo {
  return {
    outerInstructionIndex: txIndex,
    innerInstructionIndex: -1,
    eventCategory: 'MINT',
    eventAction: 'erc20_mint',
    programId: normalizeAddress(contractAddress) || ZERO_PROGRAM_ID,
    address: watchedAddress,
    contractAddress: normalizeAddress(contractAddress) || ZERO_PROGRAM_ID,
    delta: amount.toString(),
    counterpartyAddress: counterparty,
    tokenSymbol: '',
    tokenName: '',
    tokenDecimals: 18,
    tokenType: 'FUNGIBLE',
    metadata,
  };
}

function buildERC20BurnEvent(
  watchedAddress: string,
  counterparty: string,
  contractAddress: string,
  amount: bigint,
  txIndex: number,
  metadata: Record<string, string>,
): BalanceEventInfo {
  return {
    outerInstructionIndex: txIndex,
    innerInstructionIndex: -1,
    eventCategory: 'BURN',
    eventAction: 'erc20_burn',
    programId: normalizeAddress(contractAddress) || ZERO_PROGRAM_ID,
    address: watchedAddress,
    contractAddress: normalizeAddress(contractAddress) || ZERO_PROGRAM_ID,
    delta: `-${amount.toString()}`,
    counterpartyAddress: counterparty,
    tokenSymbol: '',
    tokenName: '',
    tokenDecimals: 18,
    tokenType: 'FUNGIBLE',
    metadata,
  };
}

function normalizeTopic(topic?: string): string {
  return (topic || '').toLowerCase();
}

function normalizeAddress(address?: string): string {
  return (address || '').toLowerCase();
}

function topicToAddress(topic?: string): string {
  const value = normalizeTopic(topic).replace(/^0x/, '');
  if (value.length < 40) {
    return '';
  }
  return `0x${value.slice(value.length - 40)}`;
}

function parseTimestamp(raw: string | number | undefined): number {
  if (typeof raw === 'number') {
    return Number.isFinite(raw) ? Math.floor(raw) : 0;
  }
  return hexToNumber(raw);
}

function hexToNumber(value: string | undefined, fallback: number = 0): number {
  if (!value) {
    return fallback;
  }
  try {
    const parsed = toBigInt(value);
    const num = Number(parsed);
    return Number.isFinite(num) ? num : fallback;
  } catch {
    return fallback;
  }
}

function toBigInt(value: unknown): bigint {
  if (typeof value === 'bigint') {
    return value;
  }
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) {
      return 0n;
    }
    return BigInt(Math.floor(value));
  }
  if (typeof value !== 'string') {
    return 0n;
  }

  const trimmed = value.trim();
  if (trimmed === '') {
    return 0n;
  }

  try {
    if (trimmed.startsWith('0x') || trimmed.startsWith('0X')) {
      return BigInt(trimmed);
    }
    return BigInt(trimmed);
  } catch {
    return 0n;
  }
}

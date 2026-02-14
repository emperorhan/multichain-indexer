import {
  BalanceEvent,
  ParsedOuterInstruction,
  ParsedInnerInstruction,
  createDefaultDispatcher,
  PluginDispatcher,
} from './plugin';

export interface BalanceEventInfo {
  outerInstructionIndex: number;
  innerInstructionIndex: number;
  eventCategory: string;
  eventAction: string;
  programId: string;
  address: string;
  contractAddress: string;
  delta: string;
  counterpartyAddress: string;
  tokenSymbol: string;
  tokenName: string;
  tokenDecimals: number;
  tokenType: string;
  metadata: Record<string, string>;
}

export interface TransactionResult {
  txHash: string;
  blockCursor: number;
  blockTime: number;
  feeAmount: string;
  feePayer: string;
  status: string;
  error?: string;
  balanceEvents: BalanceEventInfo[];
  metadata?: Record<string, string>;
}

const defaultDispatcher = createDefaultDispatcher();

export function decodeSolanaTransaction(
  txResponse: any,
  signature: string,
  watchedAddresses: Set<string>,
  dispatcher: PluginDispatcher = defaultDispatcher,
): TransactionResult {
  const slot: number = txResponse.slot || 0;
  const blockTime: number = txResponse.blockTime || 0;
  const meta = txResponse.meta;
  const transaction = txResponse.transaction;

  // Extract fee payer (first account key)
  const accountKeys = transaction?.message?.accountKeys || [];
  const feePayer = typeof accountKeys[0] === 'string'
    ? accountKeys[0]
    : accountKeys[0]?.pubkey || '';

  // Determine status
  const hasError = meta?.err !== null && meta?.err !== undefined;
  const status = hasError ? 'FAILED' : 'SUCCESS';
  const errorMsg = hasError ? JSON.stringify(meta.err) : undefined;

  // Fee
  const fee = String(meta?.fee || 0);

  // Skip event parsing for failed transactions
  if (hasError) {
    return {
      txHash: signature,
      blockCursor: slot,
      blockTime,
      feeAmount: fee,
      feePayer,
      status,
      error: errorMsg,
      balanceEvents: [],
    };
  }

  // Build account key lookup map
  const accountKeyMap = buildAccountKeyMap(accountKeys);

  // Build instruction tree (preserving outer/inner structure)
  const outerInstructions = buildInstructionTree(transaction, meta, accountKeyMap);

  // Dispatch through plugin system
  const rawEvents = dispatcher.dispatch(outerInstructions, watchedAddresses, accountKeyMap, meta);

  // Convert BalanceEvent to BalanceEventInfo
  const balanceEvents: BalanceEventInfo[] = rawEvents.map((ev) => ({
    outerInstructionIndex: ev.outerInstructionIndex,
    innerInstructionIndex: ev.innerInstructionIndex,
    eventCategory: ev.eventCategory,
    eventAction: ev.eventAction,
    programId: ev.programId,
    address: ev.address,
    contractAddress: ev.contractAddress,
    delta: ev.delta,
    counterpartyAddress: ev.counterpartyAddress,
    tokenSymbol: ev.tokenSymbol,
    tokenName: ev.tokenName,
    tokenDecimals: ev.tokenDecimals,
    tokenType: ev.tokenType,
    metadata: ev.metadata,
  }));

  // Emit FEE event if fee payer is watched
  if (watchedAddresses.has(feePayer) && fee !== '0') {
    balanceEvents.push({
      outerInstructionIndex: -1,
      innerInstructionIndex: -1,
      eventCategory: 'FEE',
      eventAction: 'transaction_fee',
      programId: '11111111111111111111111111111111',
      address: feePayer,
      contractAddress: '11111111111111111111111111111111',
      delta: `-${fee}`,
      counterpartyAddress: '',
      tokenSymbol: 'SOL',
      tokenName: 'Solana',
      tokenDecimals: 9,
      tokenType: 'NATIVE',
      metadata: {},
    });
  }

  return {
    txHash: signature,
    blockCursor: slot,
    blockTime,
    feeAmount: fee,
    feePayer,
    status,
    error: errorMsg,
    balanceEvents,
  };
}

function buildAccountKeyMap(accountKeys: any[]): Map<number, string> {
  const map = new Map<number, string>();
  for (let i = 0; i < accountKeys.length; i++) {
    const key = typeof accountKeys[i] === 'string'
      ? accountKeys[i]
      : accountKeys[i]?.pubkey || '';
    map.set(i, key);
  }
  return map;
}

function resolveProgramId(instruction: any, accountKeyMap: Map<number, string>): string {
  if (instruction.programId) return instruction.programId;
  if (instruction.programIdIndex !== undefined) {
    return accountKeyMap.get(instruction.programIdIndex) || '';
  }
  return '';
}

export function buildInstructionTree(
  transaction: any,
  meta: any,
  accountKeyMap: Map<number, string>,
): ParsedOuterInstruction[] {
  const outerInstructions = transaction?.message?.instructions || [];
  const result: ParsedOuterInstruction[] = [];

  for (let i = 0; i < outerInstructions.length; i++) {
    const outerIx = outerInstructions[i];
    const programId = resolveProgramId(outerIx, accountKeyMap);

    // Collect inner instructions for this outer instruction
    const innerGroup = meta?.innerInstructions?.find((g: any) => g.index === i);
    const innerInstructions: ParsedInnerInstruction[] = [];

    if (innerGroup) {
      for (let j = 0; j < innerGroup.instructions.length; j++) {
        const innerIx = innerGroup.instructions[j];
        innerInstructions.push({
          innerIndex: j,
          programId: resolveProgramId(innerIx, accountKeyMap),
          instruction: innerIx,
        });
      }
    }

    result.push({
      outerIndex: i,
      programId,
      instruction: outerIx,
      innerInstructions,
    });
  }

  return result;
}

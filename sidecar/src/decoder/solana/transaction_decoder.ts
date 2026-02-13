import { parseSplTokenInstruction } from './spl_token_parser';
import { parseSystemInstruction } from './system_parser';

const SPL_TOKEN_PROGRAM_ID = 'TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA';
const SPL_TOKEN_2022_PROGRAM_ID = 'TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb';
const SYSTEM_PROGRAM_ID = '11111111111111111111111111111111';

interface TransferInfo {
  instructionIndex: number;
  programId: string;
  fromAddress: string;
  toAddress: string;
  amount: string;
  contractAddress: string;
  transferType: string;
  fromAta: string;
  toAta: string;
  tokenSymbol: string;
  tokenName: string;
  tokenDecimals: number;
  tokenType: string;
}

interface TransactionResult {
  txHash: string;
  blockCursor: number;
  blockTime: number;
  feeAmount: string;
  feePayer: string;
  status: string;
  error?: string;
  transfers: TransferInfo[];
}

export function decodeSolanaTransaction(
  txResponse: any,
  signature: string,
  watchedAddresses: Set<string>
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

  // Skip transfer parsing for failed transactions
  if (hasError) {
    return {
      txHash: signature,
      blockCursor: slot,
      blockTime,
      feeAmount: fee,
      feePayer,
      status,
      error: errorMsg,
      transfers: [],
    };
  }

  // Build account key lookup map
  const accountKeyMap = buildAccountKeyMap(accountKeys);

  // Parse all instructions (outer + inner)
  const allInstructions = flattenInstructions(transaction, meta);
  const transfers: TransferInfo[] = [];

  for (const { instruction, index } of allInstructions) {
    const programId = resolveProgramId(instruction, accountKeyMap);

    let parsedTransfers: TransferInfo[] = [];

    if (programId === SPL_TOKEN_PROGRAM_ID || programId === SPL_TOKEN_2022_PROGRAM_ID) {
      parsedTransfers = parseSplTokenInstruction(instruction, index, programId, accountKeyMap, meta);
    } else if (programId === SYSTEM_PROGRAM_ID) {
      parsedTransfers = parseSystemInstruction(instruction, index, accountKeyMap);
    }

    // Filter: only include transfers involving watched addresses
    for (const transfer of parsedTransfers) {
      if (
        watchedAddresses.has(transfer.fromAddress) ||
        watchedAddresses.has(transfer.toAddress)
      ) {
        transfers.push(transfer);
      }
    }
  }

  return {
    txHash: signature,
    blockCursor: slot,
    blockTime,
    feeAmount: fee,
    feePayer,
    status,
    error: errorMsg,
    transfers,
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
  // jsonParsed format
  if (instruction.programId) {
    return instruction.programId;
  }
  // Fallback: use programIdIndex
  if (instruction.programIdIndex !== undefined) {
    return accountKeyMap.get(instruction.programIdIndex) || '';
  }
  return '';
}

interface FlatInstruction {
  instruction: any;
  index: number;
}

function flattenInstructions(transaction: any, meta: any): FlatInstruction[] {
  const result: FlatInstruction[] = [];
  let globalIndex = 0;

  // Outer instructions
  const outerInstructions = transaction?.message?.instructions || [];
  for (let i = 0; i < outerInstructions.length; i++) {
    result.push({ instruction: outerInstructions[i], index: globalIndex++ });

    // Inner instructions for this outer instruction
    const innerGroup = meta?.innerInstructions?.find((g: any) => g.index === i);
    if (innerGroup) {
      for (const innerIx of innerGroup.instructions) {
        result.push({ instruction: innerIx, index: globalIndex++ });
      }
    }
  }

  return result;
}

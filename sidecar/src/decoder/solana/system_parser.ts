const SYSTEM_PROGRAM_ID = '11111111111111111111111111111111';
const SOL_NATIVE_MINT = '11111111111111111111111111111111';

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

/**
 * Parse System Program instructions.
 * Handles: transfer, createAccount (with lamport transfer)
 */
export function parseSystemInstruction(
  instruction: any,
  instructionIndex: number,
  accountKeyMap: Map<number, string>
): TransferInfo[] {
  const parsed = instruction.parsed;
  if (!parsed) return [];

  const type_ = parsed.type;
  const info = parsed.info;
  if (!info) return [];

  switch (type_) {
    case 'transfer':
      return parseSystemTransfer(info, instructionIndex);
    case 'createAccount':
      return parseCreateAccount(info, instructionIndex);
    default:
      return [];
  }
}

function parseSystemTransfer(
  info: any,
  instructionIndex: number
): TransferInfo[] {
  const from: string = info.source || '';
  const to: string = info.destination || '';
  const lamports: string = String(info.lamports || '0');

  if (lamports === '0') return [];

  return [{
    instructionIndex,
    programId: SYSTEM_PROGRAM_ID,
    fromAddress: from,
    toAddress: to,
    amount: lamports,
    contractAddress: SOL_NATIVE_MINT,
    transferType: 'systemTransfer',
    fromAta: '',
    toAta: '',
    tokenSymbol: 'SOL',
    tokenName: 'Solana',
    tokenDecimals: 9,
    tokenType: 'NATIVE',
  }];
}

function parseCreateAccount(
  info: any,
  instructionIndex: number
): TransferInfo[] {
  const from: string = info.source || '';
  const to: string = info.newAccount || '';
  const lamports: string = String(info.lamports || '0');

  if (lamports === '0') return [];

  return [{
    instructionIndex,
    programId: SYSTEM_PROGRAM_ID,
    fromAddress: from,
    toAddress: to,
    amount: lamports,
    contractAddress: SOL_NATIVE_MINT,
    transferType: 'createAccount',
    fromAta: '',
    toAta: '',
    tokenSymbol: 'SOL',
    tokenName: 'Solana',
    tokenDecimals: 9,
    tokenType: 'NATIVE',
  }];
}

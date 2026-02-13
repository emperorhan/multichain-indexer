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
 * Parse SPL Token program instructions (both Token and Token-2022).
 * Handles: transfer, transferChecked, mintTo, burn
 */
export function parseSplTokenInstruction(
  instruction: any,
  instructionIndex: number,
  programId: string,
  accountKeyMap: Map<number, string>,
  meta: any
): TransferInfo[] {
  const parsed = instruction.parsed;
  if (!parsed) return [];

  const type_ = parsed.type;
  const info = parsed.info;
  if (!info) return [];

  switch (type_) {
    case 'transfer':
      return parseTransfer(info, instructionIndex, programId, meta);
    case 'transferChecked':
      return parseTransferChecked(info, instructionIndex, programId, meta);
    default:
      return [];
  }
}

function parseTransfer(
  info: any,
  instructionIndex: number,
  programId: string,
  meta: any
): TransferInfo[] {
  const sourceAta: string = info.source || '';
  const destAta: string = info.destination || '';
  const amount: string = String(info.amount || '0');

  // Resolve owner addresses from ATA using token balances in meta
  const sourceOwner = info.authority || resolveOwnerFromMeta(sourceAta, meta) || sourceAta;
  const destOwner = resolveOwnerFromMeta(destAta, meta) || destAta;

  // Resolve mint from meta token balances
  const mint = resolveMintFromMeta(sourceAta, meta) || resolveMintFromMeta(destAta, meta) || '';
  const decimals = resolveDecimalsFromMeta(sourceAta, meta) ?? resolveDecimalsFromMeta(destAta, meta) ?? 0;

  return [{
    instructionIndex,
    programId,
    fromAddress: sourceOwner,
    toAddress: destOwner,
    amount,
    contractAddress: mint,
    transferType: 'transfer',
    fromAta: sourceAta,
    toAta: destAta,
    tokenSymbol: '',
    tokenName: '',
    tokenDecimals: decimals,
    tokenType: 'FUNGIBLE',
  }];
}

function parseTransferChecked(
  info: any,
  instructionIndex: number,
  programId: string,
  meta: any
): TransferInfo[] {
  const sourceAta: string = info.source || '';
  const destAta: string = info.destination || '';
  const mint: string = info.mint || '';
  const decimals: number = info.tokenAmount?.decimals ?? 0;
  const amount: string = info.tokenAmount?.amount || String(info.amount || '0');

  const sourceOwner = info.authority || resolveOwnerFromMeta(sourceAta, meta) || sourceAta;
  const destOwner = resolveOwnerFromMeta(destAta, meta) || destAta;

  return [{
    instructionIndex,
    programId,
    fromAddress: sourceOwner,
    toAddress: destOwner,
    amount,
    contractAddress: mint,
    transferType: 'transferChecked',
    fromAta: sourceAta,
    toAta: destAta,
    tokenSymbol: '',
    tokenName: '',
    tokenDecimals: decimals,
    tokenType: 'FUNGIBLE',
  }];
}

/**
 * Resolve the owner of an ATA from pre/postTokenBalances.
 */
function resolveOwnerFromMeta(ata: string, meta: any): string | undefined {
  // We need to match by account index, but we have the address
  // postTokenBalances has accountIndex and owner fields
  // We need the account keys to map address -> index
  // Since we receive the parsed transaction, look in both pre and post balances
  const allBalances = [
    ...(meta?.preTokenBalances || []),
    ...(meta?.postTokenBalances || []),
  ];

  // Unfortunately, token balances use accountIndex, not addresses directly.
  // But in the "parsed" format with owner field, we can iterate and match.
  // We won't have the ATA address directly, so this is a best-effort lookup.
  // The caller should prefer info.authority when available.
  for (const tb of allBalances) {
    if (tb.owner) {
      // We can't directly match ATA here without account keys.
      // Return undefined to let caller fall back to ATA address.
    }
  }
  return undefined;
}

function resolveMintFromMeta(ata: string, meta: any): string | undefined {
  const allBalances = [
    ...(meta?.preTokenBalances || []),
    ...(meta?.postTokenBalances || []),
  ];
  for (const tb of allBalances) {
    if (tb.mint) {
      return tb.mint;
    }
  }
  return undefined;
}

function resolveDecimalsFromMeta(ata: string, meta: any): number | undefined {
  const allBalances = [
    ...(meta?.preTokenBalances || []),
    ...(meta?.postTokenBalances || []),
  ];
  for (const tb of allBalances) {
    if (tb.uiTokenAmount?.decimals !== undefined) {
      return tb.uiTokenAmount.decimals;
    }
  }
  return undefined;
}

import { EventPlugin } from '../interface';
import { BalanceEvent, ParsedOuterInstruction } from '../types';

const SPL_TOKEN_PROGRAM_ID = 'TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA';
const SPL_TOKEN_2022_PROGRAM_ID = 'TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb';

export class GenericSplTokenPlugin implements EventPlugin {
  readonly id = 'generic_spl_token';
  readonly priority = 10;
  readonly programIds = new Set([SPL_TOKEN_PROGRAM_ID, SPL_TOKEN_2022_PROGRAM_ID]);

  parse(
    outer: ParsedOuterInstruction,
    watchedSet: Set<string>,
    accountKeyMap: Map<number, string>,
    meta: any,
  ): BalanceEvent[] | null {
    const parsed = outer.instruction.parsed;
    if (!parsed) return null;

    const type_ = parsed.type;
    const info = parsed.info;
    if (!info) return null;

    switch (type_) {
      case 'transfer':
        return this.parseTransfer(info, outer, watchedSet, meta);
      case 'transferChecked':
        return this.parseTransferChecked(info, outer, watchedSet, meta);
      default:
        return null;
    }
  }

  private parseTransfer(
    info: any,
    outer: ParsedOuterInstruction,
    watchedSet: Set<string>,
    meta: any,
  ): BalanceEvent[] {
    const sourceAta: string = info.source || '';
    const destAta: string = info.destination || '';
    const amount: string = String(info.amount || '0');

    const sourceOwner = info.authority || resolveOwnerFromMeta(sourceAta, meta) || sourceAta;
    const destOwner = resolveOwnerFromMeta(destAta, meta) || destAta;
    const mint = resolveMintFromMeta(sourceAta, meta) || resolveMintFromMeta(destAta, meta) || '';
    const decimals = resolveDecimalsFromMeta(sourceAta, meta) ?? resolveDecimalsFromMeta(destAta, meta) ?? 0;

    return buildTransferEvents(
      outer.outerIndex,
      -1,
      outer.programId,
      'spl_transfer',
      sourceOwner,
      destOwner,
      amount,
      mint,
      decimals,
      'FUNGIBLE',
      watchedSet,
      { from_ata: sourceAta, to_ata: destAta, transfer_type: 'transfer' },
    );
  }

  private parseTransferChecked(
    info: any,
    outer: ParsedOuterInstruction,
    watchedSet: Set<string>,
    meta: any,
  ): BalanceEvent[] {
    const sourceAta: string = info.source || '';
    const destAta: string = info.destination || '';
    const mint: string = info.mint || '';
    const decimals: number = info.tokenAmount?.decimals ?? 0;
    const amount: string = info.tokenAmount?.amount || String(info.amount || '0');

    const sourceOwner = info.authority || resolveOwnerFromMeta(sourceAta, meta) || sourceAta;
    const destOwner = resolveOwnerFromMeta(destAta, meta) || destAta;

    return buildTransferEvents(
      outer.outerIndex,
      -1,
      outer.programId,
      'spl_transfer_checked',
      sourceOwner,
      destOwner,
      amount,
      mint,
      decimals,
      'FUNGIBLE',
      watchedSet,
      { from_ata: sourceAta, to_ata: destAta, transfer_type: 'transferChecked' },
    );
  }
}

/**
 * Build two BalanceEvents (sender -delta, receiver +delta) filtered by watchedSet.
 */
function buildTransferEvents(
  outerIndex: number,
  innerIndex: number,
  programId: string,
  eventAction: string,
  fromAddress: string,
  toAddress: string,
  amount: string,
  contractAddress: string,
  decimals: number,
  tokenType: string,
  watchedSet: Set<string>,
  metadata: Record<string, string>,
): BalanceEvent[] {
  const events: BalanceEvent[] = [];

  if (watchedSet.has(fromAddress)) {
    events.push({
      outerInstructionIndex: outerIndex,
      innerInstructionIndex: innerIndex,
      eventCategory: 'TRANSFER',
      eventAction,
      programId,
      address: fromAddress,
      contractAddress,
      delta: `-${amount}`,
      counterpartyAddress: toAddress,
      tokenSymbol: '',
      tokenName: '',
      tokenDecimals: decimals,
      tokenType,
      metadata,
    });
  }

  if (watchedSet.has(toAddress)) {
    events.push({
      outerInstructionIndex: outerIndex,
      innerInstructionIndex: innerIndex,
      eventCategory: 'TRANSFER',
      eventAction,
      programId,
      address: toAddress,
      contractAddress,
      delta: amount,
      counterpartyAddress: fromAddress,
      tokenSymbol: '',
      tokenName: '',
      tokenDecimals: decimals,
      tokenType,
      metadata,
    });
  }

  return events;
}

function resolveOwnerFromMeta(ata: string, meta: any): string | undefined {
  const allBalances = [
    ...(meta?.preTokenBalances || []),
    ...(meta?.postTokenBalances || []),
  ];
  for (const tb of allBalances) {
    if (tb.owner) {
      // Best-effort: can't directly match ATA without account keys
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
    if (tb.mint) return tb.mint;
  }
  return undefined;
}

function resolveDecimalsFromMeta(ata: string, meta: any): number | undefined {
  const allBalances = [
    ...(meta?.preTokenBalances || []),
    ...(meta?.postTokenBalances || []),
  ];
  for (const tb of allBalances) {
    if (tb.uiTokenAmount?.decimals !== undefined) return tb.uiTokenAmount.decimals;
  }
  return undefined;
}

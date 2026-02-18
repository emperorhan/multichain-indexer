import { EventPlugin } from '../interface';
import { BalanceEvent, ParsedOuterInstruction } from '../types';
import { BalanceValidator } from '../balance_validator';

const SPL_TOKEN_PROGRAM_ID = 'TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA';

export class GenericSplTokenPlugin implements EventPlugin {
  readonly id = 'generic_spl_token';
  readonly priority = 10;
  readonly programIds = new Set([SPL_TOKEN_PROGRAM_ID]);

  parse(
    outer: ParsedOuterInstruction,
    watchedSet: Set<string>,
    accountKeyMap: Map<number, string>,
    meta: any,
  ): BalanceEvent[] | null {
    const parsed = outer.instruction?.parsed;
    if (!parsed || typeof parsed.type !== 'string') {
      return null;
    }

    const info: any = parsed.info || {};

    let events: BalanceEvent[] | null = null;
    switch (parsed.type) {
      case 'transfer':
        events = parseTokenTransfer(outer, info, watchedSet, accountKeyMap, meta, false);
        break;
      case 'transferChecked':
        events = parseTokenTransfer(outer, info, watchedSet, accountKeyMap, meta, true);
        break;
      case 'mintTo':
        events = parseTokenMint(outer, info, watchedSet, accountKeyMap, meta, false);
        break;
      case 'mintToChecked':
        events = parseTokenMint(outer, info, watchedSet, accountKeyMap, meta, true);
        break;
      case 'burn':
        events = parseTokenBurn(outer, info, watchedSet, accountKeyMap, meta, false);
        break;
      case 'burnChecked':
        events = parseTokenBurn(outer, info, watchedSet, accountKeyMap, meta, true);
        break;
      default:
        return null;
    }

    if (events && events.length > 0) {
      const balanceMap = BalanceValidator.buildBalanceMap(meta);
      for (const ev of events) {
        const signal = BalanceValidator.validateEvent(ev, balanceMap);
        if (signal) {
          ev.metadata = { ...ev.metadata, scam_signal: signal };
        }
      }
    }

    return events;
  }
}

function parseTokenTransfer(
  outer: ParsedOuterInstruction,
  info: any,
  watchedSet: Set<string>,
  accountKeyMap: Map<number, string>,
  meta: any,
  checked: boolean,
): BalanceEvent[] {
  const amount = parseAmount(info.amount ?? info.tokenAmount?.amount);
  if (amount <= 0n) {
    return [];
  }

  const sourceTokenAccount = resolveAccount(info.source, accountKeyMap);
  const destinationTokenAccount = resolveAccount(info.destination, accountKeyMap);
  const authority = resolveAccount(info.authority, accountKeyMap);
  const sourceOwner = resolveOwnerFromBalances(meta, sourceTokenAccount, accountKeyMap) || authority || sourceTokenAccount;
  const destinationOwner = resolveOwnerFromBalances(meta, destinationTokenAccount, accountKeyMap) || destinationTokenAccount;
  const mint = resolveMint(info.mint, sourceTokenAccount, destinationTokenAccount, accountKeyMap, meta);
  const decimals = resolveDecimals(info);

  const action = checked ? 'spl_transfer_checked' : 'spl_transfer';
  const eventPath = `outer:${outer.outerIndex}`;
  const commonMetadata: Record<string, string> = {
    event_path: eventPath,
  };
  const events: BalanceEvent[] = [];

  if (sourceOwner && watchedSet.has(sourceOwner)) {
    events.push(buildSplTransferEvent(
      outer.outerIndex,
      -1,
      'TRANSFER',
      action,
      sourceOwner,
      mint,
      `-${amount.toString()}`,
      destinationOwner,
      decimals,
      commonMetadata,
    ));
  }

  if (destinationOwner && watchedSet.has(destinationOwner) && destinationOwner !== sourceOwner) {
    events.push(buildSplTransferEvent(
      outer.outerIndex,
      -1,
      'TRANSFER',
      action,
      destinationOwner,
      mint,
      `${amount.toString()}`,
      sourceOwner,
      decimals,
      commonMetadata,
    ));
  }

  return events;
}

function parseTokenMint(
  outer: ParsedOuterInstruction,
  info: any,
  watchedSet: Set<string>,
  accountKeyMap: Map<number, string>,
  meta: any,
  checked: boolean,
): BalanceEvent[] {
  const amount = parseAmount(info.amount ?? info.tokenAmount?.amount);
  if (amount <= 0n) {
    return [];
  }

  const destinationTokenAccount = resolveAccount(info.account, accountKeyMap);
  const authority = resolveAccount(info.authority, accountKeyMap);
  const recipient = resolveOwnerFromBalances(meta, destinationTokenAccount, accountKeyMap)
    || destinationTokenAccount
    || authority;
  if (!recipient || !watchedSet.has(recipient)) {
    return [];
  }

  const action = checked ? 'spl_mint_checked' : 'spl_mint';
  const mint = resolveMint(info.mint, destinationTokenAccount, undefined, accountKeyMap, meta);
  const decimals = resolveDecimals(info);
  return [
    buildSplTransferEvent(
      outer.outerIndex,
      -1,
      'MINT',
      action,
      recipient,
      mint,
      `${amount.toString()}`,
      authority,
      decimals,
      {
        event_path: `outer:${outer.outerIndex}`,
      },
    ),
  ];
}

function parseTokenBurn(
  outer: ParsedOuterInstruction,
  info: any,
  watchedSet: Set<string>,
  accountKeyMap: Map<number, string>,
  meta: any,
  checked: boolean,
): BalanceEvent[] {
  const amount = parseAmount(info.amount ?? info.tokenAmount?.amount);
  if (amount <= 0n) {
    return [];
  }

  const sourceTokenAccount = resolveAccount(info.account, accountKeyMap);
  const authority = resolveAccount(info.authority, accountKeyMap) || resolveAccount(info.owner, accountKeyMap);
  const burner = resolveOwnerFromBalances(meta, sourceTokenAccount, accountKeyMap)
    || authority
    || sourceTokenAccount;
  if (!burner || !watchedSet.has(burner)) {
    return [];
  }

  const action = checked ? 'spl_burn_checked' : 'spl_burn';
  const mint = resolveMint(info.mint, sourceTokenAccount, undefined, accountKeyMap, meta);
  const decimals = resolveDecimals(info);
  return [
    buildSplTransferEvent(
      outer.outerIndex,
      -1,
      'BURN',
      action,
      burner,
      mint,
      `-${amount.toString()}`,
      authority,
      decimals,
      {
        event_path: `outer:${outer.outerIndex}`,
      },
    ),
  ];
}

function buildSplTransferEvent(
  outerIndex: number,
  innerIndex: number,
  eventCategory: 'TRANSFER' | 'MINT' | 'BURN',
  eventAction: string,
  address: string,
  contractAddress: string,
  delta: string,
  counterpartyAddress: string,
  decimals: number,
  metadata: Record<string, string>,
): BalanceEvent {
  return {
    outerInstructionIndex: outerIndex,
    innerInstructionIndex: innerIndex,
    eventCategory,
    eventAction,
    programId: SPL_TOKEN_PROGRAM_ID,
    address,
    contractAddress,
    delta,
    counterpartyAddress,
    tokenSymbol: '',
    tokenName: '',
    tokenDecimals: decimals,
    tokenType: 'FUNGIBLE',
    metadata,
  };
}

function resolveAccount(value: unknown, accountKeyMap: Map<number, string>): string {
  if (typeof value === 'number') {
    return accountKeyMap.get(value) || '';
  }
  if (typeof value === 'string') {
    const normalized = value.trim();
    const parsedIndex = Number(normalized);
    if (Number.isInteger(parsedIndex) && String(parsedIndex) === normalized) {
      return accountKeyMap.get(parsedIndex) || normalized;
    }
    return normalized;
  }
  return '';
}

function findAccountIndex(tokenAccount: string, accountKeyMap: Map<number, string>): number | undefined {
  for (const [index, key] of accountKeyMap.entries()) {
    if (key === tokenAccount) {
      return index;
    }
  }
  return undefined;
}

function resolveOwnerFromBalances(
  meta: any,
  tokenAccount: string,
  accountKeyMap: Map<number, string>,
): string {
  const tokenIndex = findAccountIndex(tokenAccount, accountKeyMap);
  if (tokenIndex === undefined) {
    return '';
  }

  const fromPre = readOwner(meta?.preTokenBalances, tokenIndex);
  if (fromPre) {
    return fromPre;
  }
  return readOwner(meta?.postTokenBalances, tokenIndex);
}

function resolveMint(
  mint: unknown,
  sourceTokenAccount: string,
  destinationTokenAccount: string | undefined,
  accountKeyMap: Map<number, string>,
  meta: any,
): string {
  const mintedMint = typeof mint === 'string' ? mint : '';
  if (mintedMint.length > 0) {
    return mintedMint;
  }

  const sourceIndex = findAccountIndex(sourceTokenAccount, accountKeyMap);
  const destinationIndex = destinationTokenAccount ? findAccountIndex(destinationTokenAccount, accountKeyMap) : undefined;
  const sourceMint = sourceIndex === undefined ? '' : readMint(meta?.preTokenBalances, sourceIndex) || readMint(meta?.postTokenBalances, sourceIndex);
  if (sourceMint) {
    return sourceMint;
  }
  if (destinationIndex === undefined) {
    return '';
  }
  return readMint(meta?.preTokenBalances, destinationIndex) || readMint(meta?.postTokenBalances, destinationIndex) || '';
}

function readOwner(values: any, index: number): string {
  if (!Array.isArray(values)) {
    return '';
  }
  for (const item of values) {
    if (!item || item.accountIndex === undefined) {
      continue;
    }
    if (Number(item.accountIndex) === index && typeof item.owner === 'string') {
      return item.owner;
    }
  }
  return '';
}

function readMint(values: any, index: number): string {
  if (!Array.isArray(values)) {
    return '';
  }
  for (const item of values) {
    if (!item || item.accountIndex === undefined || typeof item.mint !== 'string') {
      continue;
    }
    if (Number(item.accountIndex) === index) {
      return item.mint;
    }
  }
  return '';
}

function resolveDecimals(info: any): number {
  const decimalsValue = info?.decimals ?? info?.tokenAmount?.decimals;
  if (typeof decimalsValue === 'number' && Number.isInteger(decimalsValue) && decimalsValue >= 0) {
    return decimalsValue;
  }
  if (typeof decimalsValue === 'string' && decimalsValue.trim() !== '') {
    const parsed = Number(decimalsValue);
    return Number.isFinite(parsed) && parsed >= 0 ? parsed : 0;
  }
  return 0;
}

function parseAmount(raw: unknown): bigint {
  if (typeof raw === 'bigint') {
    return raw;
  }
  if (typeof raw === 'number') {
    if (!Number.isFinite(raw)) {
      return 0n;
    }
    return BigInt(Math.trunc(raw));
  }
  if (typeof raw !== 'string') {
    return 0n;
  }
  const trimmed = raw.trim();
  if (trimmed === '') {
    return 0n;
  }
  try {
    return BigInt(trimmed);
  } catch {
    return 0n;
  }
}

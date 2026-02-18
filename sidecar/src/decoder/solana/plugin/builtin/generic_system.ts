import { EventPlugin } from '../interface';
import { BalanceEvent, ParsedOuterInstruction } from '../types';

const SYSTEM_PROGRAM_ID = '11111111111111111111111111111111';
const SOL_NATIVE_MINT = '11111111111111111111111111111111';

export class GenericSystemPlugin implements EventPlugin {
  readonly id = 'generic_system';
  readonly priority = 10;
  readonly programIds = new Set([SYSTEM_PROGRAM_ID]);

  parse(
    outer: ParsedOuterInstruction,
    watchedSet: Set<string>,
    accountKeyMap: Map<number, string>,
    _meta: any,
  ): BalanceEvent[] | null {
    const parsed = outer.instruction.parsed;
    if (!parsed) return null;

    const type_ = parsed.type;
    const info = parsed.info;
    if (!info) return null;

    switch (type_) {
      case 'transfer':
        return this.parseSystemTransfer(info, outer, watchedSet, 'system_transfer', accountKeyMap);
      case 'transferWithSeed':
        return this.parseSystemTransfer(info, outer, watchedSet, 'system_transfer_with_seed', accountKeyMap);
      case 'createAccount':
        return this.parseCreateAccount(info, outer, watchedSet, accountKeyMap);
      case 'createAccountWithSeed':
        return this.parseCreateAccount(
          info,
          outer,
          watchedSet,
          accountKeyMap,
          'system_create_account_with_seed',
        );
      case 'withdrawNonceAccount':
        return this.parseSystemWithdrawal(info, outer, watchedSet, accountKeyMap);
      default:
        return null;
    }
  }

  private parseSystemTransfer(
    info: any,
    outer: ParsedOuterInstruction,
    watchedSet: Set<string>,
    eventAction = 'system_transfer',
    accountKeyMap?: Map<number, string>,
  ): BalanceEvent[] {
    const from = resolveAddress(firstDefined(info.source, info.fromAddress, info.fromAccount), accountKeyMap);
    const to = resolveAddress(firstDefined(info.destination, info.toAddress, info.toAccount), accountKeyMap);
    const lamports = parseLamports(info.lamports);

    if (lamports === '0') return [];

    return buildSolTransferEvents(
      outer.outerIndex,
      -1,
      eventAction,
      from,
      to,
      lamports,
      watchedSet,
    );
  }

  private parseCreateAccount(
    info: any,
    outer: ParsedOuterInstruction,
    watchedSet: Set<string>,
    accountKeyMap?: Map<number, string>,
    eventAction = 'system_create_account',
  ): BalanceEvent[] {
    const from = resolveAddress(firstDefined(info.source, info.fromAddress, info.fromAccount, info.fromPubkey), accountKeyMap);
    const to = resolveAddress(firstDefined(info.newAccount, info.baseAccount, info.to, info.account), accountKeyMap);
    const lamports = parseLamports(info.lamports);

    if (lamports === '0') return [];

    return buildSolTransferEvents(
      outer.outerIndex,
      -1,
      eventAction,
      from,
      to,
      lamports,
      watchedSet,
    );
  }

  private parseSystemWithdrawal(
    info: any,
    outer: ParsedOuterInstruction,
    watchedSet: Set<string>,
    accountKeyMap?: Map<number, string>,
  ): BalanceEvent[] {
    const from = resolveAddress(firstDefined(info.nonceAccount, info.account, info.fromAccount, info.fromAddress), accountKeyMap);
    const to = resolveAddress(firstDefined(info.to, info.toAddress, info.destination, info.recipient), accountKeyMap);
    const lamports = parseLamports(info.lamports);

    if (lamports === '0') return [];

    return buildSolTransferEvents(
      outer.outerIndex,
      -1,
      'system_withdraw_nonce',
      from,
      to,
      lamports,
      watchedSet,
    );
  }
}

function buildSolTransferEvents(
  outerIndex: number,
  innerIndex: number,
  eventAction: string,
  fromAddress: string,
  toAddress: string,
  amount: string,
  watchedSet: Set<string>,
): BalanceEvent[] {
  const events: BalanceEvent[] = [];

  if (fromAddress && watchedSet.has(fromAddress)) {
    events.push({
      outerInstructionIndex: outerIndex,
      innerInstructionIndex: innerIndex,
      eventCategory: 'TRANSFER',
      eventAction,
      programId: SYSTEM_PROGRAM_ID,
      address: fromAddress,
      contractAddress: SOL_NATIVE_MINT,
      delta: `-${amount}`,
      counterpartyAddress: toAddress,
      tokenSymbol: 'SOL',
      tokenName: 'Solana',
      tokenDecimals: 9,
      tokenType: 'NATIVE',
      metadata: {},
    });
  }

  if (toAddress && watchedSet.has(toAddress)) {
    events.push({
      outerInstructionIndex: outerIndex,
      innerInstructionIndex: innerIndex,
      eventCategory: 'TRANSFER',
      eventAction,
      programId: SYSTEM_PROGRAM_ID,
      address: toAddress,
      contractAddress: SOL_NATIVE_MINT,
      delta: amount,
      counterpartyAddress: fromAddress,
      tokenSymbol: 'SOL',
      tokenName: 'Solana',
      tokenDecimals: 9,
      tokenType: 'NATIVE',
      metadata: {},
    });
  }

  return events;
}

function parseLamports(value: unknown): string {
  if (typeof value === 'number' && Number.isFinite(value)) {
    return String(Math.trunc(value));
  }
  if (typeof value === 'bigint') {
    return value.toString();
  }
  if (typeof value === 'string') {
    const trimmed = value.trim();
    if (trimmed.length === 0) return '0';
    try {
      return BigInt(trimmed).toString();
    } catch {
      return trimmed;
    }
  }
  return '0';
}

function firstDefined(...values: unknown[]): string {
  for (const value of values) {
    if (typeof value === 'number') {
      return String(value);
    }
    if (typeof value === 'string') {
      const normalized = value.trim();
      if (normalized.length > 0) return normalized;
    }
  }
  return '';
}

function resolveAddress(value: unknown, accountKeyMap?: Map<number, string>): string {
  if (typeof value === 'number' && accountKeyMap) {
    return accountKeyMap.get(value) || '';
  }
  if (typeof value === 'string') {
    const trimmed = value.trim();
    const parsedIndex = Number(trimmed);
    if (Number.isInteger(parsedIndex) && String(parsedIndex) === trimmed && accountKeyMap) {
      return accountKeyMap.get(parsedIndex) || trimmed;
    }
    return trimmed;
  }
  return '';
}

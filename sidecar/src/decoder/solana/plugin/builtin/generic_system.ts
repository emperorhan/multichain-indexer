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
    _accountKeyMap: Map<number, string>,
    _meta: any,
  ): BalanceEvent[] | null {
    const parsed = outer.instruction.parsed;
    if (!parsed) return null;

    const type_ = parsed.type;
    const info = parsed.info;
    if (!info) return null;

    switch (type_) {
      case 'transfer':
        return this.parseSystemTransfer(info, outer, watchedSet);
      case 'createAccount':
        return this.parseCreateAccount(info, outer, watchedSet);
      default:
        return null;
    }
  }

  private parseSystemTransfer(
    info: any,
    outer: ParsedOuterInstruction,
    watchedSet: Set<string>,
  ): BalanceEvent[] {
    const from: string = info.source || '';
    const to: string = info.destination || '';
    const lamports: string = String(info.lamports || '0');

    if (lamports === '0') return [];

    return buildSolTransferEvents(
      outer.outerIndex,
      -1,
      'system_transfer',
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
  ): BalanceEvent[] {
    const from: string = info.source || '';
    const to: string = info.newAccount || '';
    const lamports: string = String(info.lamports || '0');

    if (lamports === '0') return [];

    return buildSolTransferEvents(
      outer.outerIndex,
      -1,
      'system_create_account',
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

  if (watchedSet.has(fromAddress)) {
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

  if (watchedSet.has(toAddress)) {
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

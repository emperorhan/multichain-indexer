export type EventCategory =
  | 'TRANSFER'
  | 'STAKE'
  | 'SWAP'
  | 'MINT'
  | 'BURN'
  | 'REWARD'
  | 'FEE';

export interface BalanceEvent {
  outerInstructionIndex: number;
  innerInstructionIndex: number; // -1 = outer ix itself
  eventCategory: EventCategory;
  eventAction: string; // e.g. "spl_transfer", "system_transfer", "stake_delegate"
  programId: string;
  address: string; // account whose balance changed
  contractAddress: string; // mint address
  delta: string; // SIGNED: positive = inflow, negative = outflow
  counterpartyAddress: string;
  tokenSymbol: string;
  tokenName: string;
  tokenDecimals: number;
  tokenType: string; // "NATIVE", "FUNGIBLE", "NFT"
  metadata: Record<string, string>; // plugin-specific data (from_ata, to_ata, etc.)
}

export interface ParsedOuterInstruction {
  outerIndex: number;
  programId: string;
  instruction: any;
  innerInstructions: ParsedInnerInstruction[];
}

export interface ParsedInnerInstruction {
  innerIndex: number;
  programId: string;
  instruction: any;
}

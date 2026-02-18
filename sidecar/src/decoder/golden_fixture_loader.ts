import { TransactionResult } from './solana/transaction_decoder';
import solanaFixtureCatalog from './fixture_output/solana_golden_fixtures.json';
import baseFixtureCatalog from './fixture_output/base_golden_fixtures.json';
import btcFixtureCatalog from './fixture_output/btc_golden_fixtures.json';

export type GoldenChain = 'solana' | 'base' | 'btc';

export interface GoldenBalanceEventSnapshot {
  outer_instruction_index: number;
  inner_instruction_index: number;
  event_category: string;
  event_action: string;
  program_id: string;
  address: string;
  contract_address: string;
  delta: string;
  counterparty_address: string;
  token_symbol: string;
  token_name: string;
  token_decimals: number;
  token_type: string;
  metadata?: Record<string, string>;
}

export interface GoldenFixtureOutputSnapshot {
  tx_hash: string;
  block_cursor: number;
  block_time: number;
  fee_amount: string;
  fee_payer: string;
  status: string;
  error?: string;
  metadata?: Record<string, string>;
  balance_events: GoldenBalanceEventSnapshot[];
}

export interface GoldenFixtureCase {
  fixture_id: string;
  fixture_seed: number;
  run_id: string;
  chain: GoldenChain;
  network: string;
  class_path: string;
  signature: string;
  watched_addresses: string[];
  input: unknown;
  expected_output: GoldenFixtureOutputSnapshot;
}

interface FixtureCatalog {
  fixtures: GoldenFixtureCase[];
}

function normalizeString(raw: unknown): string {
  if (typeof raw !== 'string') {
    return '';
  }
  return raw.trim();
}

function cloneStringMap(raw?: Record<string, string>): Record<string, string> | undefined {
  if (!raw) {
    return undefined;
  }

  const normalized = Object.keys(raw).reduce<Record<string, string>>((acc, key) => {
    const value = raw[key];
    if (typeof value === 'string' && value.length > 0) {
      acc[key] = value;
    }
    return acc;
  }, {});

  return Object.keys(normalized).length > 0 ? normalized : undefined;
}

function canonicalizeFixtureCase(fixture: GoldenFixtureCase): GoldenFixtureCase {
  const normalizedError = normalizeString(fixture.expected_output.error);

  return {
    ...fixture,
    fixture_id: normalizeString(fixture.fixture_id),
    run_id: normalizeString(fixture.run_id),
    chain: fixture.chain,
    network: normalizeString(fixture.network),
    class_path: normalizeString(fixture.class_path),
    signature: normalizeString(fixture.signature),
    watched_addresses: Array.from(new Set((fixture.watched_addresses || []).map(normalizeString).filter(Boolean))),
    input: fixture.input,
    expected_output: {
      ...fixture.expected_output,
      tx_hash: normalizeString(fixture.expected_output.tx_hash),
      block_cursor: fixture.expected_output.block_cursor,
      block_time: fixture.expected_output.block_time,
      fee_amount: normalizeString(fixture.expected_output.fee_amount),
      fee_payer: normalizeString(fixture.expected_output.fee_payer),
      status: normalizeString(fixture.expected_output.status),
      ...(normalizedError ? { error: normalizedError } : {}),
      metadata: cloneStringMap(fixture.expected_output.metadata),
      balance_events: fixture.expected_output.balance_events.map((event) => ({
        ...event,
        event_category: normalizeString(event.event_category),
        event_action: normalizeString(event.event_action),
        program_id: normalizeString(event.program_id),
        address: normalizeString(event.address),
        contract_address: normalizeString(event.contract_address),
        delta: normalizeString(event.delta),
        counterparty_address: normalizeString(event.counterparty_address),
        token_symbol: normalizeString(event.token_symbol),
        token_name: normalizeString(event.token_name),
        token_type: normalizeString(event.token_type),
        metadata: cloneStringMap(event.metadata),
      })),
    },
  };
}

function assertUniqueFixtureIds(fixtures: GoldenFixtureCase[]): void {
  const seen = new Set<string>();
  for (const fixture of fixtures) {
    const fixtureId = normalizeString(fixture.fixture_id);
    if (seen.has(fixtureId)) {
      throw new Error(`duplicate fixture_id in golden fixture catalog: ${fixture.fixture_id}`);
    }
    seen.add(fixtureId);
  }
}

function normalizeAndSortFixtures(rawFixtures: GoldenFixtureCase[]): GoldenFixtureCase[] {
  const fixtures = rawFixtures.map((fixture) => canonicalizeFixtureCase(fixture));
  assertUniqueFixtureIds(fixtures);
  return fixtures.sort((left, right) => {
    if (left.fixture_seed !== right.fixture_seed) {
      return left.fixture_seed - right.fixture_seed;
    }
    return left.fixture_id.localeCompare(right.fixture_id);
  });
}

function catalogFixtures(raw: unknown): GoldenFixtureCase[] {
  if (!raw || typeof raw !== 'object') {
    return [];
  }

  const entry = raw as FixtureCatalog;
  return Array.isArray(entry.fixtures) ? entry.fixtures : [];
}

const SOLANA_GOLDEN_FIXTURES = normalizeAndSortFixtures(catalogFixtures(solanaFixtureCatalog as unknown));
const BASE_GOLDEN_FIXTURES = normalizeAndSortFixtures(catalogFixtures(baseFixtureCatalog as unknown));
const BTC_GOLDEN_FIXTURES = normalizeAndSortFixtures(catalogFixtures(btcFixtureCatalog as unknown));

export const GOLDEN_FIXTURE_CASES: GoldenFixtureCase[] = [
  ...SOLANA_GOLDEN_FIXTURES,
  ...BASE_GOLDEN_FIXTURES,
  ...BTC_GOLDEN_FIXTURES,
];

export function getGoldenFixtures(): GoldenFixtureCase[] {
  return GOLDEN_FIXTURE_CASES.map((fixture) => canonicalizeFixtureCase(fixture));
}

function normalizeMetadata(raw?: Record<string, string>): Record<string, string> | undefined {
  if (!raw) {
    return undefined;
  }

  const keys = Object.keys(raw).filter((key) => key.length > 0).sort((a, b) => a.localeCompare(b));
  if (keys.length === 0) {
    return undefined;
  }

  const metadata: Record<string, string> = {};
  for (const key of keys) {
    metadata[key] = raw[key];
  }

  return metadata;
}

function normalizeEvent(event: GoldenBalanceEventSnapshot): GoldenBalanceEventSnapshot {
  return {
    outer_instruction_index: event.outer_instruction_index,
    inner_instruction_index: event.inner_instruction_index,
    event_category: event.event_category,
    event_action: event.event_action,
    program_id: event.program_id,
    address: event.address,
    contract_address: event.contract_address,
    delta: event.delta,
    counterparty_address: event.counterparty_address,
    token_symbol: event.token_symbol,
    token_name: event.token_name,
    token_decimals: event.token_decimals,
    token_type: event.token_type,
    ...(normalizeMetadata(event.metadata) ? { metadata: normalizeMetadata(event.metadata) } : {}),
  } as GoldenBalanceEventSnapshot;
}

function eventSort(a: GoldenBalanceEventSnapshot, b: GoldenBalanceEventSnapshot): number {
  if (a.outer_instruction_index !== b.outer_instruction_index) {
    return a.outer_instruction_index - b.outer_instruction_index;
  }
  if (a.inner_instruction_index !== b.inner_instruction_index) {
    return a.inner_instruction_index - b.inner_instruction_index;
  }
  const categorySort = a.event_category.localeCompare(b.event_category);
  if (categorySort !== 0) {
    return categorySort;
  }
  const actionSort = a.event_action.localeCompare(b.event_action);
  if (actionSort !== 0) {
    return actionSort;
  }
  const addressSort = a.address.localeCompare(b.address);
  if (addressSort !== 0) {
    return addressSort;
  }
  return a.contract_address.localeCompare(b.contract_address);
}

export function normalizeFixtureOutput(result: TransactionResult): GoldenFixtureOutputSnapshot {
  const normalizedEvents = result.balanceEvents
    .map((event) => {
      const eventMetadata = normalizeMetadata(event.metadata);
      return {
        outer_instruction_index: event.outerInstructionIndex,
        inner_instruction_index: event.innerInstructionIndex,
        event_category: event.eventCategory,
        event_action: event.eventAction,
        program_id: event.programId,
        address: event.address,
        contract_address: event.contractAddress,
        delta: event.delta,
        counterparty_address: event.counterpartyAddress,
        token_symbol: event.tokenSymbol,
        token_name: event.tokenName,
        token_decimals: event.tokenDecimals,
        token_type: event.tokenType,
        ...(eventMetadata ? { metadata: eventMetadata } : {}),
      };
    })
    .sort((left, right) => eventSort(left as GoldenBalanceEventSnapshot, right as GoldenBalanceEventSnapshot));

  const normalized: GoldenFixtureOutputSnapshot = {
    tx_hash: result.txHash,
    block_cursor: result.blockCursor,
    block_time: result.blockTime,
    fee_amount: result.feeAmount,
    fee_payer: result.feePayer,
    status: result.status,
    balance_events: normalizedEvents as GoldenBalanceEventSnapshot[],
  };

  if (result.error) {
    normalized.error = result.error;
  }

  const resultMetadata = normalizeMetadata(result.metadata);
  if (resultMetadata) {
    normalized.metadata = resultMetadata;
  }

  return normalized;
}

export function canonicalEventId(txHash: string, event: GoldenBalanceEventSnapshot): string {
  return [
    txHash,
    event.event_category,
    event.event_action,
    String(event.outer_instruction_index),
    String(event.inner_instruction_index),
    event.address,
    event.contract_address,
    event.delta,
    event.token_type,
  ].join('|');
}

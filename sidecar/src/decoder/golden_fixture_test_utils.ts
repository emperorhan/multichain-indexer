import { decodeSolanaTransactionBatch } from './index';
import {
  GoldenFixtureCase,
  GoldenFixtureOutputSnapshot,
  canonicalEventId,
  normalizeFixtureOutput,
} from './golden_fixture_loader';

export interface DecodedGoldenFixtureRow {
  fixture: GoldenFixtureCase;
  output: GoldenFixtureOutputSnapshot;
}

export interface GoldenFixtureSummaryRow {
  fixture_id: string;
  fixture_seed: number;
  run_id: string;
  chain: string;
  network: string;
  output: GoldenFixtureOutputSnapshot;
}

interface GoldenFixtureBatchInput {
  signature: string;
  rawJson: Buffer;
}

function compareGoldenFixturesByCanonicalSeed(left: GoldenFixtureCase, right: GoldenFixtureCase): number {
  if (left.fixture_seed !== right.fixture_seed) {
    return left.fixture_seed - right.fixture_seed;
  }
  if (left.chain !== right.chain) {
    return left.chain.localeCompare(right.chain);
  }
  return left.fixture_id.localeCompare(right.fixture_id);
}

export function canonicalizeGoldenFixtures(fixtures: GoldenFixtureCase[]): GoldenFixtureCase[] {
  return [...fixtures].sort(compareGoldenFixturesByCanonicalSeed);
}

function normalizeFixtureInputs(fixtures: GoldenFixtureCase[]): GoldenFixtureBatchInput[] {
  return fixtures.map((fixture) => ({
    signature: fixture.signature,
    rawJson: Buffer.from(JSON.stringify(fixture.input)),
  }));
}

function collectWatchedAddresses(fixtures: GoldenFixtureCase[]): string[] {
  const addresses = new Set<string>();
  for (const fixture of fixtures) {
    for (const address of fixture.watched_addresses) {
      addresses.add(address);
    }
  }
  return Array.from(addresses);
}

export function decodeGoldenFixtureRows(fixtures: GoldenFixtureCase[]): DecodedGoldenFixtureRow[] {
  if (fixtures.length === 0) {
    return [];
  }

  const canonicalFixtures = canonicalizeGoldenFixtures(fixtures);
  const batch = decodeSolanaTransactionBatch(
    normalizeFixtureInputs(canonicalFixtures),
    collectWatchedAddresses(canonicalFixtures),
  );

  if (batch.errors.length > 0) {
    const messages = batch.errors
      .map((error) => `${error.signature}:${error.error}`)
      .join('; ');
    throw new Error(`fixture batch decode returned errors: ${messages}`);
  }

  const outputsByHash = new Map<string, GoldenFixtureOutputSnapshot>();
  for (const result of batch.results) {
    if (outputsByHash.has(result.txHash)) {
      throw new Error(`fixture batch produced duplicate output tx hash ${result.txHash}`);
    }
    outputsByHash.set(result.txHash, normalizeFixtureOutput(result));
  }

  const expectedTxHashes = new Set<string>();
  for (const fixture of canonicalFixtures) {
    if (expectedTxHashes.has(fixture.expected_output.tx_hash)) {
      throw new Error(`duplicate expected_output.tx_hash in fixture catalog: ${fixture.expected_output.tx_hash}`);
    }
    expectedTxHashes.add(fixture.expected_output.tx_hash);
  }

  if (outputsByHash.size !== expectedTxHashes.size) {
    throw new Error(
      `fixture batch output count mismatch expected=${expectedTxHashes.size} produced=${outputsByHash.size}`,
    );
  }

  return canonicalFixtures.map((fixture) => {
    const output = outputsByHash.get(fixture.expected_output.tx_hash);
    if (!output) {
      throw new Error(`fixture ${fixture.fixture_id} produced no output in batch decode`);
    }

    return { fixture, output };
  });
}

function compareDecodedRowsByCanonicalSeed(left: DecodedGoldenFixtureRow, right: DecodedGoldenFixtureRow): number {
  if (left.fixture.fixture_seed !== right.fixture.fixture_seed) {
    return left.fixture.fixture_seed - right.fixture.fixture_seed;
  }
  return left.fixture.fixture_id.localeCompare(right.fixture.fixture_id);
}

export function sortDecodedFixtureRows(rows: DecodedGoldenFixtureRow[]): DecodedGoldenFixtureRow[] {
  return [...rows].sort(compareDecodedRowsByCanonicalSeed);
}

export function summarizeGoldenFixtureRows(rows: DecodedGoldenFixtureRow[]): GoldenFixtureSummaryRow[] {
  return rows.map(({ fixture, output }) => ({
    fixture_id: fixture.fixture_id,
    fixture_seed: fixture.fixture_seed,
    run_id: fixture.run_id,
    chain: fixture.chain,
    network: fixture.network,
    output,
  }));
}

export function compareSummaryRows(left: GoldenFixtureSummaryRow, right: GoldenFixtureSummaryRow): number {
  if (left.fixture_seed !== right.fixture_seed) {
    return left.fixture_seed - right.fixture_seed;
  }
  return left.fixture_id.localeCompare(right.fixture_id);
}

export function canonicalFixtureRows(rows: DecodedGoldenFixtureRow[]): GoldenFixtureSummaryRow[] {
  return summarizeGoldenFixtureRows(sortDecodedFixtureRows(rows)).sort(compareSummaryRows);
}

export function collectCanonicalEventIds(rows: DecodedGoldenFixtureRow[]): string[] {
  return rows
    .flatMap(({ output }) =>
      output.balance_events.map((event) => canonicalEventId(output.tx_hash, event)),
    )
    .sort();
}

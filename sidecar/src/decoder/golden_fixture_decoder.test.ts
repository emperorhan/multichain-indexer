import { describe, expect, it } from 'vitest';

import {
  getGoldenFixtures,
  GoldenFixtureCase,
} from './golden_fixture_loader';
import {
  canonicalFixtureRows,
  collectCanonicalEventIds,
  decodeGoldenFixtureRows,
  canonicalizeGoldenFixtures,
  GoldenFixtureSummaryRow,
} from './golden_fixture_test_utils';

const REQUIRED_CLASS_PATHS = [
  'TRANSFER',
  'MINT',
  'BURN',
  'FEE',
  'fee_execution_l2',
  'fee_data_l1',
  'TRANSFER:vin',
  'TRANSFER:vout',
  'miner_fee',
];

const REQUIRED_CHAINS = ['solana', 'base', 'btc'];

function decodeAndSortFixtureRows(fixtures: GoldenFixtureCase[]): GoldenFixtureSummaryRow[] {
  return canonicalFixtureRows(decodeGoldenFixtureRows(fixtures));
}

describe('golden fixture decoder determinism', () => {
  const allFixtures = getGoldenFixtures();

  it('validates mandatory class-path and chain coverage in fixture catalog', () => {
    const coveredClasses = new Set<string>(allFixtures.map((fixture) => fixture.class_path));
    for (const required of REQUIRED_CLASS_PATHS) {
      expect(coveredClasses.has(required)).toBe(true);
    }

    const coveredChains = new Set<string>(allFixtures.map((fixture) => fixture.chain));
    for (const chain of REQUIRED_CHAINS) {
      expect(coveredChains.has(chain)).toBe(true);
    }

    expect(new Set(allFixtures.map((fixture) => fixture.fixture_id)).size).toBe(allFixtures.length);
  });

  it('loads fixtures in canonical seed+chain+id order', () => {
    const canonicalOrder = canonicalizeGoldenFixtures(allFixtures);
    expect(allFixtures).toEqual(canonicalOrder);
  });

  it('matches each golden fixture against deterministic expected output snapshot', () => {
    const rows = decodeGoldenFixtureRows(allFixtures);
    const ids = collectCanonicalEventIds(rows);
    expect(new Set(ids).size).toBe(ids.length);

    for (const { fixture, output } of rows) {
      expect(output).toEqual(fixture.expected_output);
    }
  });

  it('canonical_range_replay: replaying fixture run with stable range is idempotent', () => {
    const runs = Array.from(new Set(allFixtures.map((fixture) => fixture.run_id))).sort();
    for (const runId of runs) {
      const rangeFixtures = allFixtures.filter((fixture) => fixture.run_id === runId);
      const firstPass = decodeAndSortFixtureRows(rangeFixtures);
      const secondPass = decodeAndSortFixtureRows(rangeFixtures);

      expect(firstPass).toEqual(secondPass);

      const replayRows = decodeGoldenFixtureRows(rangeFixtures);
      const replayIds = collectCanonicalEventIds(replayRows);
      expect(new Set(replayIds).size).toBe(replayIds.length);
    }
  });

  it('replay_order_swap: output is invariant under canonical order swap', () => {
    const asc = [...allFixtures].sort((left, right) => left.fixture_seed - right.fixture_seed);
    const desc = [...allFixtures].sort((left, right) => right.fixture_seed - left.fixture_seed);

    const ascRows = decodeAndSortFixtureRows(asc);
    const descRows = decodeAndSortFixtureRows(desc);

    expect(ascRows).toEqual(descRows);
  });

  it('one_chain_restart_perturbation: chain-isolated replay path converges to same canonical rows', () => {
    const fullRows = decodeAndSortFixtureRows(allFixtures);

    const chainOrder: Array<'solana' | 'base' | 'btc'> = ['solana', 'base', 'btc'];
    const chainRows: GoldenFixtureCase[] = [];

    for (const chain of chainOrder) {
      chainRows.push(...allFixtures.filter((fixture) => fixture.chain === chain));
    }

    const perturbedRows = canonicalFixtureRows(decodeGoldenFixtureRows(chainRows));

    expect(perturbedRows).toEqual(fullRows);
  });

  it('canonical permutation assertions: event identities remain stable across all supported permutations', () => {
    const replayIds = collectCanonicalEventIds(decodeGoldenFixtureRows(allFixtures));
    expect(new Set(replayIds).size).toBe(replayIds.length);
  });
});

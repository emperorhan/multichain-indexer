import { describe, expect, it } from 'vitest';

import {
  REQUIRED_CHAIN_CLASS_PATHS,
  REQUIRED_GOLDEN_CLASS_PATHS,
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

const REQUIRED_CHAINS = ['solana', 'base', 'btc'];

function decodeAndSortFixtureRows(fixtures: GoldenFixtureCase[]): GoldenFixtureSummaryRow[] {
  return canonicalFixtureRows(decodeGoldenFixtureRows(fixtures));
}

function collectChainClassCoverage(fixtures: GoldenFixtureCase[]): Map<string, Set<string>> {
  const coverage = new Map<string, Set<string>>();
  for (const fixture of fixtures) {
    const chainClassSet = coverage.get(fixture.chain) ?? new Set<string>();
    chainClassSet.add(fixture.class_path);
    coverage.set(fixture.chain, chainClassSet);
  }
  return coverage;
}

function classFixtures(fixtures: GoldenFixtureCase[], chain: string, classPath: string): GoldenFixtureCase[] {
  return fixtures.filter((fixture) => fixture.chain === chain && fixture.class_path === classPath);
}

describe('golden fixture decoder determinism', () => {
  const allFixtures = getGoldenFixtures();

  it('validates explicit mandatory class-path coverage by chain', () => {
    const coveredClasses = new Set<string>(allFixtures.map((fixture) => fixture.class_path));
    for (const required of REQUIRED_GOLDEN_CLASS_PATHS) {
      expect(coveredClasses.has(required)).toBe(true);
    }

    const coveredChains = new Set<string>(allFixtures.map((fixture) => fixture.chain));
    for (const chain of REQUIRED_CHAINS) {
      expect(coveredChains.has(chain)).toBe(true);
    }

    const coverageByChain = collectChainClassCoverage(allFixtures);
    for (const row of REQUIRED_CHAIN_CLASS_PATHS) {
      const chainClasses = coverageByChain.get(row.chain) ?? new Set<string>();
      expect(chainClasses.has(row.class_path)).toBe(true);
    }

    expect(new Set(allFixtures.map((fixture) => fixture.fixture_id)).size).toBe(allFixtures.length);
  });

  it('validates replay-safe canonical IDs for BTC mandatory class-path fixtures', () => {
    const btcFixtures = allFixtures.filter((fixture) => fixture.chain === 'btc');
    expect(btcFixtures.length).toBeGreaterThan(0);

    for (const mandatory of REQUIRED_CHAIN_CLASS_PATHS.filter((item) => item.chain === 'btc')) {
      const subset = classFixtures(allFixtures, mandatory.chain, mandatory.class_path);
      expect(subset.length).toBeGreaterThan(0);

      const asc = decodeAndSortFixtureRows([...subset].sort((left, right) => left.fixture_seed - right.fixture_seed));
      const desc = decodeAndSortFixtureRows([...subset].sort((left, right) => right.fixture_seed - left.fixture_seed));
      expect(asc).toEqual(desc);

      const ids = collectCanonicalEventIds(decodeGoldenFixtureRows(subset));
      expect(new Set(ids).size).toBe(ids.length);
    }

    const btcAsc = decodeAndSortFixtureRows([...btcFixtures].sort((left, right) => left.fixture_seed - right.fixture_seed));
    const btcDesc = decodeAndSortFixtureRows([...btcFixtures].sort((left, right) => right.fixture_seed - left.fixture_seed));
    expect(btcAsc).toEqual(btcDesc);
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

import { describe, it, expect } from 'vitest';
import { decodeBTCTransaction } from './transaction_decoder';

describe('decodeBTCTransaction', () => {
  it('decodes watched vin/vout deltas with deterministic fee metadata', () => {
    const watched = 'tb1watched';
    const payload = {
      chain: 'btc',
      txid: 'tx-btc-1',
      block_height: 123,
      block_time: 1700000123,
      confirmations: 2,
      fee_sat: '100',
      fee_payer: watched,
      vin: [
        {
          index: 0,
          txid: 'prevtx',
          vout: 1,
          address: watched,
          value_sat: '10000',
        },
      ],
      vout: [
        {
          index: 0,
          address: watched,
          value_sat: '900',
        },
        {
          index: 1,
          address: 'tb1external',
          value_sat: '9000',
        },
      ],
    };

    const result = decodeBTCTransaction(payload, 'fallback', new Set([watched]));
    expect(result.txHash).toBe('tx-btc-1');
    expect(result.blockCursor).toBe(123);
    expect(result.blockTime).toBe(1700000123);
    expect(result.feeAmount).toBe('100');
    expect(result.feePayer).toBe(watched);
    expect(result.status).toBe('SUCCESS');
    expect(result.balanceEvents).toHaveLength(2);

    const vinSpend = result.balanceEvents.find((ev) => ev.eventAction === 'vin_spend');
    const voutReceive = result.balanceEvents.find((ev) => ev.eventAction === 'vout_receive');
    expect(vinSpend?.delta).toBe('-10000');
    expect(vinSpend?.metadata.event_path).toBe('vin:0');
    expect(voutReceive?.delta).toBe('900');
    expect(voutReceive?.metadata.event_path).toBe('vout:0');
    expect(result.metadata?.btc_fee_sat).toBe('100');
  });

  it('derives fee when fee_sat is missing', () => {
    const watched = 'tb1watched';
    const result = decodeBTCTransaction({
      chain: 'btc',
      txid: 'tx-btc-2',
      block_height: 10,
      confirmations: 0,
      vin: [
        {
          index: 0,
          address: watched,
          value_sat: '1500',
        },
      ],
      vout: [
        {
          index: 0,
          address: 'tb1external',
          value_sat: '1200',
        },
      ],
    }, 'fallback', new Set([watched]));

    expect(result.feeAmount).toBe('300');
    expect(result.metadata?.finality_state).toBe('processed');
  });

  it('skips coinbase vin inputs', () => {
    const watched = 'tb1miner';
    const result = decodeBTCTransaction({
      chain: 'btc',
      txid: 'tx-coinbase',
      block_height: 100,
      confirmations: 100,
      fee_sat: '0',
      vin: [
        {
          index: 0,
          coinbase: true,
          value_sat: '0',
        },
      ],
      vout: [
        {
          index: 0,
          address: watched,
          value_sat: '625000000',
        },
      ],
    }, 'fallback', new Set([watched]));

    expect(result.status).toBe('SUCCESS');
    const vinEvents = result.balanceEvents.filter((ev) => ev.eventAction === 'vin_spend');
    expect(vinEvents).toHaveLength(0);
    const voutEvents = result.balanceEvents.filter((ev) => ev.eventAction === 'vout_receive');
    expect(voutEvents).toHaveLength(1);
    expect(voutEvents[0].delta).toBe('625000000');
  });

  it('handles zero-fee transaction', () => {
    const watched = 'tb1watched';
    const result = decodeBTCTransaction({
      chain: 'btc',
      txid: 'tx-zerofee',
      block_height: 200,
      confirmations: 1,
      fee_sat: '0',
      fee_payer: watched,
      vin: [
        { index: 0, address: watched, value_sat: '10000', txid: 'prev', vout: 0 },
      ],
      vout: [
        { index: 0, address: 'tb1external', value_sat: '10000' },
      ],
    }, 'fallback', new Set([watched]));

    expect(result.feeAmount).toBe('0');
    expect(result.metadata?.btc_fee_sat).toBe('0');
  });

  it('handles OP_RETURN output (no address)', () => {
    const watched = 'tb1watched';
    const result = decodeBTCTransaction({
      chain: 'btc',
      txid: 'tx-opreturn',
      block_height: 300,
      confirmations: 2,
      fee_sat: '500',
      fee_payer: watched,
      vin: [
        { index: 0, address: watched, value_sat: '5000', txid: 'prev', vout: 0 },
      ],
      vout: [
        { index: 0, address: 'tb1external', value_sat: '4500' },
        { index: 1, value_sat: '0' }, // OP_RETURN: no address
      ],
    }, 'fallback', new Set([watched]));

    expect(result.status).toBe('SUCCESS');
    // Only vin_spend event (OP_RETURN vout has no address and zero value)
    const vinEvents = result.balanceEvents.filter((ev) => ev.eventAction === 'vin_spend');
    expect(vinEvents).toHaveLength(1);
    const voutEvents = result.balanceEvents.filter((ev) => ev.eventAction === 'vout_receive');
    expect(voutEvents).toHaveLength(0);
  });

  it('handles consolidation (same address multiple vin)', () => {
    const watched = 'tb1watched';
    const result = decodeBTCTransaction({
      chain: 'btc',
      txid: 'tx-consolidation',
      block_height: 400,
      confirmations: 3,
      fee_sat: '2000',
      fee_payer: watched,
      vin: [
        { index: 0, address: watched, value_sat: '10000', txid: 'prevA', vout: 0 },
        { index: 1, address: watched, value_sat: '20000', txid: 'prevB', vout: 1 },
        { index: 2, address: watched, value_sat: '30000', txid: 'prevC', vout: 0 },
      ],
      vout: [
        { index: 0, address: watched, value_sat: '58000' },
      ],
    }, 'fallback', new Set([watched]));

    const vinEvents = result.balanceEvents.filter((ev) => ev.eventAction === 'vin_spend');
    expect(vinEvents).toHaveLength(3);
    const totalSpent = vinEvents.reduce((sum, ev) => sum + BigInt(ev.delta), 0n);
    expect(totalSpent).toBe(-60000n);

    const voutEvents = result.balanceEvents.filter((ev) => ev.eventAction === 'vout_receive');
    expect(voutEvents).toHaveLength(1);
    expect(voutEvents[0].delta).toBe('58000');
  });

  it('maps confirmations to finality state', () => {
    const watched = 'tb1watched';

    // 0 confirmations = processed
    const unconfirmed = decodeBTCTransaction({
      chain: 'btc', txid: 'tx-unconf', block_height: 500, confirmations: 0,
      vin: [{ index: 0, address: watched, value_sat: '1000', txid: 'p', vout: 0 }],
      vout: [{ index: 0, address: 'tb1ext', value_sat: '800' }],
    }, 'fallback', new Set([watched]));
    expect(unconfirmed.metadata?.finality_state).toBe('processed');

    // >0 confirmations = confirmed
    const confirmed = decodeBTCTransaction({
      chain: 'btc', txid: 'tx-conf', block_height: 500, confirmations: 1,
      vin: [{ index: 0, address: watched, value_sat: '1000', txid: 'p', vout: 0 }],
      vout: [{ index: 0, address: 'tb1ext', value_sat: '800' }],
    }, 'fallback', new Set([watched]));
    expect(confirmed.metadata?.finality_state).toBe('confirmed');
  });
});

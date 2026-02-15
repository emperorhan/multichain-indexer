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
});

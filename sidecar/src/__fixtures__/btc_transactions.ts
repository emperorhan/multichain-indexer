/**
 * Test fixtures for BTC transaction decoding.
 */

export const WATCHED_ADDRESS = 'tb1qwatched111111111111111111111111';
export const EXTERNAL_ADDRESS = 'tb1qexternal22222222222222222222222';

export const standardSpendTx = {
  chain: 'btc',
  txid: 'btc-standard-spend-001',
  block_height: 800000,
  block_hash: '00000000000000000002a7c4c1e48d76c5a37902165a270156b7a8d72f8804b2',
  block_time: 1700100000,
  confirmations: 6,
  fee_sat: '1500',
  fee_payer: WATCHED_ADDRESS,
  vin: [
    {
      index: 0,
      txid: 'prev-tx-001',
      vout: 0,
      address: WATCHED_ADDRESS,
      value_sat: '50000',
    },
  ],
  vout: [
    {
      index: 0,
      address: EXTERNAL_ADDRESS,
      value_sat: '30000',
    },
    {
      index: 1,
      address: WATCHED_ADDRESS,
      value_sat: '18500',
    },
  ],
};

export const coinbaseTx = {
  chain: 'btc',
  txid: 'btc-coinbase-001',
  block_height: 800001,
  block_time: 1700100600,
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
      address: WATCHED_ADDRESS,
      value_sat: '625000000',
    },
  ],
};

export const consolidationTx = {
  chain: 'btc',
  txid: 'btc-consolidation-001',
  block_height: 800002,
  block_time: 1700101200,
  confirmations: 3,
  fee_sat: '2000',
  fee_payer: WATCHED_ADDRESS,
  vin: [
    {
      index: 0,
      txid: 'prev-tx-a',
      vout: 0,
      address: WATCHED_ADDRESS,
      value_sat: '10000',
    },
    {
      index: 1,
      txid: 'prev-tx-b',
      vout: 1,
      address: WATCHED_ADDRESS,
      value_sat: '20000',
    },
    {
      index: 2,
      txid: 'prev-tx-c',
      vout: 0,
      address: WATCHED_ADDRESS,
      value_sat: '30000',
    },
  ],
  vout: [
    {
      index: 0,
      address: WATCHED_ADDRESS,
      value_sat: '58000',
    },
  ],
};

export const zeroFeeTx = {
  chain: 'btc',
  txid: 'btc-zerofee-001',
  block_height: 800003,
  block_time: 1700101800,
  confirmations: 1,
  fee_sat: '0',
  fee_payer: WATCHED_ADDRESS,
  vin: [
    {
      index: 0,
      txid: 'prev-tx-002',
      vout: 0,
      address: WATCHED_ADDRESS,
      value_sat: '10000',
    },
  ],
  vout: [
    {
      index: 0,
      address: EXTERNAL_ADDRESS,
      value_sat: '10000',
    },
  ],
};

export const opReturnTx = {
  chain: 'btc',
  txid: 'btc-opreturn-001',
  block_height: 800004,
  block_time: 1700102400,
  confirmations: 2,
  fee_sat: '500',
  fee_payer: WATCHED_ADDRESS,
  vin: [
    {
      index: 0,
      txid: 'prev-tx-003',
      vout: 0,
      address: WATCHED_ADDRESS,
      value_sat: '5000',
    },
  ],
  vout: [
    {
      index: 0,
      address: EXTERNAL_ADDRESS,
      value_sat: '4500',
    },
    {
      index: 1,
      // OP_RETURN output: no address
      value_sat: '0',
    },
  ],
};

export const noConfirmationsTx = {
  chain: 'btc',
  txid: 'btc-noconf-001',
  block_height: 800005,
  block_time: 1700103000,
  confirmations: 0,
  fee_sat: '1000',
  fee_payer: WATCHED_ADDRESS,
  vin: [
    {
      index: 0,
      txid: 'prev-tx-004',
      vout: 0,
      address: WATCHED_ADDRESS,
      value_sat: '20000',
    },
  ],
  vout: [
    {
      index: 0,
      address: EXTERNAL_ADDRESS,
      value_sat: '19000',
    },
  ],
};

export const highConfirmationsTx = {
  chain: 'btc',
  txid: 'btc-highconf-001',
  block_height: 799000,
  block_time: 1700000000,
  confirmations: 1000,
  fee_sat: '800',
  fee_payer: WATCHED_ADDRESS,
  vin: [
    {
      index: 0,
      txid: 'prev-tx-005',
      vout: 0,
      address: WATCHED_ADDRESS,
      value_sat: '15000',
    },
  ],
  vout: [
    {
      index: 0,
      address: EXTERNAL_ADDRESS,
      value_sat: '14200',
    },
  ],
};

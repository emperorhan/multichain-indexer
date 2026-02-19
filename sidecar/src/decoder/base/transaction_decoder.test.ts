import { describe, it, expect } from 'vitest';
import { decodeBaseTransaction } from './transaction_decoder';
import { erc20MintTx, erc20BurnTx } from '../../__fixtures__/base_transactions';

const WATCHED_ADDRESS = '0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
const COUNTERPARTY = '0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb';
const TOKEN_CONTRACT = '0xcccccccccccccccccccccccccccccccccccccccc';
const TRANSFER_TOPIC = '0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55aebf9f4f7b8';

function padTopic(address: string): string {
  return `0x${'0'.repeat(24)}${address.toLowerCase().replace(/^0x/, '')}`;
}

describe('decodeBaseTransaction', () => {
  it('decodes base payload and emits native/erc20 events', () => {
    const payload = {
      chain: 'base',
      tx: {
        hash: '0xtx1',
        blockNumber: '0x10',
        transactionIndex: '0x2',
        from: WATCHED_ADDRESS,
        to: COUNTERPARTY,
        value: '0x5',
        gasPrice: '0x10',
      },
      receipt: {
        transactionHash: '0xtx1',
        blockNumber: '0x10',
        transactionIndex: '0x2',
        status: '0x1',
        from: WATCHED_ADDRESS,
        to: COUNTERPARTY,
        gasUsed: '0x10',
        effectiveGasPrice: '0x10',
        l1Fee: '0x4',
        logs: [
          {
            address: TOKEN_CONTRACT,
            topics: [TRANSFER_TOPIC, padTopic(WATCHED_ADDRESS), padTopic(COUNTERPARTY)],
            data: '0xa',
            logIndex: '0x7',
          },
        ],
      },
    };

    const result = decodeBaseTransaction(payload, 'fallback-sig', new Set([WATCHED_ADDRESS]));

    expect(result.txHash).toBe('0xtx1');
    expect(result.blockCursor).toBe(16);
    expect(result.status).toBe('SUCCESS');
    expect(result.feePayer).toBe(WATCHED_ADDRESS);
    // (gasUsed 16 * gasPrice 16) + l1Fee 4 = 260
    expect(result.feeAmount).toBe('260');
    expect(result.metadata?.fee_execution_l2).toBe('256');
    expect(result.metadata?.fee_data_l1).toBe('4');

    const nativeTransfer = result.balanceEvents.find((ev) => ev.eventAction === 'native_transfer');
    expect(nativeTransfer).toBeDefined();
    expect(nativeTransfer?.delta).toBe('-5');
    expect(nativeTransfer?.address).toBe(WATCHED_ADDRESS);

    const erc20Transfer = result.balanceEvents.find((ev) => ev.eventAction === 'erc20_transfer');
    expect(erc20Transfer).toBeDefined();
    expect(erc20Transfer?.delta).toBe('-10');
    expect(erc20Transfer?.contractAddress).toBe(TOKEN_CONTRACT);
    expect(erc20Transfer?.metadata.event_path).toBe('log:7');
  });

  it('classifies ERC20 zero-from transfer as MINT class', () => {
    const result = decodeBaseTransaction(
      erc20MintTx,
      erc20MintTx.tx.hash,
      new Set([WATCHED_ADDRESS]),
    );

    const mintEvents = result.balanceEvents.filter((ev) => ev.eventCategory === 'MINT');
    expect(mintEvents).toHaveLength(1);
    expect(mintEvents[0].eventAction).toBe('erc20_mint');
    expect(mintEvents[0].address).toBe(WATCHED_ADDRESS);
    expect(mintEvents[0].contractAddress).toBe(TOKEN_CONTRACT);
    expect(mintEvents[0].delta).toBe('5000000');
    expect(mintEvents[0].metadata.event_path).toBe('log:6');
    expect(result.metadata?.fee_execution_l2).toBe('2000000000000');
    expect(result.metadata?.fee_data_l1).toBe('10000');
  });

  it('classifies ERC20 zero-to transfer as BURN class', () => {
    const result = decodeBaseTransaction(
      erc20BurnTx,
      erc20BurnTx.tx.hash,
      new Set([WATCHED_ADDRESS]),
    );

    const burnEvents = result.balanceEvents.filter((ev) => ev.eventCategory === 'BURN');
    expect(burnEvents).toHaveLength(1);
    expect(burnEvents[0].eventAction).toBe('erc20_burn');
    expect(burnEvents[0].address).toBe(WATCHED_ADDRESS);
    expect(burnEvents[0].contractAddress).toBe(TOKEN_CONTRACT);
    expect(burnEvents[0].delta).toBe('-3000000');
    expect(burnEvents[0].metadata.event_path).toBe('log:7');
    expect(result.metadata?.fee_execution_l2).toBe('2000000000000');
    expect(result.metadata?.fee_data_l1).toBe('5000');
  });

  it('returns failed status when receipt status is zero', () => {
    const result = decodeBaseTransaction({
      chain: 'base',
      tx: {
        hash: '0xfail',
        from: WATCHED_ADDRESS,
      },
      receipt: {
        transactionHash: '0xfail',
        status: '0x0',
      },
    }, 'fallback', new Set([WATCHED_ADDRESS]));

    expect(result.status).toBe('FAILED');
    expect(result.error).toBeDefined();
  });

  it('skips removed logs', () => {
    const payload = {
      chain: 'base',
      tx: {
        hash: '0xremoved',
        blockNumber: '0x10',
        transactionIndex: '0x0',
        from: WATCHED_ADDRESS,
        to: TOKEN_CONTRACT,
        value: '0x0',
      },
      receipt: {
        transactionHash: '0xremoved',
        blockNumber: '0x10',
        transactionIndex: '0x0',
        status: '0x1',
        from: WATCHED_ADDRESS,
        gasUsed: '0x10',
        effectiveGasPrice: '0x10',
        logs: [
          {
            address: TOKEN_CONTRACT,
            topics: [TRANSFER_TOPIC, padTopic(WATCHED_ADDRESS), padTopic(COUNTERPARTY)],
            data: '0xa',
            logIndex: '0x0',
            removed: true,
          },
        ],
      },
    };

    const result = decodeBaseTransaction(payload, 'fallback', new Set([WATCHED_ADDRESS]));
    const erc20Events = result.balanceEvents.filter((ev) => ev.eventAction === 'erc20_transfer');
    expect(erc20Events).toHaveLength(0);
  });

  it('skips logs with incomplete topics (< 3)', () => {
    const payload = {
      chain: 'base',
      tx: {
        hash: '0xshort',
        blockNumber: '0x10',
        transactionIndex: '0x0',
        from: WATCHED_ADDRESS,
        to: TOKEN_CONTRACT,
        value: '0x0',
      },
      receipt: {
        transactionHash: '0xshort',
        blockNumber: '0x10',
        transactionIndex: '0x0',
        status: '0x1',
        from: WATCHED_ADDRESS,
        gasUsed: '0x10',
        effectiveGasPrice: '0x10',
        logs: [
          {
            address: TOKEN_CONTRACT,
            topics: [TRANSFER_TOPIC, padTopic(WATCHED_ADDRESS)], // only 2 topics
            data: '0xa',
            logIndex: '0x0',
          },
        ],
      },
    };

    const result = decodeBaseTransaction(payload, 'fallback', new Set([WATCHED_ADDRESS]));
    const erc20Events = result.balanceEvents.filter((ev) => ev.eventAction === 'erc20_transfer');
    expect(erc20Events).toHaveLength(0);
  });

  it('handles missing l1Fee (pre-Bedrock)', () => {
    const payload = {
      chain: 'base',
      tx: {
        hash: '0xnol1',
        blockNumber: '0x10',
        transactionIndex: '0x0',
        from: WATCHED_ADDRESS,
        to: COUNTERPARTY,
        value: '0x1',
        gasPrice: '0x3b9aca00',
      },
      receipt: {
        transactionHash: '0xnol1',
        blockNumber: '0x10',
        transactionIndex: '0x0',
        status: '0x1',
        from: WATCHED_ADDRESS,
        gasUsed: '0x5208',
        effectiveGasPrice: '0x3b9aca00',
        // no l1Fee field
        logs: [],
      },
    };

    const result = decodeBaseTransaction(payload, 'fallback', new Set([WATCHED_ADDRESS]));
    expect(result.metadata?.fee_data_l1).toBeUndefined();
    expect(result.metadata?.fee_execution_l2).toBeDefined();
    // Total fee should be gasUsed * effectiveGasPrice (no l1 component)
    const gasUsed = 0x5208;
    const gasPrice = 0x3b9aca00;
    expect(result.feeAmount).toBe(String(BigInt(gasUsed) * BigInt(gasPrice)));
  });

  it('uses L1 metadata keys when evm_layer is l1', () => {
    const payload = {
      chain: 'ethereum',
      evm_layer: 'l1',
      tx: {
        hash: '0xeth1',
        blockNumber: '0x10',
        transactionIndex: '0x2',
        from: WATCHED_ADDRESS,
        to: COUNTERPARTY,
        value: '0x5',
        gasPrice: '0x10',
      },
      receipt: {
        transactionHash: '0xeth1',
        blockNumber: '0x10',
        transactionIndex: '0x2',
        status: '0x1',
        from: WATCHED_ADDRESS,
        to: COUNTERPARTY,
        gasUsed: '0x10',
        effectiveGasPrice: '0x10',
        logs: [],
      },
    };

    const result = decodeBaseTransaction(payload, 'fallback-sig', new Set([WATCHED_ADDRESS]));

    expect(result.txHash).toBe('0xeth1');
    expect(result.status).toBe('SUCCESS');
    // L1: gasUsed 16 * gasPrice 16 = 256 (no l1Fee component)
    expect(result.feeAmount).toBe('256');

    // L1 metadata keys
    expect(result.metadata?.gas_used).toBe('16');
    expect(result.metadata?.effective_gas_price).toBe('16');

    // L2-specific keys should NOT be present
    expect(result.metadata?.fee_execution_l2).toBeUndefined();
    expect(result.metadata?.base_gas_used).toBeUndefined();
    expect(result.metadata?.base_effective_gas_price).toBeUndefined();
    expect(result.metadata?.fee_data_l1).toBeUndefined();
  });

  it('uses L2 metadata keys when evm_layer is l2', () => {
    const payload = {
      chain: 'base',
      evm_layer: 'l2',
      tx: {
        hash: '0xbase1',
        blockNumber: '0x10',
        transactionIndex: '0x2',
        from: WATCHED_ADDRESS,
        to: COUNTERPARTY,
        value: '0x5',
        gasPrice: '0x10',
      },
      receipt: {
        transactionHash: '0xbase1',
        blockNumber: '0x10',
        transactionIndex: '0x2',
        status: '0x1',
        from: WATCHED_ADDRESS,
        to: COUNTERPARTY,
        gasUsed: '0x10',
        effectiveGasPrice: '0x10',
        l1Fee: '0x4',
        logs: [],
      },
    };

    const result = decodeBaseTransaction(payload, 'fallback-sig', new Set([WATCHED_ADDRESS]));

    // L2 metadata keys
    expect(result.metadata?.fee_execution_l2).toBe('256');
    expect(result.metadata?.base_gas_used).toBe('16');
    expect(result.metadata?.base_effective_gas_price).toBe('16');
    expect(result.metadata?.fee_data_l1).toBe('4');

    // L1-specific keys should NOT be present
    expect(result.metadata?.gas_used).toBeUndefined();
    expect(result.metadata?.effective_gas_price).toBeUndefined();
  });

  it('does not include fee_data_l1 for L1 even if l1Fee is present', () => {
    const payload = {
      chain: 'ethereum',
      evm_layer: 'l1',
      tx: {
        hash: '0xeth2',
        blockNumber: '0x10',
        transactionIndex: '0x0',
        from: WATCHED_ADDRESS,
        to: COUNTERPARTY,
        value: '0x0',
      },
      receipt: {
        transactionHash: '0xeth2',
        blockNumber: '0x10',
        transactionIndex: '0x0',
        status: '0x1',
        from: WATCHED_ADDRESS,
        gasUsed: '0x5208',
        effectiveGasPrice: '0x3b9aca00',
        l1Fee: '0x100', // should be ignored for L1
        logs: [],
      },
    };

    const result = decodeBaseTransaction(payload, 'fallback', new Set([WATCHED_ADDRESS]));
    expect(result.metadata?.fee_data_l1).toBeUndefined();
    expect(result.metadata?.gas_used).toBeDefined();
    expect(result.metadata?.effective_gas_price).toBeDefined();
  });

  it('emits events for both sender and receiver when both are watched', () => {
    const payload = {
      chain: 'base',
      tx: {
        hash: '0xbothwatched',
        blockNumber: '0x10',
        transactionIndex: '0x0',
        from: WATCHED_ADDRESS,
        to: COUNTERPARTY,
        value: '0x5',
        gasPrice: '0x1',
      },
      receipt: {
        transactionHash: '0xbothwatched',
        blockNumber: '0x10',
        transactionIndex: '0x0',
        status: '0x1',
        from: WATCHED_ADDRESS,
        to: COUNTERPARTY,
        gasUsed: '0x1',
        effectiveGasPrice: '0x1',
        logs: [],
      },
    };

    const bothWatched = new Set([WATCHED_ADDRESS, COUNTERPARTY]);
    const result = decodeBaseTransaction(payload, 'fallback', bothWatched);
    const nativeEvents = result.balanceEvents.filter((ev) => ev.eventAction === 'native_transfer');
    expect(nativeEvents).toHaveLength(2);

    const senderEvent = nativeEvents.find((ev) => ev.address === WATCHED_ADDRESS);
    const receiverEvent = nativeEvents.find((ev) => ev.address === COUNTERPARTY);
    expect(senderEvent?.delta).toBe('-5');
    expect(receiverEvent?.delta).toBe('5');
  });
});

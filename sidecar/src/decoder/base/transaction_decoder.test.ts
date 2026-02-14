import { describe, it, expect } from 'vitest';
import { decodeBaseTransaction } from './transaction_decoder';

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
});

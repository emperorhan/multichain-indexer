/**
 * Test fixtures for Base (EVM) transaction decoding.
 */

export const WATCHED_ADDRESS = '0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
export const COUNTERPARTY = '0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb';
export const TOKEN_CONTRACT = '0xcccccccccccccccccccccccccccccccccccccccc';
export const TRANSFER_TOPIC = '0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55aebf9f4f7b8';
export const ZERO_ADDRESS = '0x0000000000000000000000000000000000000000';

function padTopic(address: string): string {
  return `0x${'0'.repeat(24)}${address.toLowerCase().replace(/^0x/, '')}`;
}

export const nativeTransferTx = {
  chain: 'base',
  tx: {
    hash: '0xnative1',
    blockNumber: '0x10',
    transactionIndex: '0x0',
    from: WATCHED_ADDRESS,
    to: COUNTERPARTY,
    value: '0xde0b6b3a7640000', // 1 ETH
    gasPrice: '0x3b9aca00', // 1 gwei
  },
  receipt: {
    transactionHash: '0xnative1',
    blockNumber: '0x10',
    transactionIndex: '0x0',
    status: '0x1',
    from: WATCHED_ADDRESS,
    to: COUNTERPARTY,
    gasUsed: '0x5208', // 21000
    effectiveGasPrice: '0x3b9aca00',
    l1Fee: '0x2710', // 10000
    logs: [],
  },
};

export const erc20TransferTx = {
  chain: 'base',
  tx: {
    hash: '0xerc20send',
    blockNumber: '0x20',
    transactionIndex: '0x1',
    from: WATCHED_ADDRESS,
    to: TOKEN_CONTRACT,
    value: '0x0',
    gasPrice: '0x3b9aca00',
  },
  receipt: {
    transactionHash: '0xerc20send',
    blockNumber: '0x20',
    transactionIndex: '0x1',
    status: '0x1',
    from: WATCHED_ADDRESS,
    to: TOKEN_CONTRACT,
    gasUsed: '0xea60', // 60000
    effectiveGasPrice: '0x3b9aca00',
    l1Fee: '0x1388', // 5000
    logs: [
      {
        address: TOKEN_CONTRACT,
        topics: [TRANSFER_TOPIC, padTopic(WATCHED_ADDRESS), padTopic(COUNTERPARTY)],
        data: '0x' + (1000000n).toString(16),
        logIndex: '0x3',
      },
    ],
  },
};

export const erc20ReceiveTx = {
  chain: 'base',
  tx: {
    hash: '0xerc20recv',
    blockNumber: '0x21',
    transactionIndex: '0x2',
    from: COUNTERPARTY,
    to: TOKEN_CONTRACT,
    value: '0x0',
    gasPrice: '0x3b9aca00',
  },
  receipt: {
    transactionHash: '0xerc20recv',
    blockNumber: '0x21',
    transactionIndex: '0x2',
    status: '0x1',
    from: COUNTERPARTY,
    to: TOKEN_CONTRACT,
    gasUsed: '0xea60',
    effectiveGasPrice: '0x3b9aca00',
    l1Fee: '0x1388',
    logs: [
      {
        address: TOKEN_CONTRACT,
        topics: [TRANSFER_TOPIC, padTopic(COUNTERPARTY), padTopic(WATCHED_ADDRESS)],
        data: '0x' + (2000000n).toString(16),
        logIndex: '0x5',
      },
    ],
  },
};

export const erc20MintTx = {
  chain: 'base',
  tx: {
    hash: '0xerc20mint',
    blockNumber: '0x80',
    transactionIndex: '0x4',
    from: ZERO_ADDRESS,
    to: TOKEN_CONTRACT,
    value: '0x0',
    gasPrice: '0x3b9aca00',
  },
  receipt: {
    transactionHash: '0xerc20mint',
    blockNumber: '0x80',
    transactionIndex: '0x4',
    status: '0x1',
    from: ZERO_ADDRESS,
    to: TOKEN_CONTRACT,
    gasUsed: '0x7d0',
    effectiveGasPrice: '0x3b9aca00',
    l1Fee: '0x2710',
    logs: [
      {
        address: TOKEN_CONTRACT,
        topics: [TRANSFER_TOPIC, padTopic(ZERO_ADDRESS), padTopic(WATCHED_ADDRESS)],
        data: '0x' + (5000000n).toString(16),
        logIndex: '0x6',
      },
    ],
  },
};

export const erc20BurnTx = {
  chain: 'base',
  tx: {
    hash: '0xerc20burn',
    blockNumber: '0x81',
    transactionIndex: '0x0',
    from: WATCHED_ADDRESS,
    to: COUNTERPARTY,
    value: '0x0',
    gasPrice: '0x3b9aca00',
  },
  receipt: {
    transactionHash: '0xerc20burn',
    blockNumber: '0x81',
    transactionIndex: '0x0',
    status: '0x1',
    from: WATCHED_ADDRESS,
    gasUsed: '0x7d0',
    effectiveGasPrice: '0x3b9aca00',
    l1Fee: '0x1388',
    logs: [
      {
        address: TOKEN_CONTRACT,
        topics: [TRANSFER_TOPIC, padTopic(WATCHED_ADDRESS), padTopic(ZERO_ADDRESS)],
        data: '0x' + (3000000n).toString(16),
        logIndex: '0x7',
      },
    ],
  },
};

export const failedTx = {
  chain: 'base',
  tx: {
    hash: '0xfailed1',
    blockNumber: '0x30',
    from: WATCHED_ADDRESS,
    to: COUNTERPARTY,
    value: '0x1',
  },
  receipt: {
    transactionHash: '0xfailed1',
    blockNumber: '0x30',
    status: '0x0',
    from: WATCHED_ADDRESS,
    to: COUNTERPARTY,
    gasUsed: '0x5208',
    effectiveGasPrice: '0x3b9aca00',
    logs: [],
  },
};

export const noLogsTx = {
  chain: 'base',
  tx: {
    hash: '0xnologs',
    blockNumber: '0x40',
    transactionIndex: '0x0',
    from: WATCHED_ADDRESS,
    to: COUNTERPARTY,
    value: '0x0',
    gasPrice: '0x3b9aca00',
  },
  receipt: {
    transactionHash: '0xnologs',
    blockNumber: '0x40',
    transactionIndex: '0x0',
    status: '0x1',
    from: WATCHED_ADDRESS,
    gasUsed: '0x5208',
    effectiveGasPrice: '0x3b9aca00',
    logs: [],
  },
};

export const removedLogTx = {
  chain: 'base',
  tx: {
    hash: '0xremoved',
    blockNumber: '0x50',
    transactionIndex: '0x0',
    from: WATCHED_ADDRESS,
    to: TOKEN_CONTRACT,
    value: '0x0',
    gasPrice: '0x3b9aca00',
  },
  receipt: {
    transactionHash: '0xremoved',
    blockNumber: '0x50',
    transactionIndex: '0x0',
    status: '0x1',
    from: WATCHED_ADDRESS,
    gasUsed: '0xea60',
    effectiveGasPrice: '0x3b9aca00',
    logs: [
      {
        address: TOKEN_CONTRACT,
        topics: [TRANSFER_TOPIC, padTopic(WATCHED_ADDRESS), padTopic(COUNTERPARTY)],
        data: '0x' + (500000n).toString(16),
        logIndex: '0x0',
        removed: true,
      },
    ],
  },
};

export const zeroValueTx = {
  chain: 'base',
  tx: {
    hash: '0xzero',
    blockNumber: '0x60',
    transactionIndex: '0x0',
    from: WATCHED_ADDRESS,
    to: COUNTERPARTY,
    value: '0x0',
    gasPrice: '0x3b9aca00',
  },
  receipt: {
    transactionHash: '0xzero',
    blockNumber: '0x60',
    transactionIndex: '0x0',
    status: '0x1',
    from: WATCHED_ADDRESS,
    gasUsed: '0x5208',
    effectiveGasPrice: '0x3b9aca00',
    logs: [
      {
        address: TOKEN_CONTRACT,
        topics: [TRANSFER_TOPIC, padTopic(WATCHED_ADDRESS), padTopic(COUNTERPARTY)],
        data: '0x0',
        logIndex: '0x0',
      },
    ],
  },
};

export const multiLogTx = {
  chain: 'base',
  tx: {
    hash: '0xmultilog',
    blockNumber: '0x70',
    transactionIndex: '0x0',
    from: WATCHED_ADDRESS,
    to: TOKEN_CONTRACT,
    value: '0x5',
    gasPrice: '0x3b9aca00',
  },
  receipt: {
    transactionHash: '0xmultilog',
    blockNumber: '0x70',
    transactionIndex: '0x0',
    status: '0x1',
    from: WATCHED_ADDRESS,
    to: TOKEN_CONTRACT,
    gasUsed: '0x1d4c0',
    effectiveGasPrice: '0x3b9aca00',
    l1Fee: '0x7d0',
    logs: [
      {
        address: TOKEN_CONTRACT,
        topics: [TRANSFER_TOPIC, padTopic(WATCHED_ADDRESS), padTopic(COUNTERPARTY)],
        data: '0x' + (100n).toString(16),
        logIndex: '0x0',
      },
      {
        address: '0xdddddddddddddddddddddddddddddddddddddd',
        topics: [TRANSFER_TOPIC, padTopic(COUNTERPARTY), padTopic(WATCHED_ADDRESS)],
        data: '0x' + (200n).toString(16),
        logIndex: '0x1',
      },
    ],
  },
};

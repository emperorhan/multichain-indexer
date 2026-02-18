/**
 * Test fixtures for Solana transaction decoding.
 */

export const WATCHED_ADDRESS = '7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump';
export const OTHER_ADDRESS = 'ANotherAddr1111111111111111111111111111111';
export const FEE_PAYER = '7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump';
export const SPL_TOKEN_PROGRAM_ID = 'TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA';
export const SYSTEM_PROGRAM_ID = '11111111111111111111111111111111';
export const USDC_MINT = 'EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v';

export const solTransferTx = {
  slot: 100,
  blockTime: 1700000000,
  transaction: {
    message: {
      accountKeys: [
        { pubkey: WATCHED_ADDRESS },
        { pubkey: OTHER_ADDRESS },
        { pubkey: SYSTEM_PROGRAM_ID },
      ],
      instructions: [
        {
          programId: SYSTEM_PROGRAM_ID,
          parsed: {
            type: 'transfer',
            info: {
              source: WATCHED_ADDRESS,
              destination: OTHER_ADDRESS,
              lamports: 1000000000,
            },
          },
        },
      ],
    },
  },
  meta: {
    err: null,
    fee: 5000,
    preBalances: [2000000000, 500000000, 1],
    postBalances: [999995000, 1500000000, 1],
    innerInstructions: [],
  },
};

export const splTransferTx = {
  slot: 200,
  blockTime: 1700001000,
  transaction: {
    message: {
      accountKeys: [
        { pubkey: WATCHED_ADDRESS },
        { pubkey: 'sourceATA' },
        { pubkey: 'destATA' },
        { pubkey: SPL_TOKEN_PROGRAM_ID },
      ],
      instructions: [
        {
          programId: SPL_TOKEN_PROGRAM_ID,
          parsed: {
            type: 'transfer',
            info: {
              source: 'sourceATA',
              destination: 'destATA',
              amount: '500000',
              authority: WATCHED_ADDRESS,
            },
          },
        },
      ],
    },
  },
  meta: {
    err: null,
    fee: 5000,
    preBalances: [2000000000],
    postBalances: [1999995000],
    preTokenBalances: [
      {
        accountIndex: 1,
        mint: USDC_MINT,
        owner: WATCHED_ADDRESS,
        uiTokenAmount: { decimals: 6, amount: '1000000' },
      },
    ],
    postTokenBalances: [
      {
        accountIndex: 1,
        mint: USDC_MINT,
        owner: WATCHED_ADDRESS,
        uiTokenAmount: { decimals: 6, amount: '500000' },
      },
      {
        accountIndex: 2,
        mint: USDC_MINT,
        owner: OTHER_ADDRESS,
        uiTokenAmount: { decimals: 6, amount: '500000' },
      },
    ],
    innerInstructions: [],
  },
};

export const splTransferCheckedTx = {
  slot: 300,
  blockTime: 1700002000,
  transaction: {
    message: {
      accountKeys: [
        { pubkey: WATCHED_ADDRESS },
        { pubkey: 'sourceATA2' },
        { pubkey: USDC_MINT },
        { pubkey: 'destATA2' },
        { pubkey: SPL_TOKEN_PROGRAM_ID },
      ],
      instructions: [
        {
          programId: SPL_TOKEN_PROGRAM_ID,
          parsed: {
            type: 'transferChecked',
            info: {
              source: 'sourceATA2',
              destination: 'destATA2',
              mint: USDC_MINT,
              authority: WATCHED_ADDRESS,
              tokenAmount: {
                amount: '1000000',
                decimals: 6,
                uiAmount: 1.0,
              },
            },
          },
        },
      ],
    },
  },
  meta: {
    err: null,
    fee: 5000,
    preBalances: [2000000000],
    postBalances: [1999995000],
    innerInstructions: [],
  },
};

export const splMintTx = {
  slot: 1100,
  blockTime: 1700008000,
  transaction: {
    message: {
      accountKeys: [
        { pubkey: WATCHED_ADDRESS },
        { pubkey: 'mintDestinationATA' },
        { pubkey: SPL_TOKEN_PROGRAM_ID },
        { pubkey: USDC_MINT },
      ],
      instructions: [
        {
          programId: SPL_TOKEN_PROGRAM_ID,
          parsed: {
            type: 'mintTo',
            info: {
              account: 'mintDestinationATA',
              mint: USDC_MINT,
              authority: WATCHED_ADDRESS,
              amount: '2500000',
            },
          },
        },
      ],
    },
  },
  meta: {
    err: null,
    fee: 5000,
    preBalances: [2000000000],
    postBalances: [1999995000],
    preTokenBalances: [
      {
        accountIndex: 1,
        mint: USDC_MINT,
        owner: WATCHED_ADDRESS,
        uiTokenAmount: {
          amount: '0',
          decimals: 6,
          uiAmount: 0,
        },
      },
    ],
    postTokenBalances: [
      {
        accountIndex: 1,
        mint: USDC_MINT,
        owner: WATCHED_ADDRESS,
        uiTokenAmount: {
          amount: '2500000',
          decimals: 6,
          uiAmount: 2.5,
        },
      },
    ],
    innerInstructions: [],
  },
};

export const splBurnTx = {
  slot: 1200,
  blockTime: 1700009000,
  transaction: {
    message: {
      accountKeys: [
        { pubkey: WATCHED_ADDRESS },
        { pubkey: 'burnSourceATA' },
        { pubkey: SPL_TOKEN_PROGRAM_ID },
        { pubkey: USDC_MINT },
      ],
      instructions: [
        {
          programId: SPL_TOKEN_PROGRAM_ID,
          parsed: {
            type: 'burn',
            info: {
              account: 'burnSourceATA',
              mint: USDC_MINT,
              owner: WATCHED_ADDRESS,
              amount: '1250000',
            },
          },
        },
      ],
    },
  },
  meta: {
    err: null,
    fee: 5000,
    preBalances: [2000000000],
    postBalances: [1999995000],
    preTokenBalances: [
      {
        accountIndex: 1,
        mint: USDC_MINT,
        owner: WATCHED_ADDRESS,
        uiTokenAmount: {
          amount: '2500000',
          decimals: 6,
          uiAmount: 2.5,
        },
      },
    ],
    postTokenBalances: [
      {
        accountIndex: 1,
        mint: USDC_MINT,
        owner: WATCHED_ADDRESS,
        uiTokenAmount: {
          amount: '1250000',
          decimals: 6,
          uiAmount: 1.25,
        },
      },
    ],
    innerInstructions: [],
  },
};

export const failedTx = {
  slot: 400,
  blockTime: 1700003000,
  transaction: {
    message: {
      accountKeys: [
        { pubkey: WATCHED_ADDRESS },
        { pubkey: OTHER_ADDRESS },
      ],
      instructions: [
        {
          programId: SYSTEM_PROGRAM_ID,
          parsed: {
            type: 'transfer',
            info: {
              source: WATCHED_ADDRESS,
              destination: OTHER_ADDRESS,
              lamports: 99999999999,
            },
          },
        },
      ],
    },
  },
  meta: {
    err: { InstructionError: [0, { Custom: 1 }] },
    fee: 5000,
    preBalances: [100000, 0],
    postBalances: [95000, 0],
    innerInstructions: [],
  },
};

export const innerInstructionsTx = {
  slot: 500,
  blockTime: 1700004000,
  transaction: {
    message: {
      accountKeys: [
        { pubkey: WATCHED_ADDRESS },
        { pubkey: OTHER_ADDRESS },
        { pubkey: SYSTEM_PROGRAM_ID },
      ],
      instructions: [
        {
          programId: 'SomeProgram1111111111111111111111111111111',
          parsed: null,
        },
      ],
    },
  },
  meta: {
    err: null,
    fee: 5000,
    preBalances: [2000000000, 500000000, 1],
    postBalances: [999995000, 1500000000, 1],
    innerInstructions: [
      {
        index: 0,
        instructions: [
          {
            programId: SYSTEM_PROGRAM_ID,
            parsed: {
              type: 'transfer',
              info: {
                source: WATCHED_ADDRESS,
                destination: OTHER_ADDRESS,
                lamports: 500000000,
              },
            },
          },
        ],
      },
    ],
  },
};

export const transferWithSeedTx = {
  slot: 1500,
  blockTime: 1700004500,
  transaction: {
    message: {
      accountKeys: [
        { pubkey: WATCHED_ADDRESS },
        { pubkey: OTHER_ADDRESS },
        { pubkey: SYSTEM_PROGRAM_ID },
      ],
      instructions: [
        {
          programId: SYSTEM_PROGRAM_ID,
          parsed: {
            type: 'transferWithSeed',
            info: {
              source: WATCHED_ADDRESS,
              destination: OTHER_ADDRESS,
              lamports: 222,
              fromSeed: 'seed',
            },
          },
        },
      ],
    },
  },
  meta: {
    err: null,
    fee: 5000,
    preBalances: [2000000000, 500000000, 1],
    postBalances: [1999999778, 500000222, 1],
    innerInstructions: [],
  },
};

export const transferWithPubkeyTx = {
  slot: 1510,
  blockTime: 1700004550,
  transaction: {
    message: {
      accountKeys: [
        { pubkey: WATCHED_ADDRESS },
        { pubkey: OTHER_ADDRESS },
        { pubkey: SYSTEM_PROGRAM_ID },
      ],
      instructions: [
        {
          programId: SYSTEM_PROGRAM_ID,
          parsed: {
            type: 'transfer',
            info: {
              fromPubkey: WATCHED_ADDRESS,
              toPubkey: OTHER_ADDRESS,
              lamports: '333',
            },
          },
        },
      ],
    },
  },
  meta: {
    err: null,
    fee: 5000,
    preBalances: [2000000000, 500000000, 1],
    postBalances: [1999999667, 500000333, 1],
    innerInstructions: [],
  },
};

export const createAccountFromPubkeyTx = {
  slot: 1511,
  blockTime: 1700004560,
  transaction: {
    message: {
      accountKeys: [
        { pubkey: WATCHED_ADDRESS },
        { pubkey: 'newAccountPubkey222' },
        { pubkey: SYSTEM_PROGRAM_ID },
      ],
      instructions: [
        {
          programId: SYSTEM_PROGRAM_ID,
          parsed: {
            type: 'createAccount',
            info: {
              fromPubkey: WATCHED_ADDRESS,
              newAccount: 'newAccountPubkey222',
              lamports: 2039280,
            },
          },
        },
      ],
    },
  },
  meta: {
    err: null,
    fee: 5000,
    preBalances: [2000000000, 0, 1],
    postBalances: [1997960720, 2039280, 1],
    innerInstructions: [],
  },
};

export const createAccountWithSeedTx = {
  slot: 1501,
  blockTime: 1700004510,
  transaction: {
    message: {
      accountKeys: [
        { pubkey: WATCHED_ADDRESS },
        { pubkey: 'seedAccount111' },
        { pubkey: SYSTEM_PROGRAM_ID },
      ],
      instructions: [
        {
          programId: SYSTEM_PROGRAM_ID,
          parsed: {
            type: 'createAccountWithSeed',
            info: {
              fromAccount: WATCHED_ADDRESS,
              newAccount: 'seedAccount111',
              base: WATCHED_ADDRESS,
              lamports: '2039280',
              seed: 'seed',
            },
          },
        },
      ],
    },
  },
  meta: {
    err: null,
    fee: 5000,
    preBalances: [2000000000, 0, 1],
    postBalances: [1997960720, 2039280, 1],
    innerInstructions: [],
  },
};

export const withdrawNonceAccountTx = {
  slot: 1502,
  blockTime: 1700004520,
  transaction: {
    message: {
      accountKeys: [
        { pubkey: WATCHED_ADDRESS },
        { pubkey: 'withdrawRecipient111' },
        { pubkey: SYSTEM_PROGRAM_ID },
      ],
      instructions: [
        {
          programId: SYSTEM_PROGRAM_ID,
          parsed: {
            type: 'withdrawNonceAccount',
            info: {
              nonceAccount: WATCHED_ADDRESS,
              to: 'withdrawRecipient111',
              lamports: 120000,
              authority: WATCHED_ADDRESS,
            },
          },
        },
      ],
    },
  },
  meta: {
    err: null,
    fee: 5000,
    preBalances: [2000000000, 0, 1],
    postBalances: [1999880000, 120000, 1],
    innerInstructions: [],
  },
};

export const createAccountTx = {
  slot: 600,
  blockTime: 1700005000,
  transaction: {
    message: {
      accountKeys: [
        { pubkey: WATCHED_ADDRESS },
        { pubkey: 'newAccount111' },
        { pubkey: SYSTEM_PROGRAM_ID },
      ],
      instructions: [
        {
          programId: SYSTEM_PROGRAM_ID,
          parsed: {
            type: 'createAccount',
            info: {
              source: WATCHED_ADDRESS,
              newAccount: 'newAccount111',
              lamports: 2039280,
            },
          },
        },
      ],
    },
  },
  meta: {
    err: null,
    fee: 5000,
    preBalances: [2000000000, 0, 1],
    postBalances: [1997955720, 2039280, 1],
    innerInstructions: [],
  },
};

export const noTransferTx = {
  slot: 700,
  blockTime: 1700006000,
  transaction: {
    message: {
      accountKeys: [
        { pubkey: WATCHED_ADDRESS },
        { pubkey: 'someProgram' },
      ],
      instructions: [
        {
          programId: 'someProgram',
          parsed: null,
        },
      ],
    },
  },
  meta: {
    err: null,
    fee: 5000,
    preBalances: [2000000000, 1],
    postBalances: [1999995000, 1],
    innerInstructions: [],
  },
};

export const stringAccountKeysTx = {
  slot: 800,
  blockTime: 1700007000,
  transaction: {
    message: {
      accountKeys: [
        WATCHED_ADDRESS,
        OTHER_ADDRESS,
        SYSTEM_PROGRAM_ID,
      ],
      instructions: [
        {
          programId: SYSTEM_PROGRAM_ID,
          parsed: {
            type: 'transfer',
            info: {
              source: WATCHED_ADDRESS,
              destination: OTHER_ADDRESS,
              lamports: 1000000,
            },
          },
        },
      ],
    },
  },
  meta: {
    err: null,
    fee: 5000,
    preBalances: [2000000000, 500000000, 1],
    postBalances: [1998995000, 501000000, 1],
    innerInstructions: [],
  },
};

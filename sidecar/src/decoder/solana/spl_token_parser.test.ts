import { describe, it, expect } from 'vitest';
import { parseSplTokenInstruction } from './spl_token_parser';

const SPL_TOKEN_PROGRAM_ID = 'TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA';
const USDC_MINT = 'EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v';

describe('parseSplTokenInstruction', () => {
  const accountKeyMap = new Map<number, string>([
    [0, 'owner1'],
    [1, 'sourceATA'],
    [2, 'destATA'],
    [3, SPL_TOKEN_PROGRAM_ID],
  ]);

  const metaWithBalances = {
    preTokenBalances: [
      {
        accountIndex: 1,
        mint: USDC_MINT,
        owner: 'owner1',
        uiTokenAmount: { decimals: 6, amount: '1000000' },
      },
    ],
    postTokenBalances: [
      {
        accountIndex: 2,
        mint: USDC_MINT,
        owner: 'owner2',
        uiTokenAmount: { decimals: 6, amount: '500000' },
      },
    ],
  };

  it('returns empty array for unparsed instruction', () => {
    const result = parseSplTokenInstruction(
      { parsed: null },
      0,
      SPL_TOKEN_PROGRAM_ID,
      accountKeyMap,
      {}
    );
    expect(result).toEqual([]);
  });

  it('returns empty array for unknown type', () => {
    const result = parseSplTokenInstruction(
      { parsed: { type: 'approve', info: {} } },
      0,
      SPL_TOKEN_PROGRAM_ID,
      accountKeyMap,
      {}
    );
    expect(result).toEqual([]);
  });

  it('returns empty array when info is missing', () => {
    const result = parseSplTokenInstruction(
      { parsed: { type: 'transfer' } },
      0,
      SPL_TOKEN_PROGRAM_ID,
      accountKeyMap,
      {}
    );
    expect(result).toEqual([]);
  });

  describe('transfer', () => {
    it('parses a transfer with authority', () => {
      const result = parseSplTokenInstruction(
        {
          parsed: {
            type: 'transfer',
            info: {
              source: 'sourceATA',
              destination: 'destATA',
              amount: '500000',
              authority: 'owner1',
            },
          },
        },
        0,
        SPL_TOKEN_PROGRAM_ID,
        accountKeyMap,
        metaWithBalances
      );

      expect(result).toHaveLength(1);
      expect(result[0].fromAddress).toBe('owner1');
      expect(result[0].fromAta).toBe('sourceATA');
      expect(result[0].toAta).toBe('destATA');
      expect(result[0].amount).toBe('500000');
      expect(result[0].contractAddress).toBe(USDC_MINT);
      expect(result[0].transferType).toBe('transfer');
      expect(result[0].tokenType).toBe('FUNGIBLE');
      expect(result[0].programId).toBe(SPL_TOKEN_PROGRAM_ID);
    });

    it('falls back to ATA address when authority is missing', () => {
      const result = parseSplTokenInstruction(
        {
          parsed: {
            type: 'transfer',
            info: {
              source: 'sourceATA',
              destination: 'destATA',
              amount: '100',
            },
          },
        },
        0,
        SPL_TOKEN_PROGRAM_ID,
        accountKeyMap,
        {}
      );

      expect(result).toHaveLength(1);
      expect(result[0].fromAddress).toBe('sourceATA');
      expect(result[0].toAddress).toBe('destATA');
    });

    it('resolves mint from meta tokenBalances', () => {
      const result = parseSplTokenInstruction(
        {
          parsed: {
            type: 'transfer',
            info: {
              source: 'sourceATA',
              destination: 'destATA',
              amount: '100',
              authority: 'owner1',
            },
          },
        },
        0,
        SPL_TOKEN_PROGRAM_ID,
        accountKeyMap,
        metaWithBalances
      );

      expect(result[0].contractAddress).toBe(USDC_MINT);
      expect(result[0].tokenDecimals).toBe(6);
    });

    it('returns empty mint when no meta balances', () => {
      const result = parseSplTokenInstruction(
        {
          parsed: {
            type: 'transfer',
            info: {
              source: 'sourceATA',
              destination: 'destATA',
              amount: '100',
              authority: 'owner1',
            },
          },
        },
        0,
        SPL_TOKEN_PROGRAM_ID,
        accountKeyMap,
        {}
      );

      expect(result[0].contractAddress).toBe('');
    });
  });

  describe('transferChecked', () => {
    it('parses a transferChecked with explicit mint and decimals', () => {
      const result = parseSplTokenInstruction(
        {
          parsed: {
            type: 'transferChecked',
            info: {
              source: 'sourceATA',
              destination: 'destATA',
              mint: USDC_MINT,
              authority: 'owner1',
              tokenAmount: {
                amount: '1000000',
                decimals: 6,
                uiAmount: 1.0,
              },
            },
          },
        },
        3,
        SPL_TOKEN_PROGRAM_ID,
        accountKeyMap,
        {}
      );

      expect(result).toHaveLength(1);
      expect(result[0].contractAddress).toBe(USDC_MINT);
      expect(result[0].amount).toBe('1000000');
      expect(result[0].tokenDecimals).toBe(6);
      expect(result[0].transferType).toBe('transferChecked');
      expect(result[0].fromAddress).toBe('owner1');
      expect(result[0].instructionIndex).toBe(3);
      expect(result[0].tokenType).toBe('FUNGIBLE');
    });

    it('falls back to ATA address when no authority', () => {
      const result = parseSplTokenInstruction(
        {
          parsed: {
            type: 'transferChecked',
            info: {
              source: 'sourceATA',
              destination: 'destATA',
              mint: USDC_MINT,
              tokenAmount: {
                amount: '500',
                decimals: 6,
              },
            },
          },
        },
        0,
        SPL_TOKEN_PROGRAM_ID,
        accountKeyMap,
        {}
      );

      expect(result[0].fromAddress).toBe('sourceATA');
      expect(result[0].toAddress).toBe('destATA');
    });
  });
});

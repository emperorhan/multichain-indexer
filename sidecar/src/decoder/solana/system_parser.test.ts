import { describe, it, expect } from 'vitest';
import { parseSystemInstruction } from './system_parser';

describe('parseSystemInstruction', () => {
  const accountKeyMap = new Map<number, string>([
    [0, 'addr1'],
    [1, 'addr2'],
    [2, '11111111111111111111111111111111'],
  ]);

  it('returns empty array for null parsed', () => {
    const result = parseSystemInstruction(
      { parsed: null },
      0,
      accountKeyMap
    );
    expect(result).toEqual([]);
  });

  it('returns empty array for undefined parsed', () => {
    const result = parseSystemInstruction(
      {},
      0,
      accountKeyMap
    );
    expect(result).toEqual([]);
  });

  it('returns empty array for unknown type', () => {
    const result = parseSystemInstruction(
      { parsed: { type: 'advanceNonce', info: {} } },
      0,
      accountKeyMap
    );
    expect(result).toEqual([]);
  });

  it('returns empty array when info is missing', () => {
    const result = parseSystemInstruction(
      { parsed: { type: 'transfer' } },
      0,
      accountKeyMap
    );
    expect(result).toEqual([]);
  });

  describe('transfer', () => {
    it('parses a system transfer', () => {
      const result = parseSystemInstruction(
        {
          parsed: {
            type: 'transfer',
            info: {
              source: 'addr1',
              destination: 'addr2',
              lamports: 1000000000,
            },
          },
        },
        0,
        accountKeyMap
      );

      expect(result).toHaveLength(1);
      expect(result[0]).toEqual({
        instructionIndex: 0,
        programId: '11111111111111111111111111111111',
        fromAddress: 'addr1',
        toAddress: 'addr2',
        amount: '1000000000',
        contractAddress: '11111111111111111111111111111111',
        transferType: 'systemTransfer',
        fromAta: '',
        toAta: '',
        tokenSymbol: 'SOL',
        tokenName: 'Solana',
        tokenDecimals: 9,
        tokenType: 'NATIVE',
      });
    });

    it('returns empty array when lamports is 0', () => {
      const result = parseSystemInstruction(
        {
          parsed: {
            type: 'transfer',
            info: {
              source: 'addr1',
              destination: 'addr2',
              lamports: 0,
            },
          },
        },
        5,
        accountKeyMap
      );
      expect(result).toEqual([]);
    });

    it('preserves instruction index', () => {
      const result = parseSystemInstruction(
        {
          parsed: {
            type: 'transfer',
            info: {
              source: 'addr1',
              destination: 'addr2',
              lamports: 500,
            },
          },
        },
        7,
        accountKeyMap
      );
      expect(result[0].instructionIndex).toBe(7);
    });
  });

  describe('createAccount', () => {
    it('parses a createAccount instruction', () => {
      const result = parseSystemInstruction(
        {
          parsed: {
            type: 'createAccount',
            info: {
              source: 'addr1',
              newAccount: 'newAddr',
              lamports: 2039280,
            },
          },
        },
        1,
        accountKeyMap
      );

      expect(result).toHaveLength(1);
      expect(result[0]).toEqual({
        instructionIndex: 1,
        programId: '11111111111111111111111111111111',
        fromAddress: 'addr1',
        toAddress: 'newAddr',
        amount: '2039280',
        contractAddress: '11111111111111111111111111111111',
        transferType: 'createAccount',
        fromAta: '',
        toAta: '',
        tokenSymbol: 'SOL',
        tokenName: 'Solana',
        tokenDecimals: 9,
        tokenType: 'NATIVE',
      });
    });

    it('returns empty array when lamports is 0', () => {
      const result = parseSystemInstruction(
        {
          parsed: {
            type: 'createAccount',
            info: {
              source: 'addr1',
              newAccount: 'newAddr',
              lamports: 0,
            },
          },
        },
        0,
        accountKeyMap
      );
      expect(result).toEqual([]);
    });
  });
});

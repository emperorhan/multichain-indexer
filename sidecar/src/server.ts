import * as grpc from '@grpc/grpc-js';
import * as protoLoader from '@grpc/proto-loader';
import path from 'path';
import { decodeSolanaTransactionBatch } from './decoder';
import logger from './logger';

const PROTO_PATH = path.resolve(__dirname, '../../proto/sidecar/v1/decoder.proto');

export function createServer(): grpc.Server {
  const packageDef = protoLoader.loadSync(PROTO_PATH, {
    keepCase: false,
    longs: String,
    enums: String,
    defaults: true,
    oneofs: true,
  });

  const protoDescriptor = grpc.loadPackageDefinition(packageDef) as any;
  const sidecarService = protoDescriptor.sidecar.v1.ChainDecoder.service;

  const MAX_MSG_SIZE = 16 * 1024 * 1024; // 16 MB — Solana mainnet blocks can exceed 4 MB default
  const server = new grpc.Server({
    'grpc.max_receive_message_length': MAX_MSG_SIZE,
    'grpc.max_send_message_length': MAX_MSG_SIZE,
  });
  server.addService(sidecarService, {
    decodeSolanaTransactionBatch: handleDecodeSolanaTransactionBatch,
    healthCheck: handleHealthCheck,
  });

  return server;
}

function handleDecodeSolanaTransactionBatch(
  call: grpc.ServerUnaryCall<any, any>,
  callback: grpc.sendUnaryData<any>
): void {
  const request = call.request;

  try {
    const transactions: Array<{ signature: string; rawJson: Buffer }> = request.transactions || [];
    const watchedAddresses: string[] = request.watchedAddresses || [];

    const result = decodeSolanaTransactionBatch(transactions, watchedAddresses);

    // Map balanceEvents to proto BalanceEventInfo format
    const mappedResults = result.results.map((r) => ({
      txHash: r.txHash,
      blockCursor: r.blockCursor,
      blockTime: r.blockTime,
      feeAmount: r.feeAmount,
      feePayer: r.feePayer,
      status: r.status,
      error: r.error,
      metadata: r.metadata || {},
      balanceEvents: r.balanceEvents.map((ev) => ({
        outerInstructionIndex: ev.outerInstructionIndex,
        innerInstructionIndex: ev.innerInstructionIndex,
        eventCategory: ev.eventCategory,
        eventAction: ev.eventAction,
        programId: ev.programId,
        address: ev.address,
        contractAddress: ev.contractAddress,
        delta: ev.delta,
        counterpartyAddress: ev.counterpartyAddress,
        tokenSymbol: ev.tokenSymbol,
        tokenName: ev.tokenName,
        tokenDecimals: ev.tokenDecimals,
        tokenType: ev.tokenType,
        metadata: ev.metadata,
      })),
    }));

    callback(null, { results: mappedResults, errors: result.errors });
  } catch (err: any) {
    logger.error({ err }, 'error decoding batch');
    callback({
      code: grpc.status.INTERNAL,
      message: err.message || 'Internal error',
    });
  }
}

function handleHealthCheck(
  _call: grpc.ServerUnaryCall<any, any>,
  callback: grpc.sendUnaryData<any>
): void {
  callback(null, { status: 'SERVING' });
}

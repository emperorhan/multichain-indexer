import * as grpc from '@grpc/grpc-js';
import * as protoLoader from '@grpc/proto-loader';
import path from 'path';
import { decodeSolanaTransactionBatch } from './decoder';

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

  const server = new grpc.Server();
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
    callback(null, result);
  } catch (err: any) {
    console.error('Error decoding batch:', err);
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

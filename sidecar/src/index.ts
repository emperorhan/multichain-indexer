import * as grpc from '@grpc/grpc-js';
import * as fs from 'fs';
import { createServer } from './server';
import logger from './logger';

const PORT = process.env.PORT || '50051';
const TLS_ENABLED = (process.env.SIDECAR_TLS_ENABLED || '').toLowerCase() === 'true';
const TLS_CERT = process.env.SIDECAR_TLS_CERT || '';
const TLS_KEY = process.env.SIDECAR_TLS_KEY || '';
const TLS_CA = process.env.SIDECAR_TLS_CA || '';

function buildServerCredentials(): grpc.ServerCredentials {
  if (!TLS_ENABLED) {
    return grpc.ServerCredentials.createInsecure();
  }

  if (!TLS_CERT || !TLS_KEY) {
    logger.fatal('SIDECAR_TLS_CERT and SIDECAR_TLS_KEY are required when SIDECAR_TLS_ENABLED=true');
    process.exit(1);
  }

  const keyCert: grpc.KeyCertPair = {
    private_key: fs.readFileSync(TLS_KEY),
    cert_chain: fs.readFileSync(TLS_CERT),
  };

  // If CA is provided, enable mTLS (require client certificates).
  const rootCerts = TLS_CA ? fs.readFileSync(TLS_CA) : null;
  const checkClientCertificate = rootCerts !== null;

  return grpc.ServerCredentials.createSsl(rootCerts, [keyCert], checkClientCertificate);
}

function main(): void {
  const server = createServer();
  const creds = buildServerCredentials();

  server.bindAsync(
    `0.0.0.0:${PORT}`,
    creds,
    (err, port) => {
      if (err) {
        logger.fatal({ err }, 'failed to bind server');
        process.exit(1);
      }
      logger.info({ port, tls: TLS_ENABLED }, 'sidecar gRPC server started');
    }
  );

  const shutdown = () => {
    logger.info('shutting down sidecar');
    server.tryShutdown(() => {
      logger.info('sidecar shut down');
      process.exit(0);
    });
  };

  process.on('SIGINT', shutdown);
  process.on('SIGTERM', shutdown);
}

main();

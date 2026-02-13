import * as grpc from '@grpc/grpc-js';
import { createServer } from './server';

const PORT = process.env.PORT || '50051';

function main(): void {
  const server = createServer();

  server.bindAsync(
    `0.0.0.0:${PORT}`,
    grpc.ServerCredentials.createInsecure(),
    (err, port) => {
      if (err) {
        console.error('Failed to bind server:', err);
        process.exit(1);
      }
      console.log(`Sidecar gRPC server listening on port ${port}`);
    }
  );

  const shutdown = () => {
    console.log('Shutting down sidecar...');
    server.tryShutdown(() => {
      console.log('Sidecar shut down');
      process.exit(0);
    });
  };

  process.on('SIGINT', shutdown);
  process.on('SIGTERM', shutdown);
}

main();

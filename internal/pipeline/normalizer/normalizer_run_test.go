package normalizer

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// buildTransportCredentials tests
// ---------------------------------------------------------------------------

func TestBuildTransportCredentials_Insecure(t *testing.T) {
	n := &Normalizer{
		tlsEnabled: false,
		logger:     slog.Default(),
	}

	creds, err := n.buildTransportCredentials()
	require.NoError(t, err)
	require.NotNil(t, creds)

	// insecure credentials report "insecure" as the security protocol.
	info := creds.Info()
	assert.Equal(t, "insecure", info.SecurityProtocol)
}

func TestBuildTransportCredentials_TLSEnabled_NoCA(t *testing.T) {
	// When TLS is enabled but no CA path is provided (empty string),
	// buildTransportCredentials should fail because os.ReadFile("")
	// will return an error.
	n := &Normalizer{
		tlsEnabled: true,
		tlsCA:      "",
		logger:     slog.Default(),
	}

	creds, err := n.buildTransportCredentials()
	require.Error(t, err)
	assert.Nil(t, creds)
	assert.Contains(t, err.Error(), "read CA cert")
}

func TestBuildTransportCredentials_TLSEnabled_WithCA(t *testing.T) {
	// Generate a self-signed CA certificate for testing.
	caKeyPair, caCertPEM := generateSelfSignedCA(t)

	tmpDir := t.TempDir()
	caFile := filepath.Join(tmpDir, "ca.pem")
	require.NoError(t, os.WriteFile(caFile, caCertPEM, 0o600))

	t.Run("ca_only", func(t *testing.T) {
		n := &Normalizer{
			tlsEnabled: true,
			tlsCA:      caFile,
			logger:     slog.Default(),
		}

		creds, err := n.buildTransportCredentials()
		require.NoError(t, err)
		require.NotNil(t, creds)
		info := creds.Info()
		assert.Equal(t, "tls", info.SecurityProtocol)
	})

	t.Run("ca_with_mtls_client_cert", func(t *testing.T) {
		// Generate client cert signed by our CA.
		clientCertPEM, clientKeyPEM := generateClientCert(t, caKeyPair, caCertPEM)

		clientCertFile := filepath.Join(tmpDir, "client.pem")
		clientKeyFile := filepath.Join(tmpDir, "client-key.pem")
		require.NoError(t, os.WriteFile(clientCertFile, clientCertPEM, 0o600))
		require.NoError(t, os.WriteFile(clientKeyFile, clientKeyPEM, 0o600))

		n := &Normalizer{
			tlsEnabled: true,
			tlsCA:      caFile,
			tlsCert:    clientCertFile,
			tlsKey:     clientKeyFile,
			logger:     slog.Default(),
		}

		creds, err := n.buildTransportCredentials()
		require.NoError(t, err)
		require.NotNil(t, creds)
		info := creds.Info()
		assert.Equal(t, "tls", info.SecurityProtocol)
	})

	t.Run("ca_with_invalid_client_cert", func(t *testing.T) {
		invalidCertFile := filepath.Join(tmpDir, "invalid-cert.pem")
		require.NoError(t, os.WriteFile(invalidCertFile, []byte("not a cert"), 0o600))

		n := &Normalizer{
			tlsEnabled: true,
			tlsCA:      caFile,
			tlsCert:    invalidCertFile,
			tlsKey:     invalidCertFile,
			logger:     slog.Default(),
		}

		_, err := n.buildTransportCredentials()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "load client cert/key")
	})
}

func TestBuildTransportCredentials_TLSEnabled_InvalidCA(t *testing.T) {
	t.Run("nonexistent_ca_file", func(t *testing.T) {
		n := &Normalizer{
			tlsEnabled: true,
			tlsCA:      "/nonexistent/path/ca.pem",
			logger:     slog.Default(),
		}

		creds, err := n.buildTransportCredentials()
		require.Error(t, err)
		assert.Nil(t, creds)
		assert.Contains(t, err.Error(), "read CA cert")
	})

	t.Run("invalid_pem_content", func(t *testing.T) {
		tmpDir := t.TempDir()
		badCAFile := filepath.Join(tmpDir, "bad-ca.pem")
		require.NoError(t, os.WriteFile(badCAFile, []byte("this is not valid PEM"), 0o600))

		n := &Normalizer{
			tlsEnabled: true,
			tlsCA:      badCAFile,
			logger:     slog.Default(),
		}

		creds, err := n.buildTransportCredentials()
		require.Error(t, err)
		assert.Nil(t, creds)
		assert.Contains(t, err.Error(), "failed to parse CA cert")
	})

	t.Run("empty_ca_file", func(t *testing.T) {
		tmpDir := t.TempDir()
		emptyCAFile := filepath.Join(tmpDir, "empty-ca.pem")
		require.NoError(t, os.WriteFile(emptyCAFile, []byte{}, 0o600))

		n := &Normalizer{
			tlsEnabled: true,
			tlsCA:      emptyCAFile,
			logger:     slog.Default(),
		}

		creds, err := n.buildTransportCredentials()
		require.Error(t, err)
		assert.Nil(t, creds)
		assert.Contains(t, err.Error(), "failed to parse CA cert")
	})
}

// ---------------------------------------------------------------------------
// Run() lifecycle tests
// ---------------------------------------------------------------------------

func TestNormalizer_Run_ContextCancellation(t *testing.T) {
	// Start Run() with a context that is cancelled immediately.
	// Even though there is no real gRPC server, Run() should honour the
	// cancellation and return without hanging.
	rawBatchCh := make(chan event.RawBatch)
	normalizedCh := make(chan event.NormalizedBatch, 1)

	n := New(
		"localhost:0", // non-routable port
		5*time.Second,
		rawBatchCh,
		normalizedCh,
		1,
		slog.Default(),
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	done := make(chan error, 1)
	go func() {
		done <- n.Run(ctx)
	}()

	select {
	case err := <-done:
		// Run should complete. The error will be context.Canceled
		// propagated through the worker errgroup.
		if err != nil {
			assert.ErrorIs(t, err, context.Canceled)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return within 5s after context cancellation")
	}
}

func TestNormalizer_Run_ContextTimeout(t *testing.T) {
	// Run with a short-lived context to verify graceful shutdown.
	rawBatchCh := make(chan event.RawBatch)
	normalizedCh := make(chan event.NormalizedBatch, 1)

	n := New(
		"localhost:0",
		5*time.Second,
		rawBatchCh,
		normalizedCh,
		2, // multiple workers
		slog.Default(),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- n.Run(ctx)
	}()

	select {
	case err := <-done:
		// Should exit due to context deadline exceeded.
		if err != nil {
			assert.True(t,
				err == context.Canceled || err == context.DeadlineExceeded ||
					assert.ObjectsAreEqual(context.Canceled, err) ||
					assert.ObjectsAreEqual(context.DeadlineExceeded, err),
				"expected context error, got: %v", err,
			)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return within 5s after context timeout")
	}
}

func TestNormalizer_Run_InvalidAddress(t *testing.T) {
	// Use an invalid address format. grpc.NewClient with lazy connection
	// will not fail immediately; the error surfaces when a worker tries
	// to use the connection. With a cancelled context, the worker should
	// exit via ctx.Done() regardless.
	rawBatchCh := make(chan event.RawBatch)
	normalizedCh := make(chan event.NormalizedBatch, 1)

	n := New(
		":::invalid-addr:::not-a-host",
		5*time.Second,
		rawBatchCh,
		normalizedCh,
		1,
		slog.Default(),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- n.Run(ctx)
	}()

	select {
	case err := <-done:
		// Either an immediate error from grpc.NewClient or a context
		// cancellation error from the workers. Both are acceptable.
		_ = err // success: Run() returned
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return within 5s with invalid address")
	}
}

func TestNormalizer_Run_ClosedInputChannel(t *testing.T) {
	// When the input rawBatchCh is closed, workers should exit cleanly.
	rawBatchCh := make(chan event.RawBatch)
	normalizedCh := make(chan event.NormalizedBatch, 1)

	n := New(
		"localhost:0",
		5*time.Second,
		rawBatchCh,
		normalizedCh,
		2,
		slog.Default(),
	)

	// Close input channel so workers return nil immediately.
	close(rawBatchCh)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- n.Run(ctx)
	}()

	select {
	case err := <-done:
		// Workers should return nil when the channel is closed.
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return within 5s after input channel closed")
	}
}

func TestNormalizer_Run_CircuitBreakerInitialized(t *testing.T) {
	// Verify that Run() initialises a circuit breaker with default thresholds
	// when none is pre-configured. We cancel immediately and check the breaker
	// was created.
	rawBatchCh := make(chan event.RawBatch)
	normalizedCh := make(chan event.NormalizedBatch, 1)

	n := New(
		"localhost:0",
		5*time.Second,
		rawBatchCh,
		normalizedCh,
		1,
		slog.Default(),
	)

	assert.Nil(t, n.circuitBreaker, "circuit breaker should not be set before Run()")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = n.Run(ctx)

	assert.NotNil(t, n.circuitBreaker, "circuit breaker should be initialized after Run()")
}

func TestNormalizer_Run_TLSInvalidCA_ReturnsError(t *testing.T) {
	// When TLS is enabled but the CA file does not exist, Run() should
	// return an error from buildTransportCredentials before spawning workers.
	rawBatchCh := make(chan event.RawBatch)
	normalizedCh := make(chan event.NormalizedBatch, 1)

	n := New(
		"localhost:0",
		5*time.Second,
		rawBatchCh,
		normalizedCh,
		1,
		slog.Default(),
		WithTLS(true, "/nonexistent/ca.pem", "", ""),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := n.Run(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build transport credentials")
}

// ---------------------------------------------------------------------------
// Helper: generate self-signed CA certificate
// ---------------------------------------------------------------------------

func generateSelfSignedCA(t *testing.T) (*ecdsa.PrivateKey, []byte) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test CA"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, template, template, &caKey.PublicKey, caKey)
	require.NoError(t, err)

	caCertPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caCertDER,
	})

	return caKey, caCertPEM
}

// generateClientCert creates a client certificate signed by the given CA.
func generateClientCert(t *testing.T, caKey *ecdsa.PrivateKey, caCertPEM []byte) (certPEM, keyPEM []byte) {
	t.Helper()

	block, _ := pem.Decode(caCertPEM)
	require.NotNil(t, block)
	caCert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			Organization: []string{"Test Client"},
		},
		NotBefore: time.Now().Add(-1 * time.Hour),
		NotAfter:  time.Now().Add(24 * time.Hour),
		KeyUsage:  x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageClientAuth,
		},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}

	clientCertDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, caCert, &clientKey.PublicKey, caKey)
	require.NoError(t, err)

	certPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: clientCertDER,
	})

	keyDER, err := x509.MarshalECPrivateKey(clientKey)
	require.NoError(t, err)
	keyPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: keyDER,
	})

	return certPEM, keyPEM
}

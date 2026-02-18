package main

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	appmetrics "github.com/emperorhan/multichain-indexer/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeDBStatsProvider struct {
	stats sql.DBStats
}

func (f fakeDBStatsProvider) Stats() sql.DBStats {
	return f.stats
}

type panicDBStatsProvider struct{}

func (panicDBStatsProvider) Stats() sql.DBStats {
	panic("db stats temporarily unavailable")
}

type flakyDBStatsProvider struct {
	failUntil int
	stats     sql.DBStats
	calls     int
	callCh    chan int
}

func (f *flakyDBStatsProvider) Stats() sql.DBStats {
	f.calls++
	if f.callCh != nil {
		f.callCh <- f.calls
	}
	if f.calls <= f.failUntil {
		panic("db stats temporarily unavailable")
	}
	return f.stats
}

func TestCollectDBPoolStats_RecordsTargetMetrics(t *testing.T) {
	targets := []runtimeTarget{
		{
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
		},
		{
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
		},
	}

	provider := fakeDBStatsProvider{
		stats: sql.DBStats{
			OpenConnections: 10,
			InUse:           3,
			Idle:            7,
			WaitCount:       13,
			WaitDuration:    1500 * 1000 * 1000,
		},
	}

	metrics := dbPoolStatsGauges{
		open: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "test_db_pool_open",
		}, []string{"chain", "network"}),
		inUse: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "test_db_pool_in_use",
		}, []string{"chain", "network"}),
		idle: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "test_db_pool_idle",
		}, []string{"chain", "network"}),
		waitCount: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "test_db_pool_wait_count",
		}, []string{"chain", "network"}),
		waitDuration: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "test_db_pool_wait_duration_seconds",
		}, []string{"chain", "network"}),
	}

	err := collectDBPoolStats(provider, targets, metrics)
	require.NoError(t, err)

	for _, target := range targets {
		chain := string(target.chain)
		network := string(target.network)

		assert.Equal(t, 10.0, readGaugeValue(t, metrics.open, chain, network))
		assert.Equal(t, 3.0, readGaugeValue(t, metrics.inUse, chain, network))
		assert.Equal(t, 7.0, readGaugeValue(t, metrics.idle, chain, network))
		assert.Equal(t, 13.0, readGaugeValue(t, metrics.waitCount, chain, network))
		assert.Equal(t, 1.5, readGaugeValue(t, metrics.waitDuration, chain, network))
	}
}

func TestCollectDBPoolStats_ReturnsErrorOnPanic(t *testing.T) {
	metrics := dbPoolStatsGauges{
		open: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "test_db_pool_open_error",
		}, []string{"chain", "network"}),
		inUse: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "test_db_pool_in_use_error",
		}, []string{"chain", "network"}),
		idle: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "test_db_pool_idle_error",
		}, []string{"chain", "network"}),
		waitCount: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "test_db_pool_wait_count_error",
		}, []string{"chain", "network"}),
		waitDuration: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "test_db_pool_wait_duration_seconds_error",
		}, []string{"chain", "network"}),
	}

	targets := []runtimeTarget{
		{
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
		},
	}

	err := collectDBPoolStats(panicDBStatsProvider{}, targets, metrics)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db pool stats collection panicked")
}

func TestStartDBPoolStatsPump_ToleratesTransientStatsFailure(t *testing.T) {
	callCh := make(chan int, 3)
	provider := &flakyDBStatsProvider{
		failUntil: 1,
		stats: sql.DBStats{
			OpenConnections: 10,
			InUse:           3,
			Idle:            7,
			WaitCount:       13,
			WaitDuration:    1500 * 1000 * 1000,
		},
		callCh: callCh,
	}
	targets := []runtimeTarget{
		{
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startDBPoolStatsPump(ctx, provider, targets, 5, slog.Default())

	timeout := time.After(200 * time.Millisecond)
	for {
		select {
		case count := <-callCh:
			if count >= 2 {
				assert.Equal(t, 10.0, readGaugeValue(t, appmetrics.DBPoolOpen, string(model.ChainSolana), string(model.NetworkDevnet)))
				cancel()
				return
			}
		case <-timeout:
			t.Fatal("timed out waiting for startup metric collection recovery")
		}
	}
}

func readGaugeValue(t *testing.T, gauge *prometheus.GaugeVec, chain string, network string) float64 {
	t.Helper()
	metricCh := make(chan prometheus.Metric, 1)
	gauge.WithLabelValues(chain, network).Collect(metricCh)

	metric := <-metricCh
	dtoMetric := &dto.Metric{}
	require.NoError(t, metric.Write(dtoMetric))

	return dtoMetric.GetGauge().GetValue()
}

package app

import (
	"context"
	"fmt"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockService records start/stop calls for testing lifecycle ordering.
type mockService struct {
	name    string
	tier    *ShutdownTier // nil means default (TierNetwork)
	startFn func(ctx context.Context) error
	stopFn  func(ctx context.Context) error
	mu      sync.Mutex
	started bool
	stopped bool
}

func (m *mockService) Name() string { return m.name }

func (m *mockService) Start(ctx context.Context) error {
	m.mu.Lock()
	m.started = true
	m.mu.Unlock()
	if m.startFn != nil {
		return m.startFn(ctx)
	}
	return nil
}

func (m *mockService) Stop(ctx context.Context) error {
	m.mu.Lock()
	m.stopped = true
	m.mu.Unlock()
	if m.stopFn != nil {
		return m.stopFn(ctx)
	}
	return nil
}

func (m *mockService) ShutdownTier() ShutdownTier {
	if m.tier != nil {
		return *m.tier
	}
	return TierNetwork
}

func newMockService(name string, order *[]string, mu *sync.Mutex) *mockService {
	return &mockService{
		name: name,
		startFn: func(_ context.Context) error {
			mu.Lock()
			*order = append(*order, "start:"+name)
			mu.Unlock()
			return nil
		},
		stopFn: func(_ context.Context) error {
			mu.Lock()
			*order = append(*order, "stop:"+name)
			mu.Unlock()
			return nil
		},
	}
}

func newMockServiceWithTier(name string, tier ShutdownTier, order *[]string, mu *sync.Mutex) *mockService {
	svc := newMockService(name, order, mu)
	svc.tier = &tier
	return svc
}

func TestApp_StartupOrder(t *testing.T) {
	t.Parallel()
	var order []string
	var mu sync.Mutex

	a := New()
	a.Register(
		newMockService("db", &order, &mu),
		newMockService("api", &order, &mu),
		newMockService("audio", &order, &mu),
	)

	require.NoError(t, a.Start(t.Context()))

	assert.Equal(t, []string{"start:db", "start:api", "start:audio"}, order)
}

func TestApp_ShutdownReverseOrder(t *testing.T) {
	t.Parallel()
	var order []string
	var mu sync.Mutex

	a := New()
	a.Register(
		newMockService("db", &order, &mu),
		newMockService("api", &order, &mu),
		newMockService("audio", &order, &mu),
	)

	require.NoError(t, a.Start(t.Context()))

	order = nil // reset to capture only shutdown order
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, a.Shutdown(ctx))

	assert.Equal(t, []string{"stop:audio", "stop:api", "stop:db"}, order)
}

func TestApp_RollbackOnStartupFailure(t *testing.T) {
	t.Parallel()
	var order []string
	var mu sync.Mutex

	failingSvc := &mockService{
		name: "failing",
		startFn: func(_ context.Context) error {
			mu.Lock()
			order = append(order, "start:failing")
			mu.Unlock()
			return fmt.Errorf("startup failed")
		},
		stopFn: func(_ context.Context) error {
			mu.Lock()
			order = append(order, "stop:failing")
			mu.Unlock()
			return nil
		},
	}

	a := New()
	a.Register(
		newMockService("db", &order, &mu),
		newMockService("api", &order, &mu),
		failingSvc,
	)

	err := a.Start(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "startup failed")

	// db and api started, then failing failed, so db and api must be stopped (reverse)
	// failing itself should NOT be stopped (it never successfully started)
	assert.Equal(t, []string{
		"start:db", "start:api", "start:failing",
		"stop:api", "stop:db",
	}, order)
}

func TestApp_TieredShutdown(t *testing.T) {
	t.Parallel()
	var order []string
	var mu sync.Mutex

	a := New()
	a.Register(
		newMockServiceWithTier("db", TierCore, &order, &mu),
		newMockServiceWithTier("api", TierNetwork, &order, &mu),
		newMockServiceWithTier("monitor", TierNetwork, &order, &mu),
	)

	require.NoError(t, a.Start(t.Context()))

	order = nil
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, a.Shutdown(ctx))

	// Network tier stops first (reverse order within tier), then core tier
	assert.Equal(t, []string{"stop:monitor", "stop:api", "stop:db"}, order)
}

func TestApp_TieredShutdown_CoreGetsGuaranteedBudget(t *testing.T) {
	t.Parallel()

	// Simulate a network service that blocks until context expires
	hangingSvc := &mockService{
		name: "hanging-api",
		stopFn: func(ctx context.Context) error {
			<-ctx.Done() // block until timeout
			return ctx.Err()
		},
	}
	hangingSvc.tier = new(ShutdownTier)
	*hangingSvc.tier = TierNetwork

	coreStopped := false
	var coreMu sync.Mutex
	coreSvc := &mockService{
		name: "db",
		stopFn: func(ctx context.Context) error {
			// Core should get a fresh context that hasn't expired
			require.NoError(t, ctx.Err(), "core service should receive a non-expired context")
			coreMu.Lock()
			coreStopped = true
			coreMu.Unlock()
			return nil
		},
	}
	coreSvc.tier = new(ShutdownTier)
	*coreSvc.tier = TierCore

	a := New()
	a.Register(coreSvc, hangingSvc)

	require.NoError(t, a.Start(t.Context()))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := a.Shutdown(ctx)
	// We expect an error from the hanging service
	require.Error(t, err)

	coreMu.Lock()
	assert.True(t, coreStopped, "core service must be stopped even when network service hangs")
	coreMu.Unlock()
}

func TestApp_ShutdownContinuesOnError(t *testing.T) {
	t.Parallel()
	var order []string
	var mu sync.Mutex

	errSvc := &mockService{
		name: "err-svc",
		stopFn: func(_ context.Context) error {
			mu.Lock()
			order = append(order, "stop:err-svc")
			mu.Unlock()
			return fmt.Errorf("stop failed")
		},
	}

	a := New()
	a.Register(
		newMockService("db", &order, &mu),
		errSvc,
	)

	require.NoError(t, a.Start(t.Context()))

	order = nil
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := a.Shutdown(ctx)

	// Shutdown should return error but still stop all services
	require.Error(t, err)
	assert.Equal(t, []string{"stop:err-svc", "stop:db"}, order)
}

func TestApp_Wait_LegacyEarlyExit(t *testing.T) {
	t.Parallel()

	a := New()
	a.Register(NewLegacyService("failing", func(quit <-chan struct{}) error {
		// Simulate init failure — return immediately without waiting for quit
		return fmt.Errorf("database open failed")
	}))

	require.NoError(t, a.Start(t.Context()))

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.Wait()
	}()

	select {
	case err := <-errCh:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "database open failed")
	case <-time.After(5 * time.Second):
		t.Fatal("Wait() did not return after legacy service early exit")
	}
}

func TestApp_Wait_ShutdownOnSignal(t *testing.T) {
	t.Parallel()
	var order []string
	var mu sync.Mutex

	a := New()
	a.Register(newMockService("svc1", &order, &mu))
	require.NoError(t, a.Start(t.Context()))

	// Run Wait in a goroutine and send SIGINT
	errCh := make(chan error, 1)
	go func() {
		errCh <- a.Wait()
	}()

	// Give Wait() time to set up signal handler
	time.Sleep(50 * time.Millisecond)

	// Send SIGINT to ourselves
	proc, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	require.NoError(t, proc.Signal(syscall.SIGINT))

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Wait() did not return after SIGINT")
	}

	mu.Lock()
	assert.Contains(t, order, "stop:svc1")
	mu.Unlock()
}

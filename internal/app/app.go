// Package app provides the top-level application lifecycle management.
package app

import (
	"context"
	"os"
	"os/signal"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const componentApp = "app"

const (
	// defaultShutdownTimeout is the total shutdown budget (9s for Docker's 10s default).
	defaultShutdownTimeout = 9 * time.Second
	// networkTierBudget is the timeout for stopping network services.
	// NOTE: In PR 1, all services (including the legacy wrapper) are TierNetwork
	// and receive the full budget. When core services are extracted in follow-up
	// PRs, this will be reduced to 6s and coreTierBudget will become 3s.
	networkTierBudget = 9 * time.Second
	// coreTierBudget is the guaranteed timeout for stopping core data services.
	// This uses a fresh context (not derived from the network tier) to guarantee
	// data safety even if network shutdown consumes its full budget.
	coreTierBudget = 3 * time.Second
)

// App is the top-level application that owns all subsystems.
type App struct {
	services   []Service
	analyzers  []Analyzer
	shutdownCh chan struct{}
	closeOnce  sync.Once
}

// New creates a new App instance.
func New() *App {
	return &App{
		shutdownCh: make(chan struct{}),
	}
}

// globalApp provides access to the App instance for the restart wiring path
// (API controller → App.RequestShutdown). Avoid using for other purposes.
var globalApp atomic.Pointer[App]

// SetGlobal stores the app instance for global access (e.g., by API handlers).
func SetGlobal(a *App) {
	globalApp.Store(a)
}

// GetGlobal returns the global app instance, or nil if not set.
func GetGlobal() *App {
	return globalApp.Load()
}

// RequestShutdown triggers a programmatic shutdown. Safe to call multiple times.
func (a *App) RequestShutdown() {
	a.closeOnce.Do(func() { close(a.shutdownCh) })
}

// Register adds services to the app in the order they should be started.
// Shutdown will happen in reverse order within each tier.
func (a *App) Register(services ...Service) {
	for _, svc := range services {
		a.services = append(a.services, svc)
		if analyzer, ok := svc.(Analyzer); ok {
			a.analyzers = append(a.analyzers, analyzer)
		}
	}
}

// Analyzers returns the registered analyzers.
func (a *App) Analyzers() []Analyzer {
	return a.analyzers
}

// Start starts all registered services in order.
// If a service fails to start, all previously started services are shut down in reverse.
func (a *App) Start(ctx context.Context) error {
	for i, svc := range a.services {
		if err := svc.Start(ctx); err != nil {
			// Roll back already-started services
			rollbackCtx, cancel := context.WithTimeout(context.Background(), networkTierBudget+coreTierBudget)
			a.shutdownRange(rollbackCtx, a.services[:i])
			cancel()
			return errors.New(err).Component(componentApp).Category(errors.CategorySystem).Context("operation", "service_start").Context("service", svc.Name()).Build()
		}
	}
	return nil
}

// Shutdown stops all services using tiered shutdown.
// TierNetwork services stop first, then TierCore services get a guaranteed fresh budget.
func (a *App) Shutdown(ctx context.Context) error {
	network, core := a.groupByTier()

	var allErrs []error

	// Tier 0: Network services (reverse order, bounded budget)
	networkCtx, networkCancel := context.WithTimeout(ctx, networkTierBudget)
	defer networkCancel()
	for _, svc := range network {
		if err := svc.Stop(networkCtx); err != nil {
			allErrs = append(allErrs, errors.New(err).Component(componentApp).Category(errors.CategorySystem).Context("operation", "service_stop").Context("service", svc.Name()).Build())
		}
	}

	// Tier 1: Core data services (reverse order, guaranteed fresh budget)
	coreCtx, coreCancel := context.WithTimeout(context.Background(), coreTierBudget)
	defer coreCancel()
	for _, svc := range core {
		if err := svc.Stop(coreCtx); err != nil {
			allErrs = append(allErrs, errors.New(err).Component(componentApp).Category(errors.CategorySystem).Context("operation", "service_stop").Context("service", svc.Name()).Build())
		}
	}

	return errors.Join(allErrs...)
}

// shutdownRange stops a slice of services in reverse order.
func (a *App) shutdownRange(ctx context.Context, services []Service) {
	for _, svc := range slices.Backward(services) {
		_ = svc.Stop(ctx) // best-effort during rollback
	}
}

// Wait blocks until a shutdown signal (SIGINT, SIGTERM) is received or the
// legacy service exits (e.g., startup failure inside the blocking function),
// then performs graceful shutdown.
func (a *App) Wait() error {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	log := getLogger()

	// Find legacy service error channel (if any) for early-exit detection.
	// If RealtimeAnalysis fails during init (e.g., database open error),
	// it returns an error into ErrChan — we must detect that instead of
	// hanging forever waiting for a signal that will never come.
	// NOTE: Only monitors the first LegacyService. Multiple legacy services
	// are not expected, but if needed this should be extended to select on all.
	var legacyErrChan <-chan error
	for _, svc := range a.services {
		if ls, ok := svc.(*LegacyService); ok {
			legacyErrChan = ls.ErrChan()
			break
		}
	}

	// Select on signal and legacy error channels. A nil legacyErrChan
	// blocks forever in select, so the branch is effectively ignored.
	var legacyErr error
	select {
	case sig := <-sigChan:
		log.Info("received shutdown signal",
			logger.String("signal", sig.String()),
			logger.String("operation", "graceful_shutdown"))
	case legacyErr = <-legacyErrChan:
		log.Info("legacy service exited",
			logger.Error(legacyErr),
			logger.String("operation", "graceful_shutdown"))
	case <-a.shutdownCh:
		log.Info("programmatic shutdown requested",
			logger.String("operation", "graceful_shutdown"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
	defer cancel()
	shutdownErr := a.Shutdown(ctx)

	// Join both errors — errors.Join returns nil if both are nil
	return errors.Join(legacyErr, shutdownErr)
}

// groupByTier splits services into network and core tiers, each in reverse registration order.
func (a *App) groupByTier() (network, core []Service) {
	for _, svc := range slices.Backward(a.services) {
		if ts, ok := svc.(TieredService); ok && ts.ShutdownTier() == TierCore {
			core = append(core, svc)
		} else {
			network = append(network, svc)
		}
	}
	return network, core
}

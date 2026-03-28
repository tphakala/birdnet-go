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
	// DatabaseService now uses TierCore with its own 3s budget, so network
	// services get 6s. Total: 6s + 3s = 9s (within Docker's 10s default).
	networkTierBudget = 6 * time.Second
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
	log := getLogger()
	for _, svc := range slices.Backward(services) {
		if err := svc.Stop(ctx); err != nil {
			// Log but continue -- best-effort during rollback
			log.Warn("service stop failed during rollback",
				logger.String("service", svc.Name()),
				logger.Error(err))
		}
	}
}

// Wait blocks until a shutdown signal (SIGINT, SIGTERM) is received or a
// programmatic shutdown is requested, then performs graceful shutdown.
func (a *App) Wait() error {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	log := getLogger()

	select {
	case sig := <-sigChan:
		log.Info("received shutdown signal",
			logger.String("signal", sig.String()),
			logger.String("operation", "graceful_shutdown"))
	case <-a.shutdownCh:
		log.Info("programmatic shutdown requested",
			logger.String("operation", "graceful_shutdown"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
	defer cancel()
	return a.Shutdown(ctx)
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

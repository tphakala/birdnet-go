# Event Bus Package

The `events` package is an asynchronous event bus. It carries error events and detection events from the code that produces them to consumers such as notifications, Sentry telemetry and alerting, without ever blocking the producer. For how the bus is built, how it is wired into the application and how deduplication works, see [ARCHITECTURE.md](ARCHITECTURE.md). This guide covers the day-to-day usage: publishing events, registering consumers, configuration and testing.

## Publishing events

### Error events

Application code does not call the bus directly. The `errors` package publishes every `EnhancedError` it builds through an `EventPublisher` installed at startup, so building an error is enough:

```go
err := errors.Newf("something went wrong").
    Component("database").
    Category(errors.CategoryDatabase).
    Build()
```

The component, category and message (plus the `operation`, `error_type` and `provider` context values when present) form the deduplication key, so give errors a stable component and category.

To publish an `ErrorEvent` yourself, use `TryPublish` on the bus. It never blocks. It returns `true` when the event was queued or suppressed as a duplicate, and `false` when it was dropped (full channel) or skipped (no consumer registered):

```go
if eb := events.GetEventBus(); eb != nil {
    accepted := eb.TryPublish(event)
    _ = accepted
}
```

### Detection events

Detection events are created with `NewDetectionEvent`, which validates its input (non-empty species names, confidence between 0 and 1) and returns an error otherwise:

```go
event, err := events.NewDetectionEvent(
    "Eurasian Blue Tit", "Cyanistes caeruleus",
    0.92, "backyard", true, 0,
)
if err != nil {
    return err
}
if eb := events.GetEventBus(); eb != nil {
    eb.TryPublishDetection(event)
}
```

Detection events are not deduplicated.

## Registering consumers

A consumer implements `EventConsumer`. The bus calls `ProcessEvent` for each error event and never batches, so `ProcessBatch` and `SupportsBatching` can be minimal:

```go
type MyConsumer struct {
    log logger.Logger
}

func (c *MyConsumer) Name() string { return "my-consumer" }

func (c *MyConsumer) ProcessEvent(event events.ErrorEvent) error {
    c.log.Debug("processing error event",
        logger.String("message", event.GetMessage()))
    return nil
}

func (c *MyConsumer) ProcessBatch(errorEvents []events.ErrorEvent) error {
    for _, event := range errorEvents {
        if err := c.ProcessEvent(event); err != nil {
            return err
        }
    }
    return nil
}

func (c *MyConsumer) SupportsBatching() bool { return false }
```

To receive detection events as well, also implement `DetectionEventConsumer` by adding `ProcessDetectionEvent(event events.DetectionEvent) error`. Registering the consumer adds it to both lists.

```go
eb := events.GetEventBus()
if eb == nil {
    return nil // event bus is not initialized, for example when disabled
}
if err := eb.RegisterConsumer(&MyConsumer{log: log}); err != nil {
    return err // for example, a consumer with this name is already registered
}
```

Consumer names must be unique. The first registration starts the worker pool. A consumer that panics is recovered and counted in `ConsumerErrors`, and an error from one consumer does not affect the others.

## Configuration

The application initializes the bus once at startup in `internal/telemetry/system_init_manager.go`. Calling `events.Initialize` again returns the existing bus, and `events.Initialize(nil)` uses `DefaultConfig()`.

```go
config := &events.Config{
    BufferSize: 10000,
    Workers:    4,
    Enabled:    true,
    Deduplication: &events.DeduplicationConfig{
        Enabled:         true,
        TTL:             5 * time.Minute,
        MaxEntries:      1000,
        CleanupInterval: 1 * time.Minute,
    },
}
eb, err := events.Initialize(config)
```

`Initialize` returns `ErrEventBusDisabled` when `Enabled` is false.

### Event bus options (`Config`)

| Option          | `DefaultConfig` | Application     | Description                                      |
| --------------- | --------------- | --------------- | ------------------------------------------------ |
| `BufferSize`    | 10000           | 10000           | Slots in each event channel (errors, detections) |
| `Workers`       | 4               | 4               | Worker goroutines                                |
| `Enabled`       | true            | true            | When false, `Initialize` returns an error        |
| `Debug`         | false           | `Debug` setting | Verbose bus logging                              |
| `Deduplication` | enabled         | enabled         | Deduplication settings, or nil to disable        |

### Deduplication options (`DeduplicationConfig`)

| Option            | `DefaultConfig` | Application     | Description                                   |
| ----------------- | --------------- | --------------- | --------------------------------------------- |
| `Enabled`         | true            | true            | Enable deduplication                          |
| `Debug`           | false           | `Debug` setting | Verbose deduplication logging                 |
| `TTL`             | 5 minutes       | 5 minutes       | How long a seen error suppresses duplicates   |
| `MaxEntries`      | 10000           | 1000            | Cache size, with least-recently-used eviction |
| `CleanupInterval` | 1 minute        | 1 minute        | How often expired entries are removed         |

## Monitoring and shutdown

```go
stats := eb.GetStats()
log.Info("event bus stats",
    logger.Any("received", stats.EventsReceived),
    logger.Any("dropped", stats.EventsDropped),
    logger.Any("consumer_errors", stats.ConsumerErrors))

dedupStats := eb.GetDeduplicationStats()

if err := eb.Shutdown(5 * time.Second); err != nil {
    // shutdown timeout exceeded
}
```

A full channel drops the new event and increments `EventsDropped`; publishers are never blocked. Events still buffered at shutdown are not guaranteed to be processed.

## Testing

```bash
task test
```

`task test` applies the project's build tags and runs with `-race`. To run only this package or its benchmarks, take the exact command from `task --dry test`, or for benchmarks:

```bash
go test -run '^$' -bench . ./internal/events
```

In tests, call `events.ResetForTesting()` to discard the global bus and the fast path flag between cases. The package's `mocks/` directory holds a generated mock of `ErrorEvent`. A test consumer in `integration_test.go` shows the full flow from error creation to consumer.

## Thread safety

All exported functions and methods are safe for concurrent use. Consumer registration is mutex protected, and the counters in `EventBusStats` are atomics.

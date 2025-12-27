# Logger Package - Complete Documentation Index

The HookRelay logger package is a production-ready, structured logging system built on Go's `log/slog` with **zero external dependencies**.

## 🚀 Quick Links

| Document | Purpose | Time to Read |
|----------|---------|--------------|
| [Quick Reference](LOGGING_QUICK_REFERENCE.md) | Syntax and examples | 5 min |
| [Implementation Guide](LOGGING_IMPLEMENTATION_GUIDE.md) | Complete usage guide | 30 min |
| [Extraction Guide](LOGGER_EXTRACTION_GUIDE.md) | How to reuse in other projects | 15 min |
| [Extraction Script](scripts/extract-logger.sh) | Automated extraction tool | 2 min |

---

## ✨ Features

- ✅ **Zero Dependencies** - Only uses Go standard library
- ✅ **Module-Aware** - Scope loggers to components (`storage`, `auth`, `api`)
- ✅ **Structured Logging** - Type-safe field constructors
- ✅ **Flexible Routing** - Console, files, or per-module files
- ✅ **Context Tracing** - Automatic trace ID extraction
- ✅ **YAML Configuration** - Easy to configure
- ✅ **Log Rotation** - SIGHUP support
- ✅ **Testable** - Interface-based design
- ✅ **Production Ready** - Used in HookRelay

---

## 📦 Package Structure

```
pkg/logger/
├── logger.go           # Interface + field constructors (168 lines)
├── slog_logger.go      # Core implementation (372 lines)
├── central_logger.go   # Module routing (459 lines)
├── config.go           # Configuration structs (42 lines)
├── multiwriter.go      # Multi-handler support (80 lines)
└── logger_test.go      # Test suite (640 lines)
```

**Total:** ~1,760 lines of self-contained, dependency-free code

---

## 🎯 Getting Started

### Option 1: Use in Another Project (Fastest)

```bash
# Extract logger to your project
./scripts/extract-logger.sh /path/to/myproject github.com/myorg/myproject

# Run the example
cd /path/to/myproject
go run example_logger.go
```

**Done in 2 minutes!** ✨

### Option 2: Learn the API (Most Thorough)

1. Read [Quick Reference](LOGGING_QUICK_REFERENCE.md) (5 min)
2. Read [Implementation Guide](LOGGING_IMPLEMENTATION_GUIDE.md) (30 min)
3. Review examples in the guide
4. Integrate into your project

### Option 3: Create Standalone Module (Reusable)

Follow [Extraction Guide - Method 2](LOGGER_EXTRACTION_GUIDE.md#-method-2-extract-as-standalone-module-recommended-for-multiple-projects)

---

## 💡 Usage Examples

### Basic Usage

```go
import "yourproject/pkg/logger"

// Create logger
cfg := &logger.LoggingConfig{
    DefaultLevel: "info",
    Timezone:     "UTC",
    Console: &logger.ConsoleOutput{Enabled: true},
}
centralLogger, _ := logger.NewCentralLogger(cfg)
defer centralLogger.Close()

// Create module-scoped logger
log := centralLogger.Module("main")

// Use structured logging
log.Info("Application started",
    logger.String("version", "1.0.0"),
    logger.Int("workers", 10))
```

### Advanced Usage

```go
// Module scoping
storageLog := centralLogger.Module("storage")
sqliteLog := storageLog.Module("sqlite")
sqliteLog.Debug("Query executed", logger.String("query", sql))

// Context-aware logging (trace IDs)
ctx := context.WithValue(ctx, "trace_id", "abc-123")
contextLog := log.WithContext(ctx)
contextLog.Info("Processing request")  // Includes trace_id

// Field accumulation
requestLog := log.With(
    logger.String("request_id", reqID),
    logger.String("user_id", userID))
requestLog.Info("Started")
requestLog.Info("Completed")  // Both include request_id and user_id
```

### Testing

```go
// Silent logger for tests
testLog := logger.NewSlogLogger(io.Discard, logger.LogLevelError, nil)
handler := NewHandler(testLog)

// Or buffer logger to assert output
buf := &bytes.Buffer{}
testLog := logger.NewSlogLogger(buf, logger.LogLevelDebug, nil)
handler := NewHandler(testLog)
// Assert buf.String() contains expected logs
```

---

## 📊 API Reference

### Logger Interface

```go
type Logger interface {
    Module(name string) Logger
    Trace(msg string, fields ...Field)
    Debug(msg string, fields ...Field)
    Info(msg string, fields ...Field)
    Warn(msg string, fields ...Field)
    Error(msg string, fields ...Field)
    With(fields ...Field) Logger
    WithContext(ctx context.Context) Logger
    Log(level LogLevel, msg string, fields ...Field)
    Flush() error
}
```

### Field Constructors

```go
logger.String(key, value string) Field
logger.Int(key string, value int) Field
logger.Int64(key string, value int64) Field
logger.Bool(key string, value bool) Field
logger.Error(err error) Field
logger.Duration(key string, value time.Duration) Field
logger.Time(key string, value time.Time) Field
logger.Any(key string, value any) Field
```

### Configuration

```yaml
logging:
  default_level: "info"  # trace, debug, info, warn, error
  timezone: "UTC"

  console:
    enabled: true
    level: "info"
    pretty: false  # true for dev, false for production

  file_output:
    enabled: true
    path: "logs/app.log"
    level: "debug"

  module_levels:
    storage: "debug"  # Override level per module
    auth: "info"

  modules:
    auth:
      enabled: true
      file_path: "logs/auth.log"  # Dedicated file
      level: "info"
      console_also: false
```

---

## 🧪 Testing

### Run Tests

```bash
cd pkg/logger
go test -v
go test -cover
```

### Coverage

```bash
go test -coverprofile=coverage.out
go tool cover -html=coverage.out
```

**Current coverage:** 90%+

---

## 🔧 Customization

### Add Custom Field Types

```go
// In logger.go
func URL(key string, value string) Field {
    u, _ := url.Parse(value)
    u.RawQuery = ""  // Redact query params
    return Field{Key: key, Value: u.String()}
}
```

### Add Custom Log Levels

```go
// In logger.go
const (
    LogLevelTrace    LogLevel = "trace"
    LogLevelDebug    LogLevel = "debug"
    LogLevelInfo     LogLevel = "info"
    LogLevelWarn     LogLevel = "warn"
    LogLevelError    LogLevel = "error"
    LogLevelCritical LogLevel = "critical"  // New level
)
```

### Add Sampling

```go
type SamplingLogger struct {
    logger Logger
    rate   int  // Log 1 in N messages
}

func (s *SamplingLogger) Info(msg string, fields ...Field) {
    if rand.Intn(s.rate) == 0 {
        s.logger.Info(msg, fields...)
    }
}
```

---

## 📚 Documentation Structure

### For Learning

1. **Start here:** [LOGGING_QUICK_REFERENCE.md](LOGGING_QUICK_REFERENCE.md)
   - Quick syntax lookup
   - Common patterns
   - Configuration examples

2. **Deep dive:** [LOGGING_IMPLEMENTATION_GUIDE.md](LOGGING_IMPLEMENTATION_GUIDE.md)
   - Architecture overview
   - Complete feature set
   - Advanced patterns
   - Best practices

### For Extraction

1. **Start here:** [LOGGER_EXTRACTION_GUIDE.md](LOGGER_EXTRACTION_GUIDE.md)
   - Portability analysis
   - Three extraction methods
   - Customization examples
   - Update strategies

2. **Quick extraction:** [scripts/extract-logger.sh](scripts/extract-logger.sh)
   - Automated extraction
   - Import path updates
   - Example generation
   - Validation

---

## 🎓 Design Patterns Used

### Dependency Injection

```go
type Handler struct {
    logger logger.Logger  // Interface, not concrete type
}

func NewHandler(log logger.Logger) *Handler {
    return &Handler{logger: log}
}
```

### Module Scoping

```go
// Hierarchical loggers
main := centralLogger.Module("main")
storage := centralLogger.Module("storage")
sqlite := storage.Module("sqlite")

// Output: module="storage.sqlite"
```

### Fluent Interface

```go
log.With(
    logger.String("request_id", reqID),
    logger.String("user_id", userID),
).WithContext(ctx).Info("Processing")
```

### Strategy Pattern

```go
// Different handlers for different outputs
type slog.Handler interface {
    Handle(context.Context, Record) error
    Enabled(context.Context, Level) bool
}
```

---

## ✅ Production Checklist

Before deploying logger in production:

- [ ] Configure appropriate log levels (info for console, debug for file)
- [ ] Set correct timezone for your region
- [ ] Create log directory with proper permissions
- [ ] Configure log rotation (logrotate or SIGHUP)
- [ ] Set up log aggregation (optional)
- [ ] Test file output and permissions
- [ ] Test SIGHUP log file reopening
- [ ] Verify structured fields parse correctly in your log viewer
- [ ] Check performance under load
- [ ] Document module names for your team

---

## 🐛 Troubleshooting

### Logs not appearing

```bash
# Check log level
cat config.yaml | grep default_level

# Check file permissions
ls -la logs/

# Test directly
go run example_logger.go
```

### Import errors

```bash
# After extraction, update all imports
find . -name "*.go" -exec sed -i '' \
  's|github.com/tphakala/hookrelay|github.com/yourorg/yourproject|g' {} +

# Tidy modules
go mod tidy
```

### Performance issues

```go
// Use sampling for high-volume logs
type SampledLogger struct {
    logger logger.Logger
    rate   int  // Log 1 in N
}

// Reduce log level in hot paths
if cfg.Debug {
    logger.Debug("verbose info")
}
```

---

## 🌟 Comparison with Other Loggers

| Feature | HookRelay Logger | logrus | zap | zerolog |
|---------|-----------------|--------|-----|---------|
| Dependencies | 0 (stdlib only) | 3+ | 2+ | 0 |
| Based on | log/slog | Custom | Custom | Custom |
| Module routing | ✅ Built-in | ❌ | ❌ | ❌ |
| Context tracing | ✅ Auto | ⚠️ Manual | ⚠️ Manual | ⚠️ Manual |
| YAML config | ✅ Full | ⚠️ Partial | ⚠️ Partial | ❌ |
| Per-module files | ✅ | ❌ | ❌ | ❌ |
| Learning curve | Low | Medium | High | Medium |
| Performance | Good | Good | Excellent | Excellent |
| Stdlib-aligned | ✅ (slog) | ❌ | ❌ | ❌ |

**Best for:**
- ✅ Projects wanting stdlib-only dependencies
- ✅ Microservices with module-based logging
- ✅ Teams wanting easy configuration
- ✅ Projects requiring per-component log routing

**Not ideal for:**
- ❌ Maximum performance critical paths (use zap/zerolog)
- ❌ Projects already using another logger (migration overhead)

---

## 📖 Real-World Usage

### In HookRelay

The logger is used throughout HookRelay:

```go
// cmd/hookrelay/main.go
centralLogger, _ := logger.NewCentralLogger(&cfg.Logging)
appLogger := centralLogger.Module("main")
storageLogger := centralLogger.Module("storage")
authLogger := centralLogger.Module("auth")

// Pass to components
storage := storagefactory.NewStorage(cfg, storageLogger)
auth := auth.NewMiddleware(cfg, authLogger)
handler := server.NewHandler(&server.HandlerConfig{
    Logger: centralLogger.Module("webhook"),
    // ...
})
```

### Log Output

```json
{"time":"2025-01-12T10:30:00Z","level":"INFO","msg":"Server started","module":"main","address":"0.0.0.0:8080","version":"1.0.0"}
{"time":"2025-01-12T10:30:01Z","level":"DEBUG","msg":"Database connected","module":"storage.sqlite","path":"data/hookrelay.db"}
{"time":"2025-01-12T10:30:05Z","level":"INFO","msg":"Webhook received","module":"webhook","trace_id":"abc-123","source":"github"}
{"time":"2025-01-12T10:30:05Z","level":"INFO","msg":"Webhook processed","module":"webhook","trace_id":"abc-123","duration":"150ms"}
```

---

## 🤝 Contributing

To improve the logger:

1. Add tests for new features
2. Update documentation
3. Maintain zero external dependencies
4. Follow existing patterns
5. Ensure backward compatibility

---

## 📝 License

The logger package is part of HookRelay and inherits its license.

When extracting to your project, you can:
- ✅ Use freely (MIT-style)
- ✅ Modify as needed
- ✅ Include in commercial projects
- ✅ Create derivative works

---

## 🎉 Summary

The HookRelay logger package is:

- **Portable**: Zero dependencies, copy anywhere
- **Powerful**: Module routing, context tracing, flexible configuration
- **Production-Ready**: Used in HookRelay, well-tested, documented
- **Easy**: 2-minute extraction, 5-minute integration
- **Flexible**: Customize for your needs

**Ready to use in your next project!** 🚀

---

## 📞 Questions?

See the documentation:
- [Quick Reference](LOGGING_QUICK_REFERENCE.md) - Syntax and examples
- [Implementation Guide](LOGGING_IMPLEMENTATION_GUIDE.md) - Complete guide
- [Extraction Guide](LOGGER_EXTRACTION_GUIDE.md) - Reuse in other projects

Or run the extraction script:
```bash
./scripts/extract-logger.sh /path/to/project github.com/org/project
```

Happy logging! 📝✨

# Detailed Review Patterns Reference

This file contains code examples and search patterns for the review agents. Each section corresponds to an agent's responsibility area.

---

# Agent 1: Reuse & Efficiency Patterns

## Abstraction Dispatch in Tight Loops

Go's generics use GC shape stenciling, not full monomorphization. Method calls on type parameters go through interface-style dispatch. In per-element hot loops, this costs 15-27% throughput.

```go
// BAD: Interface dispatch per element (274 MiB/s vs 378 MiB/s concrete)
type Hasher interface {
    Hash(v uint32) uint32
}

func Store(t *Table, data []byte, h Hasher) {
    for i := uint32(0); i+4 <= end; i++ {
        v := binary.LittleEndian.Uint32(data[i:])
        key := h.Hash(v)  // Non-inlined CALL every iteration
        t.Buckets[key] = i
    }
}

// BAD: Generic type parameter - still not inlined (320 MiB/s)
func StoreGeneric[H Hasher](t *Table, data []byte) {
    var h H
    for i := uint32(0); i+4 <= end; i++ {
        v := binary.LittleEndian.Uint32(data[i:])
        key := h.Hash(v)  // Interface-style dispatch despite generics
        t.Buckets[key] = i
    }
}

// BAD: Closure prevents inlining (322 MiB/s)
func StoreClosure(t *Table, data []byte, hash func(uint32) uint32) {
    for i := uint32(0); i+4 <= end; i++ {
        v := binary.LittleEndian.Uint32(data[i:])
        key := hash(v)  // Closure call forces register spill/reload
        t.Buckets[key] = i
    }
}

// GOOD: Concrete implementation, fully inlined (378 MiB/s)
func StoreConcrete(t *Table, data []byte) {
    for i := uint32(0); i+4 <= end; i++ {
        v := binary.LittleEndian.Uint32(data[i:])
        key := (v * HashMul32) >> (32 - BucketBits)  // Inlined arithmetic
        t.Buckets[key] = i
    }
}
```

**Why the assembly differs:** The concrete version keeps loop variables in registers throughout. Generic/interface/closure versions emit `CALL BX` each iteration, forcing the compiler to spill registers to the stack before the call and reload them after. This register trashing is the main cost, not just the indirect jump.

**When to flag:** Interface, generic, or closure dispatch inside a loop that processes data per-byte, per-element, or per-record. The abstraction must provide real value (swappable implementations used at multiple sites) to justify the overhead.

**When NOT to flag:** Abstraction called once per batch, per-request, or per-file. The 3-4ns dispatch cost is irrelevant outside tight loops.

**Search patterns:**
```bash
# Find interface method calls inside for loops (manual review needed)
grep -rn 'for.*range\|for.*:=.*;' --include="*.go" -A20 | grep '\.\(Hash\|Process\|Transform\|Encode\|Decode\)('

# Find closures passed to functions with "each/loop/process" in name
grep -rn 'func.*func(' --include="*.go"
```

---

# Agent 2: Correctness & Safety Patterns

## Potential Panics (Go)

```go
// BAD: Panic if user is nil
func GetName(user *User) string {
    return user.Name
}

// GOOD: Handle nil case
func GetName(user *User) string {
    if user == nil {
        return ""
    }
    return user.Name
}

// BAD: Panic on failed type assertion
val := m["key"].(string)

// GOOD: Check ok
val, ok := m["key"].(string)
if !ok {
    return errors.New("invalid type")
}
```

## Race Conditions

**Go:**
```go
// BAD: Race condition on counter
var counter int
for i := 0; i < 10; i++ {
    go func() { counter++ }()
}

// GOOD: Use atomic or mutex
var counter atomic.Int64
for i := 0; i < 10; i++ {
    go func() { counter.Add(1) }()
}
```

**TypeScript:**
```typescript
// BAD: Race between check and use
if (await fileExists(path)) {
    await readFile(path); // File might be deleted between check and read
}

// GOOD: Handle error at point of use
try {
    await readFile(path);
} catch (e) {
    if (e.code === 'ENOENT') { /* handle */ }
}
```

## Resource Leaks

**Go:**
```go
// BAD: Leaks file handle on error
func process(path string) error {
    f, err := os.Open(path)
    if err != nil {
        return err
    }
    data, err := io.ReadAll(f)
    if err != nil {
        return err // f never closed
    }
    f.Close()
    return nil
}

// GOOD: defer ensures cleanup
func process(path string) error {
    f, err := os.Open(path)
    if err != nil {
        return err
    }
    defer f.Close()
    // ...
}
```

**TypeScript/Svelte:**
```typescript
// BAD: Interval never cleared
$effect(() => {
    setInterval(() => update(), 1000);
});

// GOOD: Return cleanup function
$effect(() => {
    const id = setInterval(() => update(), 1000);
    return () => clearInterval(id);
});
```

## Security Issues

```go
// BAD: SQL injection
query := "SELECT * FROM users WHERE id = " + userInput
db.Query(query)

// GOOD: Parameterized query
db.Query("SELECT * FROM users WHERE id = ?", userInput)
```

```typescript
// BAD: XSS vulnerability
element.innerHTML = userInput;

// GOOD: Use textContent or sanitize
element.textContent = userInput;
```

```svelte
<!-- BAD: XSS with @html -->
{@html userInput}

<!-- GOOD: Sanitize first or avoid @html -->
{@html DOMPurify.sanitize(userInput)}
```

## Data Corruption

```go
// BAD: Partial update on error
func transfer(from, to *Account, amount int) error {
    from.Balance -= amount
    if err := saveAccount(from); err != nil {
        return err // from updated, to not updated
    }
    to.Balance += amount
    return saveAccount(to)
}

// GOOD: Use transaction
func transfer(tx *sql.Tx, from, to *Account, amount int) error {
    _, err := tx.Exec("UPDATE accounts SET balance = balance - ? WHERE id = ?", amount, from.ID)
    if err != nil {
        return err
    }
    _, err = tx.Exec("UPDATE accounts SET balance = balance + ? WHERE id = ?", amount, to.ID)
    return err
}
```

## Protocol Violations

```go
// BAD: Returns 200 with error body
func handler(w http.ResponseWriter, r *http.Request) {
    data, err := fetch()
    if err != nil {
        json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
        return
    }
    json.NewEncoder(w).Encode(data)
}

// GOOD: Set proper status code
func handler(w http.ResponseWriter, r *http.Request) {
    data, err := fetch()
    if err != nil {
        w.WriteHeader(http.StatusInternalServerError)
        json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
        return
    }
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(data)
}
```

## Shutdown Race Conditions

```go
// BAD: New clients can register between CloseAll and server.Shutdown
func (m *Manager) CloseAllClients() {
    m.mu.Lock()
    for _, c := range m.clients {
        close(c.Channel)
    }
    m.mu.Unlock()
}

// GOOD: Set shutdown flag before draining, reject late arrivals
func (m *Manager) CloseAllClients() {
    m.shuttingDown.Store(true)  // Block new registrations first
    m.mu.Lock()
    for _, c := range m.clients {
        if c.Channel != nil {  // Guard against nil channels
            close(c.Channel)
        }
    }
    m.mu.Unlock()
}

func (m *Manager) AddClient(c *Client) bool {
    if m.shuttingDown.Load() {
        return false  // Reject during shutdown
    }
    m.mu.Lock()
    defer m.mu.Unlock()
    if m.shuttingDown.Load() {  // Double-check after lock
        return false
    }
    m.clients = append(m.clients, c)
    return true
}
```

**Expected shutdown errors - suppress, don't log as real errors:**
```go
// BAD: Expected shutdown error logged as real error
if err := srv.ListenAndServe(); err != nil {
    log.Errorf("Server error: %v", err)  // Noisy on clean shutdown
}

// GOOD: Suppress expected shutdown errors
if err := srv.ListenAndServe(); err != nil &&
    !errors.Is(err, http.ErrServerClosed) &&
    !errors.Is(err, net.ErrClosed) {
    log.Errorf("Server error: %v", err)
}
```

**Review action:** For shutdown/cleanup code verify: (1) new work rejected before existing drained, (2) nil guards for channels/connections, (3) expected shutdown errors suppressed, (4) no double-close.

## Missing Context Propagation in Goroutines

```go
// BAD: Fire-and-forget goroutine with no context or panic recovery
go func() {
    resp, err := http.Get(url)  // No context, no timeout
    // ... process
}()

// GOOD: Context propagation + panic recovery
go func() {
    defer func() {
        if r := recover(); r != nil {
            log.Errorf("panic in download: %v", r)
        }
    }()
    req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
    resp, err := http.DefaultClient.Do(req)
    // ... process
}()
```

```go
// BAD: Semaphore blocks forever if context is cancelled
sem <- struct{}{}  // Blocks even after caller disconnected

// GOOD: Respect context cancellation
select {
case sem <- struct{}{}:
    // acquired
case <-ctx.Done():
    return ctx.Err()
}
```

## Sensitive Data in Telemetry

```go
// BAD: Concatenated string won't match partial error substrings
func reportFailure(err error, dbPath string) {
    scrubbed := strings.ReplaceAll(err.Error(), dbPath, "[REDACTED]")
    // dbPath = "host:3306/mydb" but error says "host:3306" - no match!
    sentry.CaptureMessage(scrubbed)
}

// GOOD: Accept variadic paths, scrub each component individually
func reportFailure(err error, paths ...string) {
    msg := err.Error()
    for _, path := range paths {
        if path != "" {
            msg = strings.ReplaceAll(msg, path, "[REDACTED]")
        }
    }
    sentry.CaptureMessage(msg)
}

// GOOD: Pass host, database, and username separately
reportFailure(err, config.Host, config.Database, config.Username)
```

**Review action:** For every telemetry/Sentry call, trace backward from the error to its source. If the error could contain connection details, file paths, or credentials, verify scrubbing covers all components individually. Check ALL callsites for the same reporting function.

## Critical TODOs

Flag all TODO comments that skip essential implementation in security-sensitive code:

```go
// CRITICAL: Auth TODO makes entire endpoint insecure
func (h *Handler) AdminEndpoint(w http.ResponseWriter, r *http.Request) {
    // TODO: verify admin token
    // TODO: check user permissions
    h.deleteAllUsers(r.Context())  // Proceeds without any auth!
}

// CRITICAL: Validation TODO allows injection
func (h *Handler) Search(query string) ([]Result, error) {
    // TODO: sanitize query input
    return h.db.Query("SELECT * FROM items WHERE name LIKE '%" + query + "%'")
}
```

```go
// CRITICAL: Transaction TODO risks data corruption
func (s *Service) TransferFunds(from, to string, amount int) error {
    // TODO: wrap in transaction
    s.db.Exec("UPDATE accounts SET balance = balance - ? WHERE id = ?", amount, from)
    s.db.Exec("UPDATE accounts SET balance = balance + ? WHERE id = ?", amount, to)
    return nil  // Partial update on failure!
}
```

**Search patterns:**
```bash
grep -rn "TODO\|FIXME\|HACK\|XXX" --include="*.go" --include="*.ts" --include="*.svelte"
```

Evaluate each: Critical (security/auth/crypto/data) must be implemented or feature removed. High (error handling/validation) should block merge. Low (optimization/refactoring) acceptable with tracking ticket.

## Common Bugs

**Go:**
```go
// BAD: Missing return after error
func handler(w http.ResponseWriter, r *http.Request) {
    if !authorized(r) {
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        // Continues execution!
    }
    doSensitiveOperation()
}

// GOOD: Return after error
func handler(w http.ResponseWriter, r *http.Request) {
    if !authorized(r) {
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return
    }
    doSensitiveOperation()
}
```

**TypeScript:**
```typescript
// BAD: find() can return undefined
const user = users.find(u => u.id === id);
console.log(user.name); // Might throw

// GOOD: Handle undefined
const user = users.find(u => u.id === id);
if (!user) {
    throw new Error('User not found');
}
console.log(user.name);
```

**Svelte 5:**
```svelte
<script>
// BAD: Destructuring breaks reactivity
let { x, y } = position; // x, y won't update

// GOOD: Access through object
let pos = $state({ x: 0, y: 0 });
// Use pos.x and pos.y
</script>
```

---

# Agent 3: Quality & Patterns Reference

## Swallowed Errors

Errors that are silently discarded, masking failures and making debugging impossible.

**Blank identifier discard:**
```go
// BAD: Error silently ignored
result, _ := riskyOperation()

// GOOD: Handle or propagate
result, err := riskyOperation()
if err != nil {
    return fmt.Errorf("risky operation failed: %w", err)
}
```

**Returning nil instead of differentiating "not found" from real errors:**
```go
// BAD: Caller can't tell if species wasn't found or if the lookup had a real error
func tryLocalTaxonomy(name string) *Species {
    species, err := db.LookupSpecies(name)
    if err != nil {
        return nil  // Masks database errors, connection failures, etc.
    }
    return species
}

// GOOD: Return the error so callers can distinguish not-found from failure
func tryLocalTaxonomy(name string) (*Species, error) {
    species, err := db.LookupSpecies(name)
    if err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return nil, nil  // Not found, no error
        }
        return nil, fmt.Errorf("taxonomy lookup: %w", err)
    }
    return species, nil
}
```

**Missing error context:**
```go
// BAD: No context about what failed
if err != nil {
    return err
}

// GOOD: Wrap with context
if err != nil {
    return fmt.Errorf("loading config from %s: %w", path, err)
}
```

**Catch-and-ignore in error handlers:**
```go
// BAD: Error caught but never acted on
if err := flush(); err != nil {
    // silently continue
}

// GOOD: At minimum, log the error
if err := flush(); err != nil {
    log.Warnf("flush failed: %v", err)
}
```

**Search patterns:**
```bash
# Find blank identifier error discards
grep -rn ', _ :=\|, _ =' --include="*.go" | grep -v "_test.go"

# Find bare error returns without wrapping
grep -rn 'return err$' --include="*.go"

# Find empty error handling blocks
grep -rn 'if err != nil {' -A1 --include="*.go" | grep -P '^\s*(//.*)?$'
```

**Review action:** For every function that returns nil/zero on error, ask: "Can the caller distinguish between a legitimate empty result and a failure?" If not, the error is being swallowed.

## Logging Quality

Logging that hinders troubleshooting (missing or swallowed), wastes CPU (hot-path spam), or obscures real problems (wrong levels, double-logging).

### Hot-Path Logging

Logging inside per-element loops burns CPU on string formatting and I/O even when the log level is disabled, if the format string is eagerly evaluated.

```go
// BAD: Logs per audio frame in the analysis loop (thousands/sec)
func (a *Analyzer) processFrames(frames []float32) {
    for i, frame := range frames {
        log.Debugf("processing frame %d: value=%.4f", i, frame)
        result := a.analyze(frame)
        if result.Score > threshold {
            log.Infof("detection at frame %d: %s (%.2f)", i, result.Species, result.Score)
        }
    }
}

// GOOD: Log once per batch with summary; individual detections logged outside the hot loop
func (a *Analyzer) processFrames(frames []float32) {
    var detections []Detection
    for _, frame := range frames {
        result := a.analyze(frame)
        if result.Score > threshold {
            detections = append(detections, result)
        }
    }
    if len(detections) > 0 {
        log.Infof("processed %d frames, %d detections", len(frames), len(detections))
    }
}
```

```go
// BAD: Debug log inside HTTP middleware per request with fmt.Sprintf
func authMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        log.Debug(fmt.Sprintf("auth check for %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr))
        // fmt.Sprintf allocates even when Debug level is off
        next.ServeHTTP(w, r)
    })
}

// GOOD: Use Debugf (lazy formatting) and only log meaningful events
func authMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if !authorize(r) {
            log.Warnf("unauthorized request: %s %s", r.Method, r.URL.Path)
            http.Error(w, "unauthorized", http.StatusUnauthorized)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

**When to flag:** Any `log.*` call inside: `for/range` loops processing data, per-request middleware on every request, callback functions invoked at high frequency, ticker/timer handlers firing sub-second.

**When NOT to flag:** Logging inside loops bounded by small, fixed-size collections (config items, registered handlers). Logging per-request at Info level in API handlers (not middleware wrapping every route).

### Double-Logging

Error logged at the point of occurrence AND propagated to the caller who also logs it. Produces duplicate log lines that obscure the real error count and waste log storage.

```go
// BAD: Error logged here AND by every caller up the chain
func loadConfig(path string) (*Config, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        log.Errorf("failed to read config: %v", err)  // Logged here
        return nil, fmt.Errorf("reading config: %w", err)  // AND propagated
    }
    return parseConfig(data)
}

func initialize() error {
    cfg, err := loadConfig("/etc/app.conf")
    if err != nil {
        log.Errorf("initialization failed: %v", err)  // Logged AGAIN
        return err
    }
    return nil
}

// GOOD: Log at the terminal handler only; intermediate functions just propagate
func loadConfig(path string) (*Config, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("reading config %s: %w", path, err)
    }
    return parseConfig(data)
}

func initialize() error {
    cfg, err := loadConfig("/etc/app.conf")
    if err != nil {
        log.Errorf("initialization failed: %v", err)  // Single log point
        return err
    }
    return nil
}
```

**Rule of thumb:** Either log or propagate, not both. Log at the boundary where the error is finally handled (returned to user, triggers retry, causes shutdown). Intermediate layers wrap with context via `fmt.Errorf("context: %w", err)`.

**Exception:** It is acceptable to log AND propagate when the caller is in a different failure domain (e.g., a background goroutine logging before sending error to a channel, since the receiver may not log it).

### Missing Error Logging at Terminal Handlers

Errors consumed without any record. The operation fails silently, making production debugging impossible.

```go
// BAD: Error consumed but never logged or reported
func (s *Server) cleanup() {
    if err := s.db.Close(); err != nil {
        // nothing - error vanishes
    }
    if err := s.cache.Flush(); err != nil {
        // also vanishes
    }
}

// GOOD: Log at the terminal point where errors are consumed
func (s *Server) cleanup() {
    if err := s.db.Close(); err != nil {
        log.Warnf("database close: %v", err)
    }
    if err := s.cache.Flush(); err != nil {
        log.Warnf("cache flush: %v", err)
    }
}
```

```go
// BAD: Goroutine error disappears
go func() {
    if err := processQueue(ctx); err != nil {
        return  // Error lost forever
    }
}()

// GOOD: Log before the goroutine exits
go func() {
    if err := processQueue(ctx); err != nil && !errors.Is(err, context.Canceled) {
        log.Errorf("queue processing failed: %v", err)
    }
}()
```

**Review action:** For every terminal error handler (goroutine exits, deferred cleanup, fire-and-forget operations), verify the error is either logged or reported. The exception is expected errors during shutdown (context.Canceled, http.ErrServerClosed) which should be suppressed.

### Wrong Log Levels

Log levels that mislead operators and break alerting thresholds.

```go
// BAD: Database connection failure at Debug level - operator will never see it
func connectDB(dsn string) (*sql.DB, error) {
    db, err := sql.Open("postgres", dsn)
    if err != nil {
        log.Debugf("database connection failed: %v", err)
        return nil, err
    }
    return db, nil
}

// BAD: Routine cache miss at Error level - triggers false alerts
func getFromCache(key string) ([]byte, error) {
    val, err := cache.Get(key)
    if err != nil {
        log.Errorf("cache miss for key %s: %v", key, err)  // This is normal operation
        return fetchFromDB(key)
    }
    return val, nil
}

// GOOD: Match severity to operational impact
func connectDB(dsn string) (*sql.DB, error) {
    db, err := sql.Open("postgres", dsn)
    if err != nil {
        log.Errorf("database connection failed: %v", err)  // Operator must act
        return nil, err
    }
    return db, nil
}

func getFromCache(key string) ([]byte, error) {
    val, err := cache.Get(key)
    if err != nil {
        log.Debugf("cache miss for %s, falling back to DB", key)  // Expected, low severity
        return fetchFromDB(key)
    }
    return val, nil
}
```

**Level guide:**
- **Error**: requires operator attention, something is broken (connection failures, data corruption, auth failures)
- **Warn**: degraded but operational (fallback activated, retry succeeded, threshold approached)
- **Info**: significant state transitions (server started, config loaded, connection established, graceful shutdown)
- **Debug**: diagnostic detail useful during troubleshooting (cache hits/misses, query timings, intermediate values)

### Format-String Overhead at Disabled Levels

```go
// BAD: fmt.Sprintf allocates even when Debug is disabled
log.Debug(fmt.Sprintf("processed %d items in %v", count, elapsed))

// BAD: String concatenation always runs
log.Debug("user " + user.ID + " performed " + action)

// GOOD: Use level-specific format function (formats only if level is enabled)
log.Debugf("processed %d items in %v", count, elapsed)

// GOOD: Guard expensive formatting
if log.IsDebugEnabled() {
    log.Debug(expensiveDebugString(state))
}
```

### Missing Structured Context

Error logs without enough context to identify which request, entity, or operation failed.

```go
// BAD: Which species? Which source? When?
func processDetection(d Detection) error {
    if err := validate(d); err != nil {
        log.Errorf("validation failed: %v", err)
        return err
    }
    return nil
}

// GOOD: Include identifying context
func processDetection(d Detection) error {
    if err := validate(d); err != nil {
        log.Errorf("validation failed for %s from %s at %s: %v",
            d.Species, d.Source, d.Timestamp.Format(time.RFC3339), err)
        return err
    }
    return nil
}
```

**Review action:** For every error log, ask: "If I saw this line in production logs at 3 AM, would I know which entity/request/operation failed without correlating other log lines?" If not, add identifying context.

**Search patterns:**
```bash
# Find logging inside for/range loops
grep -rn 'for.*range\|for.*:=.*;' --include="*.go" -A10 | grep 'log\.\(Debug\|Info\|Warn\|Error\)'

# Find fmt.Sprintf passed to log functions
grep -rn 'log\.\(Debug\|Info\|Warn\|Error\)(fmt\.Sprintf' --include="*.go"

# Find log-and-return patterns (potential double-logging)
grep -rn 'log\.\(Error\|Warn\)' --include="*.go" -A2 | grep 'return.*err'

# Find empty error handlers (errors consumed silently)
grep -rn 'if err != nil {' -A1 --include="*.go" | grep -P '^\s*(//.*)?$|^\s*return\s*$'

# Find string concatenation in log calls
grep -rn 'log\.\(Debug\|Info\|Warn\|Error\)("[^"]*" *+' --include="*.go"
```

## Test-Passing Hacks

Code designed to make tests pass rather than implementing correct behavior. The developer gamed the tests instead of solving the problem.

```go
// BAD: Hardcoded to pass test
func CalculateDiscount(amount float64) float64 {
    if amount == 100.0 {
        return 10.0 // Only handles the test case
    }
    if amount == 250.0 {
        return 25.0 // Another test case
    }
    return 0 // Everything else broken
}

// GOOD: Actual implementation
func CalculateDiscount(amount float64) float64 {
    if amount >= 200 {
        return amount * 0.10
    }
    if amount >= 100 {
        return amount * 0.05
    }
    return 0
}
```

```go
// BAD: Detects test environment
func ProcessPayment(amount float64) error {
    if os.Getenv("GO_TEST") != "" || testing.Testing() {
        return nil // Skip real logic in tests
    }
    // Actual implementation only runs in production
}
```

**Real-world example caught in review:**
```go
// BAD: Comment admits it's a test hack
func (t *Tracker) isCurrentYear(detectionTime time.Time) bool {
    detectionFiscalYear := t.getTrackingYear(detectionTime)
    // For testing purposes, when currentYear=2024...
    return detectionFiscalYear == t.currentYear || detectionFiscalYear == t.currentYear+1
}

// GOOD: Correct implementation
func (t *Tracker) isCurrentYear(detectionTime time.Time) bool {
    return t.getTrackingYear(detectionTime) == t.currentYear
}
```

**Red flags:**
1. Comments mentioning "testing" or "test purposes"
2. Suspiciously specific constants matching test assertions
3. Switch/if chains on values matching test data
4. Environment checks: `testing.Testing()`, `NODE_ENV === 'test'`, `GO_TEST`
5. Functions that only work for N inputs where N = test count
6. OR conditions that broaden acceptance (`|| extra condition`)

## Sentinel/Negative Cache Entries Treated as Valid

Cache lookups returning no error with a sentinel/negative entry, but caller only checks `err != nil`.

```go
// BAD: Negative cache entry has non-empty URL sentinel
img, err := cache.Get(name)
if err == nil && img.URL != "" {
    return "/api/v2/media/image/" + name  // Broken: sentinel URL passes
}

// GOOD: Check for negative entries explicitly
img, err := cache.Get(name)
if err == nil && !img.IsNegativeEntry() && img.URL != "" {
    return "/api/v2/media/image/" + name
}
```

**Review action:** Ask "What does a successful lookup with no real data look like?" Check ALL callsites.

## Wrong Condition Variable in Guards

```go
// BAD: Warning fires even when reconciliation cleared the backlog (21->0)
func report(totalDirty int64, reconciled int64) {
    if totalDirty >= 20 {
        log.Warn("elevated dirty count")  // Fires for 21->0 case
    }
}

// GOOD: Check the remaining count after reconciliation
func report(totalDirty int64, reconciled int64) {
    remaining := totalDirty - reconciled
    if remaining >= 20 {
        log.Warn("elevated dirty count")
    }
}
```

**Review action:** For every conditional/threshold, ask: "Am I checking the right variable at the right point in time?"

## Stale Cache Files Not Cleaned Up

```go
// BAD: Old .jpg stays on disk after re-caching as .png
func (fc *FileCache) Store(key string, data []byte, ext string) error {
    path := filepath.Join(fc.dir, key+ext)
    return os.WriteFile(path, data, 0644)
}

// GOOD: Remove old variants after storing new file
func (fc *FileCache) Store(key string, data []byte, ext string) error {
    path := filepath.Join(fc.dir, key+ext)
    if err := os.WriteFile(path, data, 0644); err != nil {
        return err
    }
    for _, oldExt := range knownExtensions {
        if oldExt != ext {
            os.Remove(filepath.Join(fc.dir, key+oldExt))
        }
    }
    return nil
}
```

## Incomplete Multi-Site Fixes

The #1 source of review comments that result in actual fixes. A fix is applied to one callsite but the same pattern exists at other callsites.

**Review action:** After finding/fixing any pattern, immediately search the entire codebase:

```bash
# After fixing one thumbnail URL site, find ALL thumbnail URL sites
grep -rn "media/image\|getThumbnailURL\|thumbnailURL" --include="*.go"

# After fixing one reportInitFailure callsite, find ALL callsites
grep -rn "reportInitFailure\|reportStartupError" --include="*.go"

# After adding a sentinel check, find ALL cache.Get consumers
grep -rn "\.Get(scientificName\|\.Get(species\|BirdImageCache\.Get" --include="*.go"
```

Flag as High when a fix is applied to fewer than all matching callsites.

## Tests Not Exercising the Fixed Code Path

```go
// BAD: DurationSec:0 takes the instant path, never exercises sustained logic
func TestMetricPathIsolation(t *testing.T) {
    rule := AlertRule{DurationSec: 0}  // Bypasses the fixed code path!
}

// GOOD: Use DurationSec that exercises the sustained evaluation path
func TestMetricPathIsolation_Sustained(t *testing.T) {
    rule := AlertRule{DurationSec: 60}  // Forces through metricBufferKey()
}
```

**Review action:** For every test added alongside a bug fix, verify: (1) would FAIL on old code, (2) exercises the exact fixed path, (3) name accurately describes what it verifies.

## Inconsistent Guard Patterns

```go
// BAD: Inconsistent - SetTag mutates scope even when telemetry is disabled
func ReportEvent(msg string) {
    if shouldSkipTelemetry() { return }  // Has guard
    sentry.CaptureMessage(msg)
}

func SetSchemaTag(version string) {
    // Missing guard! Mutates global scope even with telemetry off
    sentry.ConfigureScope(func(scope *sentry.Scope) {
        scope.SetTag("schema", version)
    })
}

// GOOD: Consistent guards across all public functions
func SetSchemaTag(version string) {
    if shouldSkipTelemetry() || version == "" { return }
    sentry.ConfigureScope(func(scope *sentry.Scope) {
        scope.SetTag("schema", version)
    })
}
```

**Review action:** When a function has a guard/precondition, check all siblings in the same file/package for the same guard.

## Go API Design Smells

**Exported function returning unexported type:**
```go
// BAD: External packages can't name or store the return type
func GetPushDispatcher() *pushDispatcher { return globalPushDispatcher }

// GOOD: Export the type, return an interface, or unexport the function
func GetPushDispatcher() *PushDispatcher { return globalPushDispatcher }
```

**Context not first parameter:**
```go
// BAD
func handleRequestError(err error, ctx context.Context) error {

// GOOD
func handleRequestError(ctx context.Context, err error) error {
```

**Dead code branches:**
```go
// BAD: debugMode check is dead - returns true either way
func shouldShowNotification(n Notification, debugMode bool) bool {
    if excludeToasts && isToast(n) {
        return false
    }
    if debugMode {
        return true  // Same as unconditional return below
    }
    return true
}
```

**Naming mismatches:**
```go
// BAD: "get" prefix but mutates a pointer parameter
func getUnixMetadata(metadata *FileMetadata, info os.FileInfo) {
    metadata.UID = int(stat.Uid)
}

// GOOD: Name reflects the mutation
func populateUnixMetadata(metadata *FileMetadata, info os.FileInfo) {
    metadata.UID = int(stat.Uid)
}
```

## Documentation Contradicting Code

```go
// BAD: Comment says the opposite of what the code does
// daylightFilterAll: when true, filter ALL species during daytime
// (but an empty species list actually means "filter nothing")
if len(config.DaylightFilter.Species) == 0 {
    return false // Doesn't filter anything - comment is wrong
}
```

```svelte
<!-- BAD: Help text contradicts actual behavior -->
<p class="help-text">{$t('settings.daylightFilter.addSpeciesHelp')}</p>
<!-- "An empty list filters ALL species during the day" -->
<!-- But backend: empty list = no filtering at all -->
```

**Review action:** For every user-facing help text, comment, or README, trace to the actual code path and verify the described behavior matches.

## Frontend: Partial Settings/Config Objects

```typescript
// BAD: Backend may not have all properties if config was created before this feature
let local = $derived($settings?.extendedCapture ?? {});
// local.maxDuration is undefined if backend only has { enabled: true }

// GOOD: Merge over complete defaults
const defaults = { enabled: false, maxDuration: 60, species: [] };
let local = $derived({
    ...defaults,
    ...($settings?.extendedCapture ?? {})
});

// GOOD: Validate array fields
let species = $derived(
    Array.isArray($settings?.extendedCapture?.species)
        ? $settings.extendedCapture.species
        : []
);
```

**Review action:** When new settings/config fields are added, verify the frontend handles existing saved configs missing the new fields.

## Frontend: daisyUI Class Usage

This project uses native Tailwind v4.1 only. Any daisyUI class is a code smell.

**Common daisyUI classes to flag:** `btn`, `btn-primary`, `btn-secondary`, `btn-ghost`, `btn-outline`, `card`, `card-body`, `modal`, `drawer`, `navbar`, `footer`, `hero`, `alert`, `badge`, `tooltip`, `dropdown`, `menu`, `tabs`, `toggle`, `checkbox`, `input`, `select`, `textarea`, `avatar`, `indicator`, `mask`, `artboard`, `stack`, `divider`, `btn-lg`, `btn-sm`, `btn-xs`, `glass`, `loading`, `no-animation`, `btn-info`, `btn-success`, `btn-warning`, `btn-error`, `alert-info`, `alert-success`, `alert-warning`, `alert-error`

```svelte
<!-- BAD: daisyUI classes -->
<button class="btn btn-primary">Submit</button>
<div class="card card-body">Content</div>

<!-- GOOD: Native Tailwind v4.1 -->
<button class="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors">Submit</button>
<div class="p-6 bg-white rounded-xl shadow-md">Content</div>
```

**Search pattern:**
```bash
grep -rn "btn-\|card-body\|alert-\|badge\|modal\|drawer\|navbar\|tooltip\|dropdown\|menu\|tabs\|toggle\|avatar" --include="*.svelte"
```

## Frontend: Malformed Tailwind CSS Variable Classes

Automated find-and-replace of color tokens can produce broken classes. The suffix ends up outside the `[var(...)]` brackets, creating a class Tailwind silently ignores.

```svelte
<!-- BAD: Suffix outside brackets - element unstyled -->
<span class="text-[var(--color-neutral)]-content">Text</span>

<!-- GOOD: Suffix inside the CSS variable brackets -->
<span class="text-[var(--color-neutral-content)]">Text</span>
```

**Search pattern:**
```bash
grep -rn '\[var(--[^]]*)\]-' --include="*.svelte"
```

## Frontend: Binding to Read-Only $derived Values

`$derived` is read-only in Svelte 5. Writes throw runtime errors.

```svelte
<script>
// BAD: bind:checked on $derived - runtime error on toggle
let settings = $state({ enabled: true });
let enabled = $derived(settings.enabled);
</script>
<input type="checkbox" bind:checked={enabled} />
<!-- Runtime error: Cannot write to $derived -->

<script>
// GOOD: Bind to the source $state
let settings = $state({ enabled: true });
</script>
<input type="checkbox" bind:checked={settings.enabled} />
```

## Frontend: Invalid Svelte 5 Reactivity Patterns

**1. Destructuring $state breaks reactivity:**
```svelte
<script>
// BAD
let state = $state({ count: 0, name: 'test' });
let { count, name } = state; // count and name won't update!

// GOOD: Access through the object
</script>
<p>{state.count}</p>
```

**2. Using $effect for derived values:**
```svelte
<script>
// BAD: Creates unnecessary effect cycle
let count = $state(0);
let doubled = $state(0);
$effect(() => { doubled = count * 2; });

// GOOD
let count = $state(0);
let doubled = $derived(count * 2);
</script>
```

**3. Missing $effect cleanup:**
```svelte
<script>
// BAD: Memory leak
$effect(() => {
    const id = setInterval(() => console.log('tick'), 1000);
});

// GOOD
$effect(() => {
    const id = setInterval(() => console.log('tick'), 1000);
    return () => clearInterval(id);
});
</script>
```

**4. Svelte 4 $: syntax (not valid in Svelte 5):**
```svelte
<script>
// BAD
$: doubled = count * 2;

// GOOD
let doubled = $derived(count * 2);
</script>
```

**5. Mutating $state.raw:**
```svelte
<script>
// BAD: Won't trigger updates
let data = $state.raw({ nested: { value: 1 } });
data.nested.value = 2; // Silent failure

// GOOD: Reassign entire object
data = { nested: { value: 2 } };
</script>
```

**Search patterns:**
```bash
# Svelte 4 reactive statements
grep -rn '^\s*\$:' --include="*.svelte"

# Potential destructuring of state
grep -rn 'let.*{.*}.*=.*\$state\|const.*{.*}.*=.*\$state' --include="*.svelte"

# $effect used for assignments
grep -rn '\$effect.*=.*;' --include="*.svelte"
```

## Frontend: Duplicate Keys in Lists and Dropdowns

Non-unique display fields used as keys cause silent UI failures. Common with hardware enumeration (audio/video devices), users with same names, items sharing labels.

```svelte
<!-- BAD: name is not unique - each_key_duplicate crash, dropdown empty -->
<select bind:value={selectedDevice}>
    {#each devices as device (device.name)}
        <option value={device.name}>{device.name}</option>
    {/each}
</select>

<!-- GOOD: Use unique ID as key and value -->
<select bind:value={selectedDeviceId}>
    {#each devices as device (device.id)}
        <option value={device.id}>{device.name}</option>
    {/each}
</select>
```

```typescript
// BAD: Silently drops items with duplicate names
const byName = new Map(items.map(i => [i.name, i]));

// GOOD: Key by unique ID
const byId = new Map(items.map(i => [i.id, i]));
```

**Search patterns:**
```bash
# {#each} with potentially non-unique keys
grep -rn '{#each.*(\w\+\.\(name\|label\|title\|text\))' --include="*.svelte"

# <option> using name/label as value
grep -rn 'value={\w\+\.\(name\|label\|title\)}' --include="*.svelte"
```

## Frontend: Hardcoded User-Facing Strings

All user-facing text must use the i18n library. Flag: button labels, form labels/placeholders, error/success messages, headings, tooltips, modal content, empty states, confirmation dialogs.

```svelte
<!-- BAD -->
<button>Submit</button>
<label>Email Address</label>
<input placeholder="Enter your email" />

<!-- GOOD -->
<button>{$t('common.submit')}</button>
<label>{$t('form.email_label')}</label>
<input placeholder={$t('form.email_placeholder')} />
```

**Exceptions:** Brand/product names, technical identifiers, URLs, debug output, API-provided data.

**Search patterns:**
```bash
# Hardcoded strings in button/label elements
grep -rn '<button>[^{<]*[A-Za-z][^{<]*</button>\|<label>[^{<]*[A-Za-z]' --include="*.svelte"

# Hardcoded placeholder attributes
grep -rn 'placeholder="[^"$]*[A-Za-z]' --include="*.svelte"

# Hardcoded title/aria-label
grep -rn 'title="[^"$]*[A-Za-z]\|aria-label="[^"$]*[A-Za-z]' --include="*.svelte"
```

---

# Agent 4: i18n Translation Integrity Patterns

Translation files live at `frontend/static/messages/`. `en.json` is the source of truth. All 14 non-English files must mirror its key structure exactly with properly translated values.

## Flattening JSON to Dot-Notation

Use `jq` to extract all scalar key paths and values from a translation file:

```bash
# Flatten to key=value pairs (one per line)
jq -r 'paths(scalars) as $p | "\($p | join("."))=\(getpath($p))"' en.json

# Extract just the key paths (for set comparison)
jq -r '[paths(scalars)] | .[] | join(".")' en.json | sort > /tmp/en_keys.txt
```

## Check 1: Missing Keys (Gaps)

Keys present in `en.json` but absent from a locale file. Every gap means the UI falls back to the key name or shows nothing.

```bash
cd frontend/static/messages

# Generate sorted key lists
jq -r '[paths(scalars)] | .[] | join(".")' en.json | sort > /tmp/en_keys.txt

for lang in cs da de es fi fr hu it lv nl pl pt sk sv; do
  jq -r '[paths(scalars)] | .[] | join(".")' "${lang}.json" | sort > "/tmp/${lang}_keys.txt"
  missing=$(comm -23 /tmp/en_keys.txt "/tmp/${lang}_keys.txt")
  if [ -n "$missing" ]; then
    echo "=== ${lang}.json: MISSING KEYS ==="
    echo "$missing"
  fi
done
```

**Example finding:**
```
=== fi.json: MISSING KEYS ===
wizard.steps.locationLanguage.geolocationRequiresHttps
wizard.steps.locationLanguage.geolocationDenied
settings.audio.newFieldAddedLastWeek
```

Every missing key must be flagged. There are no acceptable gaps.

## Check 2: English Placeholders (Untranslated Values)

Values in non-English files identical to the English value. English placeholders are the most common i18n defect: a developer adds keys to all files but copies the English text instead of translating.

```bash
cd frontend/static/messages

for lang in cs da de es fi fr hu it lv nl pl pt sk sv; do
  echo "=== ${lang}.json: ENGLISH PLACEHOLDERS ==="
  # Compare values at matching key paths
  jq -r 'paths(scalars) as $p | "\($p | join("."))=\(getpath($p))"' en.json | sort > /tmp/en_kv.txt
  jq -r 'paths(scalars) as $p | "\($p | join("."))=\(getpath($p))"' "${lang}.json" | sort > "/tmp/${lang}_kv.txt"
  # Find identical lines (same key, same value)
  comm -12 /tmp/en_kv.txt "/tmp/${lang}_kv.txt" | grep -v '^\s*$'
done
```

**Example finding:**
```
=== de.json: ENGLISH PLACEHOLDERS ===
common.loading=Loading...
settings.audio.captureLength=Capture Length
wizard.steps.locationLanguage.geolocationFailed=Geolocation failed
```

**What to flag:** Any non-English value that exactly matches the English value.

**Narrow exceptions (do NOT flag these):**
- Single characters or pure numbers (`/`, `0`, `1`)
- Format tokens that contain only placeholders (`{0}`, `{count}`, `{name}`)
- Unit abbreviations that are universal (`dB`, `Hz`, `kHz`, `ms`, `MB`, `GB`)
- Protocol or technical identifiers (`SSE`, `RTSP`, `MQTT`, `HTTP`, `API`)
- The literal string `OK`

**Not exceptions (MUST flag):**
- Multi-word English phrases, even if they contain a technical term (`Capture Length`, `Audio Settings`)
- Brand names used as part of a sentence (`Powered by BirdNET-Go`)
- Strings that happen to be short but are real English words (`Save`, `Delete`, `Loading...`, `Error`, `Retry`)
- Mixed strings with placeholders (`{count} detections found`)

The rule is simple: if a human reading the locale file would see English text, it needs a real translation. There is no "this English word is close enough in German/Finnish/etc." exception.

## Check 3: Orphaned Keys

Keys present in a non-English file but absent from `en.json`. These are leftovers from deleted features or key renames.

```bash
cd frontend/static/messages

for lang in cs da de es fi fr hu it lv nl pl pt sk sv; do
  orphaned=$(comm -13 /tmp/en_keys.txt "/tmp/${lang}_keys.txt")
  if [ -n "$orphaned" ]; then
    echo "=== ${lang}.json: ORPHANED KEYS ==="
    echo "$orphaned"
  fi
done
```

Orphaned keys are lower severity than gaps or placeholders, but should still be flagged for cleanup.

## Check 4: Structural Mismatches

A key path that is an object (has children) in one file but a scalar (leaf value) in another. This breaks the i18n library at runtime.

```bash
cd frontend/static/messages

# Get object paths from en.json
jq -r '[paths | select(length > 0)] | map(join(".")) | .[]' en.json | sort -u > /tmp/en_all_paths.txt
jq -r '[paths(scalars)] | .[] | join(".")' en.json | sort > /tmp/en_scalar_paths.txt
comm -23 /tmp/en_all_paths.txt /tmp/en_scalar_paths.txt | sort > /tmp/en_object_paths.txt

for lang in cs da de es fi fr hu it lv nl pl pt sk sv; do
  jq -r '[paths(scalars)] | .[] | join(".")' "${lang}.json" | sort > "/tmp/${lang}_scalar.txt"
  # Flag: path is an object in en.json but a scalar in this file (or vice versa)
  mismatches=$(comm -12 /tmp/en_object_paths.txt "/tmp/${lang}_scalar.txt")
  if [ -n "$mismatches" ]; then
    echo "=== ${lang}.json: STRUCTURAL MISMATCH (object in en.json, scalar here) ==="
    echo "$mismatches"
  fi
done
```

**Example:**
```
en.json:   "common": { "actions": { "download": "Download" } }
fi.json:   "common": { "actions": "Toiminnot" }
                        ^^^^^^^^^ scalar where en.json has an object
```

This is always a Critical finding since the i18n library will throw when traversing into a scalar expecting an object.

## Severity Classification

| Finding | Severity |
|---------|----------|
| Structural mismatch | **Critical** - runtime error |
| Missing keys (gaps) | **High** - broken UI strings |
| English placeholders | **High** - untranslated UI |
| Orphaned keys | **Low** - dead code, cleanup |

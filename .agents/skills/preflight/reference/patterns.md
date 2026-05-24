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

## Nil Guards on Stored Function Fields

Structs that accept function-typed fields (closures, callbacks, check functions) via constructors must nil-check them before calling. Current callers may always provide non-nil, but future callers, test code, or partial initialization paths may not.

```go
// BAD: Panics if checkFn was never set
type HealthCheck struct {
    name    string
    checkFn func() (bool, string)
}

func (h *HealthCheck) Run() HealthResult {
    ok, msg := h.checkFn()  // Panic if checkFn is nil
    return HealthResult{Name: h.name, Healthy: ok, Message: msg}
}

// GOOD: Nil guard before calling stored function
func (h *HealthCheck) Run() HealthResult {
    if h.checkFn == nil {
        return HealthResult{Name: h.name, Healthy: false, Message: "check function not configured"}
    }
    ok, msg := h.checkFn()
    return HealthResult{Name: h.name, Healthy: ok, Message: msg}
}
```

```go
// BAD: Multiple stored functions, any could be nil
type Monitor struct {
    collectFn  func() []Metric
    reportFn   func([]Metric) error
    cleanupFn  func()
}

func (m *Monitor) Execute() error {
    metrics := m.collectFn()     // Panic
    err := m.reportFn(metrics)   // Panic
    m.cleanupFn()                // Panic
    return err
}

// GOOD: Guard each function call
func (m *Monitor) Execute() error {
    if m.collectFn == nil {
        return nil
    }
    metrics := m.collectFn()
    if m.reportFn != nil {
        if err := m.reportFn(metrics); err != nil {
            return err
        }
    }
    if m.cleanupFn != nil {
        m.cleanupFn()
    }
    return nil
}
```

**When to flag:** Any struct that stores `func(...)` fields and calls them in methods without nil checks. Especially important for exported types where callers outside the package control construction.

**When NOT to flag:** Function fields set in the same unexported constructor that always provides non-nil, with no other construction path. But still prefer the guard for defense in depth.

**Search patterns:**
```bash
# Find structs with function-typed fields
grep -rn 'func(' --include="*.go" | grep -v '_test.go' | grep '^\s\+\w\+\s\+func('

# Find method calls on struct fields that look like functions
grep -rn '\.\(checkFn\|handler\|callback\|onEvent\|reportFn\|collectFn\)(' --include="*.go"
```

## Constructor Input Validation

Constructors that accept size, capacity, or count parameters must validate the input. Zero or negative values can cause division-by-zero panics, infinite loops, or silent misbehavior.

```go
// BAD: Division by zero when maxSize is 0
type RingBuffer struct {
    data    []Entry
    maxSize int
    pos     int
}

func NewRingBuffer(maxSize int) *RingBuffer {
    return &RingBuffer{
        data:    make([]Entry, 0, maxSize),
        maxSize: maxSize,
    }
}

func (rb *RingBuffer) Add(e Entry) {
    rb.pos = (rb.pos + 1) % rb.maxSize  // Panic: division by zero
    rb.data[rb.pos] = e
}

// GOOD: Validate and enforce minimum
func NewRingBuffer(maxSize int) *RingBuffer {
    if maxSize <= 0 {
        maxSize = 64
    }
    return &RingBuffer{
        data:    make([]Entry, 0, maxSize),
        maxSize: maxSize,
    }
}
```

```go
// BAD: Negative limit causes unexpected behavior
func (q *Queue) Drain(limit int) []Item {
    var out []Item
    for i := 0; i < limit; i++ {  // Negative limit: loop never runs (silent no-op)
        item, ok := q.Pop()
        if !ok {
            break
        }
        out = append(out, item)
    }
    return out
}

// GOOD: Guard against invalid input
func (q *Queue) Drain(limit int) []Item {
    if limit <= 0 {
        return nil
    }
    // ...
}
```

**Search patterns:**
```bash
# Find constructors with size/capacity parameters
grep -rn 'func New.*\(.*\(size\|capacity\|max\|limit\|count\|bufSize\)' --include="*.go"

# Find modulo operations (potential division by zero)
grep -rn '%\s*[a-z]' --include="*.go" | grep -v '_test.go'
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

## Shallow Copy of Reference Types in Concurrent Collections

When storing structs that contain maps or slices into concurrent-safe collections, a shallow copy leaves reference types shared between caller and buffer. This creates a data race: the caller can mutate the map/slice after Add() returns, corrupting the stored entry.

```go
// BAD: Fields map shared between caller and buffer
type LogEntry struct {
    Message string
    Fields  map[string]string
}

type ErrorRingBuffer struct {
    mu      sync.Mutex
    entries []LogEntry
}

func (rb *ErrorRingBuffer) Add(entry LogEntry) {
    rb.mu.Lock()
    defer rb.mu.Unlock()
    rb.entries = append(rb.entries, entry)  // Shallow copy: Fields map is shared
}

// Caller can still mutate entry.Fields after Add returns, corrupting the buffer

// GOOD: Deep copy reference-typed fields on store
func (rb *ErrorRingBuffer) Add(entry LogEntry) {
    rb.mu.Lock()
    defer rb.mu.Unlock()
    stored := LogEntry{
        Message: entry.Message,
        Fields:  maps.Clone(entry.Fields),
    }
    rb.entries = append(rb.entries, stored)
}
```

```go
// BAD: Returning stored struct exposes internal map to caller mutation
func (rb *ErrorRingBuffer) Recent(n int) []LogEntry {
    rb.mu.Lock()
    defer rb.mu.Unlock()
    return rb.entries[len(rb.entries)-n:]  // Caller gets direct reference
}

// GOOD: Clone on read too
func (rb *ErrorRingBuffer) Recent(n int) []LogEntry {
    rb.mu.Lock()
    defer rb.mu.Unlock()
    start := len(rb.entries) - n
    if start < 0 {
        start = 0
    }
    out := make([]LogEntry, len(rb.entries)-start)
    for i, e := range rb.entries[start:] {
        out[i] = LogEntry{
            Message: e.Message,
            Fields:  maps.Clone(e.Fields),
        }
    }
    return out
}
```

**When to flag:** Any Add/Put/Store method on a mutex-protected or channel-based collection where the stored type contains map or slice fields. Also flag accessor methods (Get/Recent/All) that return stored structs with reference fields without cloning.

**Search patterns:**
```bash
# Find structs with map fields stored in concurrent collections
grep -rn 'map\[' --include="*.go" | grep -v '_test.go' | grep 'struct'

# Find Add/Store methods on mutex-protected types
grep -rn 'func.*Add\|func.*Store\|func.*Put' --include="*.go" | grep -v '_test.go'

# Find types with both mutex and slice/map fields
grep -rn 'sync.Mutex\|sync.RWMutex' -l --include="*.go" | xargs grep -l 'map\[\|^\s*\w\+\s\+\[\]'
```

## Context Lifecycle Through Embedded Structs

Context fields on embedded structs may be modified by methods on either the parent or the embedded struct. A goroutine capturing a context set by the parent can be silently killed when an embedded struct's lifecycle method cancels and replaces that context.

```go
// BAD: Open() initializes monitoringCtx, then goroutine captures it.
// But StartMonitoring() (on embedded DataStore) cancels and replaces monitoringCtx.
// The goroutine's context is cancelled shortly after Open() returns.

type DataStore struct {
    monitoringCtx    context.Context
    monitoringCancel context.CancelFunc
}

func (ds *DataStore) StartMonitoring() {
    if ds.monitoringCancel != nil {
        ds.monitoringCancel()  // Cancels the context from Open()!
    }
    ds.monitoringCtx, ds.monitoringCancel = context.WithCancel(context.Background())
    // ...
}

type SQLiteStore struct {
    DataStore  // Embedded struct
}

func (s *SQLiteStore) Open() error {
    // Gate suggested: initialize context before integrity check
    s.monitoringCtx, s.monitoringCancel = context.WithCancel(context.Background())

    if err := s.performStartupIntegrityCheck(); err != nil {
        // Goroutine captures s.monitoringCtx for deferred notification
        go s.notifyCorruptionDeferred(s.monitoringCtx, err)
    }

    return nil
    // After Open() returns, caller calls s.StartMonitoring()
    // which cancels s.monitoringCtx, killing the goroutine above
}

// GOOD: Use a dedicated context for the goroutine, independent of struct state
func (s *SQLiteStore) Open() error {
    if err := s.performStartupIntegrityCheck(); err != nil {
        // Own context with timeout, not shared struct field
        notifyCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        go func() {
            defer cancel()
            s.notifyCorruptionDeferred(notifyCtx, err)
        }()
    }
    return nil
}
```

**When to flag:** Any code that:
1. Sets a context field on a struct, then captures that field in a goroutine
2. Uses a context field from an embedded struct without checking who else writes it
3. Embeds a struct with lifecycle methods that cancel/replace context fields

**Review action:** When code reads or writes a `context.Context` field on a struct:
1. `grep -rn 'fieldName\s*=' pkg/` to find ALL assignments
2. Check if any embedded struct method modifies the same field
3. Check if any goroutine captures the field (will it survive lifecycle transitions?)

**Search patterns:**
```bash
# Find all context field assignments in a package
grep -rn 'monitoringCtx\s*=' internal/datastore/
grep -rn 'Ctx\s*=\|ctx\s*=' --include="*.go" | grep -v '_test.go' | grep 'context\.'

# Find embedded structs
grep -rn 'type.*struct {' -A5 --include="*.go" | grep '^\s\+\w\+\s*$\|^\s\+\*\?\w\+\s*$'

# Find lifecycle methods on embedded types
grep -rn 'func.*Start\|func.*Stop\|func.*Close\|func.*Open\|func.*Init\|func.*Monitor' --include="*.go"
```

## Fix-Introduces-Bug Pattern

When a gate agent suggests a fix that touches shared state (contexts, mutexes, atomics, channels), the fix itself may introduce a new bug through interactions with code the agent didn't examine. The fix is superficially correct in isolation but wrong in the context of the full system.

```go
// SCENARIO: Gate finds that a goroutine uses an uninitialized context.
// Gate suggests: "Initialize the context before launching the goroutine."
//
// The fix looks correct in sqlite_integrity.go:
//   s.monitoringCtx, s.monitoringCancel = context.WithCancel(context.Background())
//   go s.notifyCorruptionDeferred(s.monitoringCtx, err)
//
// But in monitoring.go (not reviewed by the agent):
//   func (ds *DataStore) StartMonitoring() {
//       ds.monitoringCancel()  // Cancels the context we just set!
//       ds.monitoringCtx, ds.monitoringCancel = context.WithCancel(...)
//   }
//
// The fix created a race: the goroutine's context is cancelled
// 50ms after Open() returns, when StartMonitoring() is called.
```

**Verification steps after any fix touching shared state:**
1. Identify the shared state the fix touches (context, mutex, channel, atomic, map, global)
2. `grep -rn 'stateName' pkg/` to find ALL readers and writers
3. For each writer: does it conflict with the fix? (cancel, replace, close, reset)
4. For each reader: does it assume a state the fix changes?
5. If the state is on an embedded struct: check the embedded struct's methods too

**When to flag:** Any suggested fix that:
- Initializes or reassigns a struct field that other methods also write
- Adds a goroutine that captures struct state
- Modifies shutdown/cleanup ordering
- Changes when a resource is created or destroyed

**This pattern is a process check, not a code pattern.** It applies during Phase 3 fix verification, not during the initial review.

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

## Non-DST-Safe Time Arithmetic

Using `time.Duration` multiplication for calendar-day offsets loses or gains an hour near DST transitions. A "day" is not always 24 hours.

```go
// BAD: Loses an hour on spring-forward, gains on fall-back
cutoff := time.Now().Add(-30 * 24 * time.Hour)

// BAD: Same problem with named constant
const thirtyDays = 30 * 24 * time.Hour
cutoff := time.Now().Add(-thirtyDays)

// GOOD: DST-safe calendar arithmetic
cutoff := time.Now().AddDate(0, 0, -30)

// GOOD: When you genuinely need exactly 24*N hours (rare - e.g., cache TTL)
expiry := time.Now().Add(24 * time.Hour) // 24h TTL is intentionally clock-hours
```

**When to flag:** Any `N * 24 * time.Hour` (or `N * time.Hour * 24`) used to compute a date boundary, retention window, or "days ago" cutoff. These should use `AddDate(0, 0, -N)`.

**When NOT to flag:** Genuine duration-based timeouts (cache TTL, request timeout, heartbeat interval) where you want exactly N hours regardless of calendar.

**Search patterns:**
```bash
# Find day-level duration arithmetic
grep -rn '24 \* time\.Hour\|time\.Hour \* 24' --include="*.go" | grep -v '_test.go'

# Find patterns like N * 24 * time.Hour
grep -rn '[0-9]\+ \* 24 \* time\.Hour' --include="*.go"
```

## Event Emission Ordering (Query-Before-Emit)

In event-sourced patterns, emitting a new event before querying for the previous one creates a race: you may read back your own event instead of the prior state.

```go
// BAD: Race - may read back your own event as the "previous" one
func (s *Service) RecordDeployment(ctx context.Context) error {
    s.events.Emit(ctx, "deploy", "started", metadata)
    prev, err := s.repo.GetLatestEvent("deploy", "started")
    // prev might be the one we just emitted!
    if prev != nil {
        s.logTimeSinceLast(prev.Timestamp)
    }
    return err
}

// GOOD: Query first, then emit
func (s *Service) RecordDeployment(ctx context.Context) error {
    prev, err := s.repo.GetLatestEvent("deploy", "started")
    if err != nil {
        return fmt.Errorf("querying previous deployment: %w", err)
    }
    s.events.Emit(ctx, "deploy", "started", metadata)
    if prev != nil {
        s.logTimeSinceLast(prev.Timestamp)
    }
    return nil
}
```

**When to flag:** Any code path that (1) emits/inserts an event, then (2) queries for the "latest" or "previous" event of the same type. The query must come before the emit.

**Search patterns:**
```bash
# Find emit-then-query patterns (look for Emit followed by Get/Find/Query on same entity)
grep -rn 'Emit\|EmitEvent\|PublishEvent' --include="*.go" -A10 | grep -i 'latest\|previous\|last\|recent'

# Find event insertion followed by query
grep -rn 'Create\|Insert\|Save.*Event' --include="*.go" -A10 | grep -i 'GetLatest\|FindLast\|MostRecent'
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

## Stub/Placeholder Return Values Causing Misleading Status

When wiring stub or placeholder functions into a system that interprets return values as health/status signals, a stub returning "failure" causes permanent degraded status for users with that feature enabled. Stubs should signal "not available" or "skip", not "broken".

```go
// BAD: Stub returns false, which the health system interprets as "unhealthy"
func NewHealthChecker() *HealthChecker {
    hc := &HealthChecker{}
    // MQTT not yet wired up
    hc.mqttCheck = func() (bool, string) {
        return false, ""  // StatusWarning for every user with MQTT enabled
    }
    return hc
}

// GOOD: Stub signals "not available" so health system skips it
func NewHealthChecker() *HealthChecker {
    hc := &HealthChecker{}
    hc.mqttCheck = nil  // Health system nil-checks and skips
    return hc
}

// GOOD (alternative): Return a "not configured" status the consumer handles
func NewHealthChecker() *HealthChecker {
    hc := &HealthChecker{}
    hc.mqttCheck = func() (bool, string) {
        return true, "not configured"  // Healthy but unconfigured
    }
    return hc
}
```

**Review action:** When a new stub/placeholder function is wired into an existing system:
1. Trace the return value to its consumer
2. Ask: "What status does this return value map to?"
3. If it maps to warning/error/degraded, the stub is lying about system state

**Search patterns:**
```bash
# Find functions returning hardcoded false (potential misleading stubs)
grep -rn 'return false' --include="*.go" | grep -v '_test.go' | grep 'func\|check\|health\|status'

# Find placeholder/stub comments near return statements
grep -rn 'stub\|placeholder\|not yet\|TODO.*wire\|not implemented' -A3 --include="*.go" | grep 'return'
```

## Inconsistent Error Response Patterns

When a project has a standardized error handler (e.g., c.HandleError, respondWithError, writeError), new API handlers must use it instead of ad-hoc error responses. Inconsistent patterns break error middleware, monitoring, and client error parsing.

```go
// BAD: Ad-hoc JSON error response, bypasses standardized handler
func (h *Handler) GetDiagnostics(ctx echo.Context) error {
    report, err := h.service.GetReport()
    if err != nil {
        return ctx.JSON(http.StatusNotFound, map[string]string{
            "error": "report not available",
        })
    }
    return ctx.JSON(http.StatusOK, report)
}

// GOOD: Use the project's standardized error handler
func (h *Handler) GetDiagnostics(ctx echo.Context) error {
    report, err := h.service.GetReport()
    if err != nil {
        return h.HandleError(ctx, err, http.StatusNotFound, "report not available")
    }
    return ctx.JSON(http.StatusOK, report)
}
```

**Review action:** When reviewing a new API handler:
1. Find how sibling handlers in the same file/package return errors
2. If they use a shared error handler, the new handler must too
3. Search for the error handler function to understand the standard pattern

**Search patterns:**
```bash
# Find ad-hoc JSON error responses (may bypass standard handler)
grep -rn 'ctx.JSON.*Status\|c.JSON.*Error\|w.WriteHeader.*http.Status' --include="*.go" | grep -i 'error\|fail\|not.found'

# Find the project's standard error handler
grep -rn 'func.*HandleError\|func.*respondWithError\|func.*writeError' --include="*.go"

# Compare error patterns across handlers in the same package
grep -rn 'return.*JSON\|return.*HandleError\|return.*Error(' --include="*.go"
```

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

Translation files live at `frontend/static/messages/`. `en.json` is the source of truth. All 13 non-English files must mirror its key structure exactly with properly translated values.

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

for lang in da de es fi fr hu it lv nl pl pt sk sv; do
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

for lang in da de es fi fr hu it lv nl pl pt sk sv; do
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

for lang in da de es fi fr hu it lv nl pl pt sk sv; do
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

for lang in da de es fi fr hu it lv nl pl pt sk sv; do
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

---

# Agent 5: Integration & Wiring Patterns

Agent 5 reads full changed files (not the diff) and checks how new code integrates with existing pipelines. It receives a structured "what's new" list from the coordinator and uses grep/search to trace data flow through the codebase.

## Check 1: End-to-End Data Flow

For each new struct field or type, trace every stage of the pipeline: populate -> store -> serialize -> consume. Flag any stage that doesn't handle the new field.

**Pattern:** A new field is added to a data struct. The collection code populates it, but the serialization/archive code was never updated to write it. The data is collected and silently dropped.

```go
// Trace a new struct field through the pipeline:

// Stage 1: Population (Collect method)
func (c *Collector) Collect(ctx context.Context, opts Options) (*Report, error) {
    data := &Report{}
    if opts.IncludeMetrics {
        data.Metrics, _ = c.collectMetrics()  // Populated here
    }
    return data, nil
}

// Stage 2: Serialization (Export method) - CHECK THIS EXISTS
func (c *Collector) Export(data *Report) ([]byte, error) {
    // Does this method write data.Metrics to the output?
    // If not: High severity - data silently dropped
}

// Stage 3: Consumption (consumer reads the export)
// Does the consumer know how to deserialize the new data type?
```

**Search commands to trace data flow:**
```bash
# Find where a new struct field is read (consumed)
grep -rn 'Metrics\|\.Metrics' --include="*.go"

# Find all serialization points for a struct type
grep -rn 'func.*Report.*\(Write\|Marshal\|Encode\|Export\|Serialize\)' --include="*.go"

# Find all places a struct is constructed
grep -rn 'Report{' --include="*.go"
```

## Check 2: Validation & Guard Completeness

When a new option/flag is added to a struct, find all validation functions that enumerate the struct's fields. Flag if the new field is missing from any enumeration.

**Pattern:** A new boolean flag is added to an options struct, but the validation check that enumerates all flags ("at least one must be true") only lists the original flags.

```go
// New flag added to options struct
type ExportOptions struct {
    IncludeHeaders  bool
    IncludeBody     bool
    IncludeMetadata bool
    IncludeMetrics  bool  // NEW
}

// Validation function - does it know about the new flag?
func (opts ExportOptions) Validate() error {
    // BAD: Only checks original 3 flags
    if !opts.IncludeHeaders && !opts.IncludeBody &&
       !opts.IncludeMetadata {
        return errors.New("at least one include option must be true")
    }

    // GOOD: Includes new flag
    if !opts.IncludeHeaders && !opts.IncludeBody &&
       !opts.IncludeMetadata && !opts.IncludeMetrics {
        return errors.New("at least one include option must be true")
    }
    return nil
}
```

**Search commands:**
```bash
# Find all validation functions for an options struct
grep -rn 'func.*Options.*\(Validate\|validate\|check\|Check\)' --include="*.go"

# Find boolean enumerations (the "at least one" pattern)
grep -rn 'Include\|include' --include="*.go" | grep '&&\|if !'

# Find switch/case that may need new cases
grep -rn 'switch.*opts\.\|case.*opts\.' --include="*.go"
```

## Check 3: Sibling Function Consistency

When a function is modified, find sibling functions and check if the same change should apply. Siblings can be identified by:
- Same receiver type (method on same struct)
- Same file, similar name pattern (e.g., `checkSQLite*` / `checkMySQL*`)
- Same semantic role (both handle settings updates, both process events, both validate input)

**Pattern (name-based siblings):** A logging call is added to all return points in `checkSQLiteMigrationState` but `checkMySQLMigrationState` (same file, same structure, same number of return points) gets none.

**Pattern (role-based siblings):** A `settings_saved` event is emitted from `UpdateSettings` but not from `UpdateSectionSettings`. These are sibling functions performing the same domain operation (modifying settings) even though their names differ.

```go
// Sibling by name pattern:
func checkSQLiteMigrationState(db *sql.DB) MigrationDecision { ... }  // Modified
func checkMySQLMigrationState(db *sql.DB) MigrationDecision { ... }   // Check this!

// Sibling by semantic role:
func (h *Handler) UpdateSettings(ctx echo.Context) error {
    // ... modifies settings, emits "settings_saved" event
}
func (h *Handler) UpdateSectionSettings(ctx echo.Context) error {
    // ... also modifies settings - does it also emit the event?
}
```

**Search commands:**
```bash
# Find sibling functions by name pattern
grep -rn 'func.*check.*MigrationState\|func.*Check.*Migration' --include="*.go"

# Find functions with same receiver type
grep -rn 'func (h \*Handler)' --include="*.go" | grep -i 'update\|save\|settings'

# Find all event emission sites for a specific event
grep -rn 'Emit.*settings_saved\|Emit.*"settings"' --include="*.go"

# Find functions performing same domain operation
grep -rn 'func.*Update.*Settings\|func.*Save.*Settings\|func.*Modify.*Settings' --include="*.go"
```

## Check 4: Pipeline Stage Coverage

For each new collection/provider method, trace the full pipeline and flag missing stages.

**Pattern:** A new `collectMetrics()` method is added to a collector. The collect stage works, but the export stage has no code to write metrics to the output.

```
Pipeline: collect -> store in struct -> write to archive -> read from archive
                                        ^^^^^^^^^^^^^^
                                        Missing stage!
```

**Search commands:**
```bash
# Find all methods on a collector/provider type
grep -rn 'func (c \*Collector)' --include="*.go"

# Find archive/write/serialize methods
grep -rn 'func.*\(Create\|Write\|Build\).*\(Archive\|Output\|Response\)' --include="*.go"

# Find consumer/reader methods
grep -rn 'func.*\(Read\|Parse\|Load\|Extract\).*\(Archive\|Dump\|Support\)' --include="*.go"
```

## Check 5: Constructor & Factory Consistency

When a struct gains a new field, check all constructors and factory functions that create instances. Flag any that don't initialize the new field.

```go
type Config struct {
    Host     string
    Port     int
    MaxRetry int  // NEW field
}

// Constructor 1 - initializes MaxRetry
func NewConfig(host string, port int) *Config {
    return &Config{Host: host, Port: port, MaxRetry: 3}
}

// Constructor 2 - MISSING MaxRetry initialization
func NewConfigFromEnv() *Config {
    return &Config{
        Host: os.Getenv("HOST"),
        Port: envInt("PORT", 8080),
        // MaxRetry not set - defaults to 0, which may cause issues
    }
}

// Factory function - also MISSING
func DefaultConfig() *Config {
    return &Config{Host: "localhost", Port: 8080}
    // MaxRetry not set
}
```

**Search commands:**
```bash
# Find all constructors for a type
grep -rn 'func New.*Config\|func Default.*Config\|func.*Config{' --include="*.go"

# Find struct literal construction
grep -rn 'Config{' --include="*.go" | grep -v '_test.go'

# Find factory methods returning the type
grep -rn 'func.*\*Config' --include="*.go" | grep -v '_test.go'
```

## Check 6: Event Schema Consistency Across Emission Sites

When the same event type is emitted from multiple code paths, all emissions must have a consistent metadata schema. Missing keys at some sites means consumers get inconsistent data.

**Pattern:** A `delivery_attempt` event is emitted from 3 code paths with inconsistent metadata:
- Success path: includes `attempts`, `duration`, `status`
- Circuit-breaker path: includes `duration`, `status` but NOT `attempts`
- Final-failure path: includes `attempts`, `status` but NOT `error`

```go
// Site 1: Success path
events.Emit(ctx, "notification", "delivery_attempt", "success", map[string]any{
    "attempts": attempts,
    "duration": elapsed,
    "status":   "delivered",
})

// Site 2: Circuit-breaker path - MISSING "attempts"
events.Emit(ctx, "notification", "delivery_attempt", "circuit_open", map[string]any{
    "duration": elapsed,
    "status":   "circuit_open",
    // "attempts" key is missing!
})

// Site 3: Final failure - MISSING "error"
events.Emit(ctx, "notification", "delivery_attempt", "failed", map[string]any{
    "attempts": attempts,
    "status":   "failed",
    // "error" key is missing (present in success path as optional)
})
```

**Search commands:**
```bash
# Find ALL emission sites for a specific event type
grep -rn 'Emit.*"delivery_attempt"\|Emit.*delivery_attempt' --include="*.go"

# Find all Emit calls and group by event name
grep -rn '\.Emit(' --include="*.go" | sort

# Compare metadata keys across sites (manual review needed)
grep -rn '\.Emit(' --include="*.go" -A5 | grep 'map\[string\]any{'
```

**Review action:** For each `Emit()` call in changed code:
1. Extract the entity + action pair (e.g., "notification", "delivery_attempt")
2. Search for ALL other emissions with the same pair
3. List metadata keys at each site
4. Flag any site missing keys present at other sites

## Check 7: Recursive Type Handling in Serialization Pipelines

When a function processes values via type-switch, it must handle all composite types that can appear in the input. Missing a composite type means nested data passes through unprocessed.

**Pattern:** A `flattenInto` function recurses into `map[string]any` but not `[]any` slices. Sensitive fields nested inside arrays (e.g., `targets.0.password`) pass through unredacted because the redaction function never sees the leaf keys.

```go
// BAD: Handles maps but not slices - array contents pass through unprocessed
func flattenInto(prefix string, v any, out map[string]string) {
    switch val := v.(type) {
    case map[string]any:
        for k, v := range val {
            flattenInto(prefix+"."+k, v, out)
        }
    case string:
        out[prefix] = val
    case float64:
        out[prefix] = fmt.Sprintf("%v", val)
    // Missing: []any - arrays are silently skipped!
    }
}

// GOOD: Handle all composite types
func flattenInto(prefix string, v any, out map[string]string) {
    switch val := v.(type) {
    case map[string]any:
        for k, v := range val {
            flattenInto(prefix+"."+k, v, out)
        }
    case []any:
        for i, v := range val {
            flattenInto(fmt.Sprintf("%s.%d", prefix, i), v, out)
        }
    case []map[string]any:
        for i, m := range val {
            flattenInto(fmt.Sprintf("%s.%d", prefix, i), m, out)
        }
    case string:
        out[prefix] = val
    default:
        out[prefix] = fmt.Sprintf("%v", val)
    }
}
```

**Search commands:**
```bash
# Find type-switch on any/interface{}
grep -rn 'switch.*:=.*\.\(type\)' --include="*.go"

# Find flatten/walk/traverse functions
grep -rn 'func.*\(flatten\|walk\|traverse\|visit\|recurse\)' --include="*.go" -i

# Check if a type-switch handles both map and slice cases
grep -rn 'case map\[string\]any' --include="*.go" -A20 | grep -c 'case \[\]any'

# Find functions that feed into security operations
grep -rn 'Sensitive\|Redact\|Scrub\|Sanitize\|Mask' --include="*.go" | grep -i 'func\|call'
```

**Review action:** For any function with a `switch v.(type)` that handles `map[string]any`:
1. Check if `[]any` is also handled (array of mixed types)
2. Check if `[]map[string]any` is also handled (array of objects)
3. Assess whether the missing type could carry security-sensitive data
4. Severity: High if the function feeds into redaction/scrubbing, Medium otherwise

## Check 8: Embedded Struct Method Tracing

When a struct embeds another struct, method calls on the outer struct may resolve to the embedded struct's methods via Go's promotion rules. These promoted methods can modify shared state (contexts, mutexes, channels) that the outer struct's code also uses, creating hidden conflicts.

```go
// SQLiteStore embeds DataStore
type SQLiteStore struct {
    DataStore  // Embedded - its methods are promoted to SQLiteStore
}

// Calling s.StartMonitoring() on a SQLiteStore actually calls DataStore.StartMonitoring()
// This method cancels and replaces monitoringCtx, which Open() may have just set

type DataStore struct {
    monitoringCtx    context.Context
    monitoringCancel context.CancelFunc
}

func (ds *DataStore) StartMonitoring() {
    if ds.monitoringCancel != nil {
        ds.monitoringCancel()  // Cancels any existing context
    }
    ds.monitoringCtx, ds.monitoringCancel = context.WithCancel(context.Background())
    go ds.monitorLoop()  // Uses the NEW context
}
```

**Review action:** When reviewing a struct that embeds another struct:
1. Identify all embedded structs: `grep -rn 'type.*struct {' -A10 file.go | grep '^\s\+\*\?\w\+\s*$'`
2. For each embedded struct, find its lifecycle methods: `grep -rn 'func.*EmbeddedType.*\(Start\|Stop\|Close\|Open\|Init\|Monitor\|Shutdown\)' --include="*.go"`
3. Check if any embedded method modifies the same state that the changed code uses
4. Trace the call sequence: does the caller invoke both the changed code AND the embedded method? In what order?

**Severity guide:**
- **High**: Embedded method cancels/replaces state (context, channel) that changed code depends on
- **Medium**: Embedded method modifies state that changed code reads but doesn't depend on for correctness
- **Low**: Embedded method touches unrelated state

**Search patterns:**
```bash
# Find all embedded structs in a type
grep -rn 'type SQLiteStore struct' -A20 --include="*.go" | grep '^\s\+\*\?\w\+\s*$'

# Find methods on the embedded type
grep -rn 'func (ds \*DataStore)' --include="*.go"

# Find all callers that invoke both Open() and StartMonitoring() in sequence
grep -rn 'Open()\|StartMonitoring()' --include="*.go"
```

## General Search Strategy for Agent 5

Agent 5 should use these patterns to systematically trace new code through the codebase:

```bash
# 1. For each new struct field: find all references
grep -rn 'NewFieldName\|\.NewFieldName' --include="*.go"

# 2. For each new function: find all callers
grep -rn 'NewFunctionName(' --include="*.go"

# 3. For each modified function: find siblings
grep -rn 'func.*ReceiverType' --include="*.go"

# 4. For each new event emission: find all same-type emissions
grep -rn 'Emit.*"entity".*"action"' --include="*.go"

# 5. For each new interface method: find all implementations
grep -rn 'func.*InterfaceName\|func.*).*MethodName(' --include="*.go"
```

## Severity Guide

| Category | Severity | Description |
|----------|----------|-------------|
| Data silently dropped | **High** | New data collected but never serialized/archived/returned |
| Security gap in recursive processing | **High** | Sensitive data in unhandled composite types bypasses redaction |
| Validation gap | **Medium** | New option not included in "at least one required" check |
| Event schema inconsistency | **Medium** | Metadata keys differ across emission sites for same event |
| Constructor missing new field | **Medium** | New field not initialized in some construction paths |
| Sibling function asymmetry | **Medium** | Change applied to one function but not its semantic sibling |
| Cosmetic inconsistency | **Low** | Non-functional naming or ordering differences |

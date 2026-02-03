//go:build ruleguard

package gorules

import "github.com/quasilyte/go-ruleguard/dsl"

// BenchmarkLoop detects the old benchmark iteration pattern and suggests using b.Loop().
//
// The old pattern:
//
//	func BenchmarkFoo(b *testing.B) {
//	    for i := 0; i < b.N; i++ {
//	        // work
//	    }
//	}
//
// New pattern (Go 1.24+):
//
//	func BenchmarkFoo(b *testing.B) {
//	    for b.Loop() {
//	        // work
//	    }
//	}
//
// Benefits:
//   - Setup/cleanup executes only once per -count
//   - Compiler cannot optimize away the loop body
//   - Cleaner, more idiomatic code
//
// See: https://pkg.go.dev/testing#B.Loop
func BenchmarkLoop(m dsl.Matcher) {
	// Pattern 1: for i := 0; i < b.N; i++
	// No auto-fix: loop variable $i may be used in body
	m.Match(
		`for $i := 0; $i < $b.N; $i++ { $*body }`,
	).
		Where(m["b"].Type.Is("*testing.B")).
		Report("use for $b.Loop() { ... } instead of for $i := 0; $i < $b.N; $i++ (Go 1.24+); if using $i in body, declare it separately")

	// Pattern 2: for i := range b.N (Go 1.22+ style)
	// No auto-fix: loop variable $i may be used in body
	m.Match(
		`for $i := range $b.N { $*body }`,
	).
		Where(m["b"].Type.Is("*testing.B")).
		Report("use for $b.Loop() { ... } instead of for $i := range $b.N (Go 1.24+); if using $i in body, declare it separately")

	// Pattern 3: for range b.N (no variable) - safe for auto-fix
	m.Match(
		`for range $b.N { $*body }`,
	).
		Where(m["b"].Type.Is("*testing.B")).
		Report("use for $b.Loop() { ... } instead of for range $b.N (Go 1.24+)").
		Suggest("for $b.Loop() { $body }")
}

// TestingContext detects context.Background() or context.TODO() in test functions
// and suggests using t.Context() instead.
//
// The old pattern:
//
//	func TestFoo(t *testing.T) {
//	    ctx := context.Background()
//	    result, err := doSomething(ctx)
//	}
//
// New pattern (Go 1.24+):
//
//	func TestFoo(t *testing.T) {
//	    ctx := t.Context()
//	    result, err := doSomething(ctx)
//	}
//
// Benefits:
//   - Context is automatically canceled when test completes
//   - Test cleanup is properly signaled to goroutines
//   - Resources are released promptly on test failure
//
// See: https://pkg.go.dev/testing#T.Context
// See: https://pkg.go.dev/testing#B.Context
func TestingContext(m dsl.Matcher) {
	// Pattern 1: Assigning context.Background() to a variable
	m.Match(
		`$ctx := context.Background()`,
		`$ctx = context.Background()`,
	).
		Where(m.File().Name.Matches(`_test\.go$`)).
		Report("in tests, use t.Context() instead of context.Background() for automatic cancellation on test completion (Go 1.24+)")

	// Pattern 2: Assigning context.TODO() to a variable
	m.Match(
		`$ctx := context.TODO()`,
		`$ctx = context.TODO()`,
	).
		Where(m.File().Name.Matches(`_test\.go$`)).
		Report("in tests, use t.Context() instead of context.TODO() for automatic cancellation on test completion (Go 1.24+)")

	// Pattern 3: Passing context.Background() directly to a function
	m.Match(
		`$fn(context.Background(), $*args)`,
	).
		Where(m.File().Name.Matches(`_test\.go$`)).
		Report("in tests, use t.Context() instead of context.Background() (Go 1.24+)")

	// Pattern 4: Passing context.TODO() directly to a function
	m.Match(
		`$fn(context.TODO(), $*args)`,
	).
		Where(m.File().Name.Matches(`_test\.go$`)).
		Report("in tests, use t.Context() instead of context.TODO() (Go 1.24+)")
}

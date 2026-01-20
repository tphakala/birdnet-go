//go:build ruleguard

// Package gorules contains custom linting rules for golangci-lint via ruleguard.
// These rules detect patterns that can be modernized for Go 1.25+.
package gorules

import "github.com/quasilyte/go-ruleguard/dsl"

// WaitGroupModernize detects old WaitGroup patterns that can use the new Go 1.25 wg.Go() method.
//
// Old pattern (error-prone):
//
//	wg.Add(1)
//	go func() {
//	    defer wg.Done()
//	    doSomething()
//	}()
//
// New pattern (Go 1.25+):
//
//	wg.Go(func() {
//	    doSomething()
//	})
func WaitGroupModernize(m dsl.Matcher) {
	// Pattern 1: Detect go func with defer wg.Done() inside
	// This is the most common pattern that should be modernized
	m.Match(`go func() { defer $wg.Done(); $*_ }()`).
		Where(m["wg"].Type.Is("*sync.WaitGroup")).
		Report("Use $wg.Go(func() { ... }) instead of go func() { defer $wg.Done(); ... }() (Go 1.25+)").
		Suggest("$wg.Go(func() { $*_ })")

	// Pattern 2: Detect go func with wg.Done() at the end (without defer)
	m.Match(`go func() { $*_; $wg.Done() }()`).
		Where(m["wg"].Type.Is("*sync.WaitGroup")).
		Report("Use $wg.Go(func() { ... }) instead of manual Done() call (Go 1.25+)")

	// Pattern 3: Detect wg.Add(1) which often precedes the old pattern
	// This helps catch cases where Add and go func are on separate lines
	m.Match(`$wg.Add(1)`).
		Where(m["wg"].Type.Is("*sync.WaitGroup")).
		Report("Consider using $wg.Go() which calls Add(1) automatically (Go 1.25+)")

	// Pattern 4: Detect wg.Add with literal > 1, suggesting multiple wg.Go calls
	m.Match(`$wg.Add($n)`).
		Where(m["wg"].Type.Is("*sync.WaitGroup") && m["n"].Const && m["n"].Value.Int() > 1).
		Report("Consider using $wg.Go() for each goroutine instead of Add($n) (Go 1.25+)")
}

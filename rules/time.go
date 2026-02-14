//go:build ruleguard

package gorules

import "github.com/quasilyte/go-ruleguard/dsl"

// TimeDateTimeConstants detects magic date/time format strings and suggests
// using the named constants added in Go 1.20.
//
// Old pattern:
//
//	t.Format("2006-01-02 15:04:05")
//	t.Format("2006-01-02")
//	t.Format("15:04:05")
//
// New pattern (Go 1.20+):
//
//	t.Format(time.DateTime)
//	t.Format(time.DateOnly)
//	t.Format(time.TimeOnly)
//
// Benefits:
//   - More readable and self-documenting
//   - No need to memorize Go's reference time format
//   - Less error-prone
//
// See: https://pkg.go.dev/time#pkg-constants (DateTime, DateOnly, TimeOnly)
func TimeDateTimeConstants(m dsl.Matcher) {
	// DateTime: "2006-01-02 15:04:05"
	m.Match(
		`$t.Format("2006-01-02 15:04:05")`,
	).
		Report(`use $t.Format(time.DateTime) instead of magic format string (Go 1.20+)`).
		Suggest(`$t.Format(time.DateTime)`)

	m.Match(
		`time.Parse("2006-01-02 15:04:05", $s)`,
	).
		Report(`use time.Parse(time.DateTime, $s) instead of magic format string (Go 1.20+)`).
		Suggest(`time.Parse(time.DateTime, $s)`)

	// DateOnly: "2006-01-02"
	m.Match(
		`$t.Format("2006-01-02")`,
	).
		Report(`use $t.Format(time.DateOnly) instead of magic format string (Go 1.20+)`).
		Suggest(`$t.Format(time.DateOnly)`)

	m.Match(
		`time.Parse("2006-01-02", $s)`,
	).
		Report(`use time.Parse(time.DateOnly, $s) instead of magic format string (Go 1.20+)`).
		Suggest(`time.Parse(time.DateOnly, $s)`)

	// TimeOnly: "15:04:05"
	m.Match(
		`$t.Format("15:04:05")`,
	).
		Report(`use $t.Format(time.TimeOnly) instead of magic format string (Go 1.20+)`).
		Suggest(`$t.Format(time.TimeOnly)`)

	m.Match(
		`time.Parse("15:04:05", $s)`,
	).
		Report(`use time.Parse(time.TimeOnly, $s) instead of magic format string (Go 1.20+)`).
		Suggest(`time.Parse(time.TimeOnly, $s)`)
}

// TimerChannelLen detects len() or cap() checks on timer/ticker channels.
//
// In Go 1.23+, timer and ticker channels are unbuffered (capacity 0),
// so checking len() or cap() always returns 0 and is likely a bug.
//
// Problematic pattern:
//
//	timer := time.NewTimer(1 * time.Second)
//	if len(timer.C) > 0 {  // Always false in Go 1.23+
//	    <-timer.C
//	}
//
// Correct pattern (Go 1.23+):
//
//	timer := time.NewTimer(1 * time.Second)
//	select {
//	case <-timer.C:
//	    // timer fired
//	default:
//	    // timer not yet fired
//	}
//
// Background: Before Go 1.23, timer channels had capacity 1. Code that
// checked len(timer.C) to avoid blocking reads is now broken.
//
// See: https://go.dev/doc/go1.23#timer-changes
// See: https://pkg.go.dev/time#Timer
func TimerChannelLen(m dsl.Matcher) {
	// len() on timer.C
	m.Match(
		`len($timer.C)`,
	).
		Where(m["timer"].Type.Is("*time.Timer")).
		Report("len() on timer channel is always 0 in Go 1.23+ (channels are now unbuffered); use non-blocking select instead")

	// len() on ticker.C
	m.Match(
		`len($ticker.C)`,
	).
		Where(m["ticker"].Type.Is("*time.Ticker")).
		Report("len() on ticker channel is always 0 in Go 1.23+ (channels are now unbuffered); use non-blocking select instead")

	// cap() on timer.C
	m.Match(
		`cap($timer.C)`,
	).
		Where(m["timer"].Type.Is("*time.Timer")).
		Report("cap() on timer channel is always 0 in Go 1.23+ (channels are now unbuffered)")

	// cap() on ticker.C
	m.Match(
		`cap($ticker.C)`,
	).
		Where(m["ticker"].Type.Is("*time.Ticker")).
		Report("cap() on ticker channel is always 0 in Go 1.23+ (channels are now unbuffered)")
}

// DeferredTimeSince detects deferred calls to time.Since which evaluate
// the duration at defer time, not at function exit.
//
// Broken pattern:
//
//	func foo() {
//	    start := time.Now()
//	    defer log.Println(time.Since(start))  // Evaluated NOW, not at exit!
//	    // ... work ...
//	}
//
// The time.Since(start) is called immediately when defer is executed,
// so it will always report ~0 duration.
//
// Correct pattern:
//
//	func foo() {
//	    start := time.Now()
//	    defer func() { log.Println(time.Since(start)) }()
//	    // ... work ...
//	}
//
// See: https://pkg.go.dev/time#Since
// Note: Go 1.22 vet tool also warns about this pattern.
func DeferredTimeSince(m dsl.Matcher) {
	// Pattern: defer with time.Since as argument
	m.Match(
		`defer $fn(time.Since($start))`,
	).
		Report("time.Since($start) is evaluated at defer time, not function exit; wrap in func() to measure actual duration")

	// Pattern: defer with time.Since as argument (multiple args)
	m.Match(
		`defer $fn(time.Since($start), $*args)`,
	).
		Report("time.Since($start) is evaluated at defer time, not function exit; wrap in func() to measure actual duration")

	m.Match(
		`defer $fn($arg, time.Since($start))`,
	).
		Report("time.Since($start) is evaluated at defer time, not function exit; wrap in func() to measure actual duration")

	m.Match(
		`defer $fn($arg1, $arg2, time.Since($start))`,
	).
		Report("time.Since($start) is evaluated at defer time, not function exit; wrap in func() to measure actual duration")

	// time.Since as second argument with trailing args
	m.Match(
		`defer $fn($arg, time.Since($start), $*args)`,
	).
		Report("time.Since($start) is evaluated at defer time, not function exit; wrap in func() to measure actual duration")

	// time.Since as fourth argument (3 preceding args)
	m.Match(
		`defer $fn($arg1, $arg2, $arg3, time.Since($start))`,
	).
		Report("time.Since($start) is evaluated at defer time, not function exit; wrap in func() to measure actual duration")
}

// DeferredTimeNow detects deferred calls to time.Now which evaluate
// the time at defer time, not at function exit.
//
// Broken pattern:
//
//	defer log.Println("finished at", time.Now())  // Evaluated NOW!
//
// Correct pattern:
//
//	defer func() { log.Println("finished at", time.Now()) }()
//
// See: https://pkg.go.dev/time#Now
func DeferredTimeNow(m dsl.Matcher) {
	m.Match(
		`defer $fn(time.Now())`,
	).
		Report("time.Now() is evaluated at defer time, not function exit; wrap in func() if you want exit time")

	m.Match(
		`defer $fn($*args, time.Now())`,
	).
		Report("time.Now() is evaluated at defer time, not function exit; wrap in func() if you want exit time")
}

---
name: preflight
description: Pre-push quality gate. Runs 4 parallel reviews (reuse, correctness, quality, i18n) on changes before pushing. Mandatory for all AI-assisted PRs.
---

# Preflight: Pre-Push Quality Gate

Combined code review running parallel review passes to catch reuse opportunities, correctness bugs, safety issues, code quality problems, and i18n translation integrity. Finds and fixes issues before pushing.

## Phase 1: Identify Changes

Run `git diff` (or `git diff HEAD` if there are staged changes) to see what changed. Also run `git diff --name-only` (or `git diff HEAD --name-only`) to get the list of changed files with their full paths.

If there are no git changes, review the most recently modified files that the user mentioned or that you edited earlier in this conversation.

**Scope check:** Before proceeding, verify the changes represent a single concern (one feature, one fix, or one refactor). If the diff contains multiple unrelated changes, stop and ask the user which concern to address in this PR. Split the rest into separate branches.

## Phase 2: Launch Review Passes

Run all four review passes. If your platform supports parallel agents or tasks, run them concurrently for speed; otherwise run them sequentially. Pass each reviewer the full diff AND the list of changed file paths.

Tell reviewers 2, 3, and 4 to read the detailed patterns reference at `reference/patterns.md` (located in the same directory as this skill file) for code examples and validation commands. The reference file is organized by reviewer number, so each can jump to their section.

**Important: All reviewers must classify each finding as:**
- **[CHANGED]** - issue is in lines added or modified by this PR's diff
- **[PRE-EXISTING]** - issue exists in surrounding code not touched by this PR, discovered while reviewing context

This classification drives Phase 3 triage. Reviewers should examine the full context of changed files (not just the diff lines) to catch pre-existing issues in the neighborhood of changes.

### Reviewer 1: Reuse & Efficiency

For each change:

1. **Existing utilities**: search for helpers that could replace newly written code. Look in utility directories, shared modules, and files adjacent to the changed ones.
2. **Duplicate functionality**: flag any new function that duplicates existing functionality. Suggest the existing function instead.
3. **Inline logic**: flag hand-rolled operations (string manipulation, path handling, type guards) where existing utilities exist.
4. **Unnecessary work**: redundant computations, repeated file reads, duplicate network/API calls, N+1 patterns.
5. **Missed concurrency**: independent operations run sequentially that could run in parallel.
6. **Hot-path bloat**: new blocking work added to startup or per-request/per-render paths.
7. **Recurring no-op updates**: state/store updates in loops or handlers that fire unconditionally without change-detection. Also verify wrapper functions honor same-reference returns.
8. **TOCTOU**: pre-checking file/resource existence before operating; operate directly and handle the error instead.
9. **Memory issues**: unbounded data structures, missing cleanup, event listener leaks.
10. **Overly broad operations**: reading entire files when only a portion is needed, loading all items when filtering for one.
11. **Abstraction dispatch in tight loops**: interfaces, generics, or closures called per-element inside hot loops when a concrete implementation exists. Go's GC shape stenciling does not monomorphize; each call goes through interface-style dispatch (15-27% throughput loss). Flag when the abstraction adds no value at the call site.

### Reviewer 2: Correctness & Safety

Review changes for bugs that cause crashes, data loss, or security incidents.

**Read `reference/patterns.md` section "Agent 2: Correctness & Safety Patterns" for detailed examples.**

**Correctness:**
1. **Potential panics** (Go): nil pointer dereferences, index out of bounds, type assertions without ok check, closing nil channels, map/slice access on nil.
2. **Race conditions**: shared state without mutex, concurrent map access, goroutines capturing loop vars (pre-1.22), missing await (TS), stale closures, async $effect() races (Svelte).
3. **Resource leaks**: unclosed files/connections/channels, missing defer, goroutines that never exit, event listeners not removed, timers/intervals not cleared, subscriptions not unsubscribed.
4. **Data corruption**: missing transactions for multi-step operations, partial writes without rollback, concurrent modifications without locking.
5. **Common bugs**: Go (= vs :=, missing rows.Err(), modify slice while iterating, defer in loop, missing return after http.Error), TS (truthy on 0/"", .find() without undefined check, unwaited promise, == vs ===), Svelte ($state destructuring, $effect for derived values, missing cleanup, $state.raw mutation).

**Safety:**
6. **Security**: SQL/command/path injection, XSS (@html without sanitize, innerHTML), hardcoded secrets, insecure crypto, missing auth/authz, CORS misconfiguration.
7. **Sensitive data in telemetry**: raw err.Error() containing file paths, connection strings, or credentials sent to Sentry/logging without scrubbing. Check scrubbing covers all components individually and consistently across all callsites.
8. **Protocol violations**: wrong HTTP status codes, missing Content-Type headers, API contract violations.
9. **Shutdown races**: drain/close without blocking new connections, missing atomic shutdown flag, expected shutdown errors (http.ErrServerClosed) logged as real errors, nil channel close, double-close of resources.
10. **Missing context propagation**: goroutines without ctx or panic recovery, semaphore acquisition without context deadline, context.Background() where parent ctx exists.
11. **Critical TODOs**: TODO/FIXME in auth, validation, crypto, or transaction paths that skip essential implementation. Flag as Critical when the code proceeds without security checks.

### Reviewer 3: Quality & Patterns

Review changes for code smells and recurring patterns from past production issues.

**Read `reference/patterns.md` section "Agent 3: Quality & Patterns Reference" for detailed examples and search commands.**

**General quality:**
1. **Redundant state**: state duplicating existing state, cached values that could be derived, observers/effects replaceable with direct calls.
2. **Parameter sprawl**: adding new parameters instead of generalizing or restructuring.
3. **Copy-paste with slight variation**: near-duplicate blocks that should be unified.
4. **Leaky abstractions**: exposing internal details, breaking abstraction boundaries.
5. **Stringly-typed code**: raw strings where constants, enums, or branded types already exist.
6. **Nested conditionals**: ternary chains, nested if/else 3+ levels deep. Flatten with early returns, guard clauses, lookup tables, or if/else-if cascades.
7. **Unnecessary comments**: comments explaining WHAT (identifiers do that), narrating the change, or referencing the task/caller. Keep only non-obvious WHY.
8. **Swallowed errors**: `result, _ := riskyOp()`, returning nil on error instead of propagating, catching errors without logging or returning, functions that return nil where callers can't distinguish "not found" from actual failures. Missing error context (`return err` instead of `fmt.Errorf("context: %w", err)`).
9. **Dead code, magic numbers, commented-out code.**
10. **Logging quality**: logging in hot paths (per-element loops, per-frame processing, per-record iteration) burns CPU and floods logs; move to batch summary or use rate-limiting. Errors logged *and* propagated to callers (double-logging at every layer). Missing error logging at terminal error handlers where errors are consumed but never recorded. Wrong log levels: Info/Warn for debug-only diagnostics, Debug for production-critical failures. Missing structured context in error logs (no operation name, entity ID, or request context). Format-string overhead in disabled log levels (`log.Debug(fmt.Sprintf(...))` always allocates the string even when Debug is off; use `log.Debugf` or guard with level check).

**Battle-tested patterns (from real PR reviews):**
11. **Test-passing hacks**: hardcoded returns matching test assertions, environment detection, special-case handling only for test inputs, OR conditions broadening acceptance. Red flags: comments mentioning "testing", suspiciously specific constants, algorithms that only handle N cases where N equals test count.
12. **Sentinel cache entries treated as valid**: cache .Get() returning no error with a sentinel/negative entry, but caller only checks err != nil and proceeds. Check ALL callsites that consume the cache.
13. **Wrong condition variable**: checking pre-operation count instead of post-operation result, total instead of remainder. Trace the variable to verify it represents the right state at the right time.
14. **Stale cache files**: file cache storing new content with different filename/extension without removing old variants.
15. **Incomplete multi-site fixes** (HIGH priority): fix applied to one callsite but same pattern exists at 3-5 other callsites. After finding/fixing any pattern, immediately grep the entire codebase for all occurrences. This is the #1 source of review comments that result in actual fixes.
16. **Tests not exercising the fixed path**: test configuration bypasses the code path being fixed (would pass before the fix too). Verify the test would FAIL on the old code.
17. **Inconsistent guard patterns**: one function checks a guard (telemetry opt-in, shutdown flag, nil check) but sibling functions with same contract don't. When a guard exists, check all siblings.
18. **Go API design smells**: exported function returning unexported type, context.Context not first parameter, dead code branches (both paths produce same result), Get* that mutates / Set* that doesn't.
19. **Documentation contradicting code**: help text, comments, or README describing behavior opposite to what code does. Pay special attention after refactors.

**Frontend-specific (only when .svelte or frontend .ts files changed):**
20. **daisyUI classes**: this project uses native Tailwind v4.1 only. Flag btn, card, modal, drawer, navbar, alert, badge, tooltip, dropdown, menu, tabs, toggle, avatar, etc.
21. **Malformed Tailwind CSS variable classes**: suffix outside brackets like `[var(--color-X)]-content` instead of `[var(--color-X-content)]`. Silent failure, no build/runtime error.
22. **Binding to read-only $derived**: bind:checked/bind:value on $derived values causes runtime errors on interaction.
23. **Invalid Svelte 5 reactivity**: destructuring $state (breaks tracking), $effect for derived values (use $derived), assigning $state to locals, mutating $state.raw, missing $effect cleanup, Svelte 4 `$:` syntax.
24. **Duplicate keys**: non-unique display fields (name, label, title) used as {#each} keys or <option> values when unique IDs exist. Causes each_key_duplicate crash.
25. **Hardcoded user-facing strings**: all user-facing text must use the i18n library.
26. **Partial settings/config objects**: frontend deriving state from backend settings without merging over defaults. New fields on existing config objects will be undefined.

### Reviewer 4: i18n Translation Integrity

Validate translation files under `frontend/static/messages/`. Use `en.json` as the single source of truth.

**Always run this reviewer when any of these are in the changed file list:**
- `frontend/static/messages/*.json`
- `frontend/**/*.svelte` or `frontend/**/*.ts` (may introduce new i18n keys)

**Read `reference/patterns.md` section "Agent 4: i18n Translation Integrity Patterns" for validation commands and examples.**

**Checks:**

1. **Missing keys (gaps)**: Flatten all key paths from `en.json` using dot-notation. For each non-English file, flag every key path present in `en.json` but absent from that file. Every gap is a runtime fallback or missing UI string.

2. **English placeholders**: For each non-English file, compare every value against the corresponding `en.json` value. Flag any exact match. English text in non-English files is never acceptable; proper translations must be provided for all locales. Only exceptions: single characters, pure numbers, format tokens (`{0}`, `{count}`), and technical identifiers that are inherently language-neutral (protocol names, unit abbreviations like `dB`, `Hz`).

3. **Orphaned keys**: Flag key paths present in a non-English file but absent from `en.json`. These are dead translations that inflate file size and confuse translators.

4. **Structural mismatches**: Flag any key that is an object in one file but a scalar in another (nesting inconsistency). This causes runtime errors in the i18n library.

Classify each finding as **[CHANGED]** or **[PRE-EXISTING]** based on whether the affected key appears in this PR's diff of the translation files.

## Phase 3: Aggregate, Fix & Report

Wait for all reviewers to complete. Then:

1. **Cross-reference findings** across all reviewers. Deduplicate overlapping reports.
2. **Filter false positives** using the reference below. Discard known benign patterns.
3. **Triage each valid finding** into one of three categories:

   **a) In-scope (introduced or touched by this PR):** Fix it directly. No exceptions.

   **b) Pre-existing but fixable in this PR:** If the fix is small, safe, and in a file already changed by this PR, fix it. Include it in the summary as a bonus fix.

   **c) Pre-existing and out-of-scope:** If the fix is risky, large, or in untouched files, file a tracking issue in the project's issue tracker. Include the file path, line number, and a clear description of the problem.

4. **Report remaining items** that cannot be auto-fixed, with severity:
   - **Critical**: security vulnerabilities, data corruption risks, panics in production paths
   - **High**: race conditions, resource leaks, incomplete multi-site fixes
   - **Medium**: code smells, inconsistent patterns, test gaps
   - **Low**: style, minor optimization opportunities

When done, summarize:
- What was fixed in this PR
- What pre-existing issues were fixed as bonus
- What tracking issues were filed (with issue numbers) for out-of-scope items
- Any remaining items that need manual attention

## Phase 4: PR Readiness Certification

Before pushing or creating a PR, verify each item by actually executing the commands and observing passing output. Do NOT check boxes based on expectation; run the command, read the output, confirm it passes.

```markdown
## Preflight Certification

- [ ] Single concern: PR contains exactly ONE feature, ONE fix, or ONE refactor
- [ ] Preflight passed: All Phase 1-3 findings resolved or filed as issues
- [ ] Linters clean: golangci-lint run -v and npm run check:all pass (zero warnings)
- [ ] Tests pass: go test -race ./... and npm test pass
- [ ] No unrelated changes: diff contains only changes relevant to the stated goal
- [ ] Scope complete: PR fully implements what it claims; no TODO/FIXME for core functionality
- [ ] Breaking changes documented: if API/config/behavior changes, noted in PR description
- [ ] i18n complete: new user-facing strings have translations in all locale files
- [ ] No secrets or PII: no hardcoded credentials, API keys, or personal data in diff
```

Include this certification in the PR description under a "Preflight Status" heading.

## Static Analysis False Positive Patterns

Use these to filter findings during Phase 3. Do NOT flag these as issues:

- **`math/rand` for non-security purposes**: jitter, backoff, load distribution are correct uses. Only flag for tokens, session IDs, CSRF nonces, or crypto.
- **`template.HTML()` on hardcoded constants**: compile-time string constants are safe. Only flag when input comes from user data, DB fields, or HTTP params.
- **`os.Exit` in unrecoverable startup**: acceptable when the app genuinely cannot continue (embedded config unreadable, crypto/rand unavailable). Flag in request handlers or background goroutines.
- **Short variable names in domain math**: `r`, `g`, `b` in color math, `s` in sRGB linearization, `t` in PRNG state are standard in their domains.
- **Empty slice literals in tests**: `[]T{}` vs `var s []T` is intentional for JSON serialization and assert.Equal comparisons.
- **Cyclomatic complexity in config validation**: config/settings validation functions are inherently branchy. High complexity is expected, not a design problem.
- **Intentional duplication in CPU-bound hot paths**: Go's inlining budget (cost < 80) means splitting hot loops into helpers often forces non-inlined calls with register spilling. Giant functions, duplicated loops, and hand-specialized variants are correct patterns for per-byte/per-element processing. Do not flag function size, code duplication, or "extract a helper" in these paths. Look for `//go:noinline` markers, build-tagged assembly files, or comments referencing inlining as signals of intentional optimization.

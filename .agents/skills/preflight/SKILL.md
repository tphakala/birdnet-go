---
name: preflight
description: Pre-push quality gate. Runs 6 parallel reviews (reuse, correctness, quality, i18n, integration wiring, regression/backward-compatibility) on changes before pushing, with optional secondary-model cross-validation. Mandatory for all AI-assisted PRs.
---

# Preflight: Pre-Push Quality Gate

Combined code review running parallel review passes to catch reuse opportunities, correctness bugs, safety issues, code quality problems, i18n translation integrity, integration wiring gaps, and regressions or backward-compatibility breaks that would hurt existing users on upgrade. Finds and fixes issues before pushing.

**Tool-agnostic:** this skill is run by whatever agent invokes it (Claude Code, Codex, Antigravity, Gemini CLI, etc.). Where it asks for a "secondary-model cross-validation," use any independent model your environment exposes; see "Secondary-Model Cross-Validation" under Reviewer 6 for the mechanism and the graceful fallback when no second model is available.

## Phase 1: Identify Changes

Run `git diff` (or `git diff HEAD` if there are staged changes) to see what changed. Also run `git diff --name-only` (or `git diff HEAD --name-only`) to get the list of changed files with their full paths.

If there are no git changes, review the most recently modified files that the user mentioned or that you edited earlier in this conversation.

**Scope check:** Before proceeding, verify the changes represent a single concern (one feature, one fix, or one refactor). If the diff contains multiple unrelated changes, stop and ask the user which concern to address in this PR. Split the rest into separate branches.

## Phase 2: Launch Review Passes

Run all six review passes. If your platform supports parallel agents or tasks, run them concurrently for speed; otherwise run them sequentially. Pass Reviewers 1-4 and 6 the full diff AND the list of changed file paths. Reviewer 5 receives different input (see coordinator pre-work below).

Tell reviewers 2, 3, 4, 5, and 6 to read the detailed patterns reference at `reference/patterns.md` (located in the same directory as this skill file) for code examples and validation commands. The reference file is organized by reviewer number, so each can jump to their section.

**Coordinator pre-work for Reviewer 5:** Before dispatching Reviewer 5, extract from the diff:
- New struct fields added
- New functions/methods added
- New event emission calls (e.g., `events.Emit()`)
- New options/flags added to config/options structs
- New interface methods added

Pass this structured "what's new" list to Reviewer 5 along with the changed file paths. Do NOT pass Reviewer 5 the raw diff.

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
2. **Nil guards on stored function fields**: when a struct stores function-typed fields (closures, callbacks, check functions), the Run/Execute/Handle method must nil-check them before calling. Even if current callers always provide non-nil, future callers or test code may not. Especially important for exported types in reusable packages.
3. **Constructor input validation**: constructors accepting size, capacity, or count parameters must validate > 0 (prevents division-by-zero panics). Methods accepting count/limit parameters must guard against negative values.
4. **Race conditions**: shared state without mutex, concurrent map access, goroutines capturing loop vars (pre-1.22), missing await (TS), stale closures, async $effect() races (Svelte).
5. **Resource leaks**: unclosed files/connections/channels, missing defer, goroutines that never exit, event listeners not removed, timers/intervals not cleared, subscriptions not unsubscribed.
6. **Data corruption**: missing transactions for multi-step operations, partial writes without rollback, concurrent modifications without locking.
7. **Common bugs**: Go (= vs :=, missing rows.Err(), modify slice while iterating, defer in loop, missing return after http.Error), TS (truthy on 0/"", .find() without undefined check, unwaited promise, == vs ===), Svelte ($state destructuring, $effect for derived values, missing cleanup, $state.raw mutation).
8. **Non-DST-safe time arithmetic**: `time.Now().Add(-N * 24 * time.Hour)` loses or gains an hour near DST transitions. Use `time.Now().AddDate(0, 0, -N)` for calendar-day arithmetic. Flag any `N * 24 * time.Hour` used for day-level offsets.
9. **Event emission ordering (query-before-emit)**: emitting a new event before querying for the previous one creates a race where you read back your own event. Always query history before emitting new entries in event-sourced patterns.
10. **Context lifecycle through embedded structs**: when code reads or writes a `context.Context` field on a struct, grep for ALL assignments to that field across the entire package (`grep -rn 'fieldName\s*=' pkg/`). Embedded struct methods can cancel and replace contexts set by the parent. A goroutine capturing a context may be killed when an embedded method replaces it. Especially important for lifecycle methods (`Start`, `Stop`, `Open`, `Close`, `Monitor`).

**Safety:**
11. **Security**: SQL/command/path injection, XSS (@html without sanitize, innerHTML), hardcoded secrets, insecure crypto, missing auth/authz, CORS misconfiguration.
12. **Sensitive data in telemetry**: raw err.Error() containing file paths, connection strings, or credentials sent to Sentry/logging without scrubbing. Check scrubbing covers all components individually and consistently across all callsites.
13. **Shallow copy of reference types in concurrent collections**: when storing structs containing maps or slices in concurrent-safe collections (ring buffers, sync.Map, channel-based queues), deep copy the reference types using maps.Clone/slices.Clone. When returning stored structs via accessor methods, clone reference-typed fields to prevent data races with callers.
14. **Protocol violations**: wrong HTTP status codes, missing Content-Type headers, API contract violations.
15. **Shutdown races**: drain/close without blocking new connections, missing atomic shutdown flag, expected shutdown errors (http.ErrServerClosed) logged as real errors, nil channel close, double-close of resources.
16. **Missing context propagation**: goroutines without ctx or panic recovery, semaphore acquisition without context deadline, context.Background() where parent ctx exists.
17. **Critical TODOs**: TODO/FIXME in auth, validation, crypto, or transaction paths that skip essential implementation. Flag as Critical when the code proceeds without security checks.

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
20. **Stub/placeholder return values causing misleading status**: when wiring stub or placeholder functions, verify the stub's return value doesn't trigger a degraded/warning status in the consumer. Stubs should return "not available" / nil / skip signals, not "failure" signals that cause permanent warnings for users with that feature enabled.
21. **Inconsistent error response patterns**: when a new API handler returns errors, verify it uses the project's standardized error handler (e.g., c.HandleError) rather than ad-hoc ctx.JSON error responses. Search for the error pattern used in sibling handlers.

**Frontend-specific (only when .svelte or frontend .ts files changed):**
22. **daisyUI classes**: this project uses native Tailwind v4.1 only. Flag btn, card, modal, drawer, navbar, alert, badge, tooltip, dropdown, menu, tabs, toggle, avatar, etc.
23. **Malformed Tailwind CSS variable classes**: suffix outside brackets like `[var(--color-X)]-content` instead of `[var(--color-X-content)]`. Silent failure, no build/runtime error.
24. **Binding to read-only $derived**: bind:checked/bind:value on $derived values causes runtime errors on interaction.
25. **Invalid Svelte 5 reactivity**: destructuring $state (breaks tracking), $effect for derived values (use $derived), assigning $state to locals, mutating $state.raw, missing $effect cleanup, Svelte 4 `$:` syntax.
26. **Duplicate keys**: non-unique display fields (name, label, title) used as {#each} keys or <option> values when unique IDs exist. Causes each_key_duplicate crash.
27. **Hardcoded user-facing strings**: all user-facing text must use the i18n library.
28. **Partial settings/config objects**: frontend deriving state from backend settings without merging over defaults. New fields on existing config objects will be undefined.

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

### Reviewer 5: Integration & Wiring

Review the full context of changed files for integration gaps where new code fails to connect with existing pipelines. This reviewer does NOT receive the diff. Instead, it receives the list of changed file paths and the structured "what's new" list extracted by the coordinator.

**Read the full content of each changed file using the Read tool. Do NOT rely on the diff.**

**Read `reference/patterns.md` section "Agent 5: Integration & Wiring Patterns" for detailed examples and search commands.**

**Checks:**
1. **End-to-end data flow**: For each new struct field or type, trace where it's populated (collection/construction) and where it's consumed (serialization, archive, API response, template). Flag if any pipeline stage doesn't handle the new field.
2. **Validation & guard completeness**: For each new option/flag added to a struct, find all validation functions that enumerate the struct's fields. Flag if the new field is missing from any enumeration.
3. **Sibling function consistency**: When a function is modified, find sibling functions (same receiver, same file, similar name pattern, or same semantic role/domain operation) and check if the same change should apply. Flag asymmetry with specific function names and line numbers.
4. **Pipeline stage coverage**: For each new collection/provider method, trace the pipeline: collect -> store in struct -> write to archive/response -> deserialize in consumer. Flag any stage that doesn't handle the new data.
5. **Constructor & factory consistency**: When a struct gains a new field, check all constructors/factories that create instances of that struct. Flag if any constructor doesn't initialize the new field.
6. **Event schema consistency across emission sites**: For each event emission call, find ALL other emissions with the same event type (entity + action pair). Compare metadata keys across all emission sites. Flag any site missing keys present in others.
7. **Recursive type handling in serialization pipelines**: When a function processes a type-switch on `any`, check that all relevant composite types are handled (`map[string]any`, `[]any`, `[]map[string]any`). Especially important when the function feeds into security-sensitive operations (redaction, scrubbing, access control).
8. **Embedded struct method tracing**: When a struct embeds another struct, read the embedded struct's methods that are called (directly or implicitly) from the changed code. Calls like `s.StartMonitoring()` may resolve to an embedded struct's method that modifies shared state (contexts, mutexes, channels). Trace all lifecycle methods (`Start`, `Stop`, `Open`, `Close`, `Init`, `Monitor`, `Shutdown`) on embedded structs to check for state conflicts with the parent struct's code.
9. **Read-time filter vs write-time invariant** (semantic regression via feature interaction): **Trigger this check only when** the change introduces a NEW filter on a persistent data entity (DB, cache, event store) in a READ path: a SQL `WHERE`/`HAVING` clause, a `.filter()`/predicate over stored rows, or a conditional that excludes stored records before returning them. When that trigger fires, do NOT stop at "the API/schema is backward compatible." Instead trace the entity's WRITE path: find where rows of that model are created/saved (e.g. `Save`, `Create`, an ingestion/flush function) and read the validation immediately preceding the write. Then answer two questions:
   - **(a) Does the new read filter blindly re-apply a filter the write path already enforced?** If every stored row already satisfies the criterion at write time, the read filter is redundant at best.
   - **(b) Does the new read filter contradict the write-time logic?** If the write path admits rows by a *finer-grained or dynamically-adjusted* version of the same criterion (per-entity overrides, adaptive/dynamic thresholds, per-model settings, time-varying gates), then a coarser static read filter will silently drop rows that were stored on purpose. That is a semantic regression even though no wire contract changed.
   Use a whole-codebase trace (find the model's write path and the features that adjust the criterion), NOT keyword grep guessing. When (b) holds, this is a **behavioral/intent conflict**: do NOT auto-fix it; route it to Maintainer-confirm (Phase 3 category d) with the colliding feature named and the user-visible consequence stated. Severity High (legitimate stored data silently hidden from the user). See the worked example in the patterns reference.

**Output format:** Each finding must include the new item being traced, the pipeline stage that's missing, the specific file and function where the gap exists, and severity: High (data silently dropped or hidden), Medium (validation gap), Low (cosmetic inconsistency).

Classify each finding as **[CHANGED]** or **[PRE-EXISTING]** based on whether the gap is in code introduced by this PR or in pre-existing code.

### Reviewer 6: Regression & Backward Compatibility

Review changes for regressions, backward-compatibility breaks, and user-facing behavioral changes that would negatively impact existing users on upgrade. This reviewer runs its own diff analysis first, then (if a secondary model is available) cross-validates its findings before reporting. Pass this reviewer the full diff AND the list of changed file paths.

**Read `reference/patterns.md` section "Agent 6: Regression & Backward Compatibility Patterns" for detailed examples, the diff-comparison method, the secondary-model cross-validation prompt template, and search commands.**

The guiding question for every change: "Would an existing user with a working setup see something break, lose data, or behave differently after upgrading to this code?" A change is a regression/compat issue if it affects the existing, working path. Brand-new code that is never reached by an existing path cannot regress, but do NOT mistake "additive on the wire" for "behavior-preserving for the user": additive code at the API or DB schema layer (a new opt-in query param, a new column, a new optional field) CAN cause a regression when an existing client surface (a dashboard call, a default-rendered list, an existing workflow, a default report) is changed to consume it in a way that alters what existing users see or do. The user experience is what regresses, not the JSON contract. The test is the rendered/observed result for an existing user after upgrade, not whether the wire shape stayed compatible. When such a behavioral change also conflicts with the intent of another feature (see Reviewer 5 check 9), treat it as a behavioral/intent conflict and route it to Maintainer-confirm (Phase 3 category d) rather than fixing it.

**Step 1: Diff analysis.** Examine the diff (added, removed, and modified lines, not just current state) across these surfaces:

1. **Config & settings schema** (`internal/conf/`, settings structs with `yaml`/`koanf`/`mapstructure` tags): renamed or removed config keys, changed struct tags (silently orphans values in every existing `config.yaml`), retyped fields (scalar to list, string to int), changed default values (alters behavior for users who never set the key), removed sections without a migration or alias.
2. **REST API v2 contracts** (`internal/api/v2/`, handlers and response structs): removed, renamed, or retyped JSON response fields (`json` tags), changed or removed endpoint paths and HTTP methods, changed status codes, new required request fields or query params, renamed query params. These break existing API clients, integrations, and the frontend.
3. **DB schema & migrations** (GORM models, migration code): dropped or renamed columns, retyped columns, non-additive schema changes, missing or irreversible migrations, AutoMigrate changes that lose data or fail on existing databases. Flag any model change that is not purely additive and lacks a corresponding safe migration.
4. **CLI flags & user UX** (cobra commands, flag definitions): removed or renamed flags, changed flag defaults, changed detection thresholds or other defaults that alter detection output, changed notification behavior, changed dashboard/UI workflows, changed log/export/filename formats that users or scripts parse.
5. **Regressions in existing logic**: changed conditionals, thresholds, or branches that alter output for inputs that previously worked; removed handling of a case the old code handled; reordered operations that change observable results; tightened validation that now rejects previously-accepted input.

For each candidate issue, record: the surface, the specific change (old to new), why it breaks or changes behavior for existing users, the `file:line`, and a proposed migration/alias/deprecation path if the break is unavoidable.

**Step 2: Secondary-model cross-validation.** If you have access to an independent secondary model (a different model family than the one running this review), use it to confirm or refute the Step 1 findings and surface anything missed. The reference mechanism in this environment is the Gemini CLI, which any tool with shell access can invoke:

```bash
gemini -m gemini-3-pro-preview -p "REVIEW ONLY - Do NOT modify any files. <regression cross-validation prompt>"
```

See the patterns reference for the full prompt template and the temp-diff-file method (so the secondary model can read removed/old lines). If your environment exposes a different secondary model, use that instead with the same prompt. If NO secondary model is available, do not silently skip: proceed with Step 1 findings only and note in the report that secondary-model cross-validation was unavailable.

**Step 3: Report.** Keep findings the secondary model confirmed, drop findings it convincingly refuted (note the disagreement if you are unsure), and add any new issues it surfaced after verifying them against the diff yourself. Classify each as **[CHANGED]** or **[PRE-EXISTING]**, and assign severity:
- **Critical**: silent data loss on upgrade (destructive migration, dropped column with live data), or config that fails to load after upgrade.
- **High**: removed/renamed API field or config key with no migration, changed default that silently alters detection behavior, removed CLI flag, read-time filter that hides data an adaptive/per-entity write gate kept on purpose.
- **Medium**: defensible but undocumented behavioral change, missing deprecation path, changed output format.
- **Low**: cosmetic or easily-tolerated change.

For any unavoidable break, the report must state the recommended migration path (config key alias/deprecation, additive migration plus backfill, API versioning, or preserving the old default behind a flag).

## Phase 3: Aggregate, Fix & Report

Wait for all reviewers (including Reviewers 5 and 6) to complete. Then:

1. **Cross-reference findings** across all reviewers. Deduplicate overlapping reports.
2. **Filter false positives** using the reference below. Discard known benign patterns.
3. **Triage each valid finding** into one of four categories:

   **a) In-scope (introduced or touched by this PR):** Fix it directly. No exceptions, EXCEPT findings that fall under category (d) below.

   **b) Pre-existing but fixable in this PR:** If the fix is small, safe, and in a file already changed by this PR, fix it. Include it in the summary as a bonus fix.

   **c) Pre-existing and out-of-scope:** If the fix is risky, large, or in untouched files, file a tracking issue in the project's issue tracker. Include the file path, line number, and a clear description of the problem.

   **d) Maintainer-confirm (behavioral/intent conflict or security bypass):** Do NOT auto-fix, revert, or "correct" these, even if in-scope. Whether the change is desirable is a product judgment the gate is not authorized to make, and auto-fixing usually means silently deleting the contributor's new code. This category covers: a behavioral/feature-interaction conflict (Reviewer 5 check 9 or Reviewer 6, e.g. a read-time filter that contradicts a write-time invariant or an adaptive feature like dynamic thresholds); an additive-but-behavior-changing client surface (Reviewer 6 guiding question); and a security bypass (e.g. disabling TLS verification, skipping auth) even when the author justifies it in a comment. For each, surface the finding to the maintainer: name the colliding feature or bypassed control, state the user-visible consequence, and hold for explicit confirmation of intent before any change. Report these prominently in the summary as "needs maintainer decision", never as fixed.

4. **Fix verification (post-fix re-scan):** After applying fixes from step 3a/3b, re-read each fixed file and verify:
   - **Shared state safety**: if the fix touches a context, mutex, atomic, or channel field, grep for ALL other code that reads or writes that same field. Confirm the fix doesn't create a new race or lifecycle conflict.
   - **Comment staleness**: check that any comments within 5 lines of the fixed code still accurately describe the behavior. Fix-then-forget is the #1 source of stale comments.
   - **Embedded struct interactions**: if the fix initializes or modifies a field on an embedded struct, check the embedded struct's lifecycle methods for conflicts (they may cancel, replace, or override the value).

   This step catches "fix-introduces-bug" patterns where a superficially correct fix creates a new problem through interactions with code the agent didn't read during the initial review.

5. **Report remaining items** that cannot be auto-fixed, with severity:
   - **Critical**: security vulnerabilities, data corruption risks, panics in production paths
   - **High**: race conditions, resource leaks, incomplete multi-site fixes
   - **Medium**: code smells, inconsistent patterns, test gaps
   - **Low**: style, minor optimization opportunities

When done, summarize:
- What was fixed in this PR
- What pre-existing issues were fixed as bonus
- What tracking issues were filed (with issue numbers) for out-of-scope items
- **What needs a maintainer decision** (category d items): each behavioral/intent conflict or security bypass, with the colliding feature/control named and the user-visible consequence stated, held unfixed pending confirmation
- Whether secondary-model cross-validation ran (and which model) or was unavailable
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
- [ ] No regression/backward-compat break: no orphaned config keys, removed/renamed API fields, destructive migrations, changed detection defaults, or read-time filters that hide data an existing feature kept on purpose (Reviewer 6)
- [ ] Breaking changes documented: if API/config/behavior changes, noted in PR description
- [ ] Maintainer-confirm items resolved: any category (d) behavioral/intent conflict or security bypass has been confirmed by the maintainer, not silently auto-fixed
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

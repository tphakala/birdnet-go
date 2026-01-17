# Navigation Code Smells Fix Plan

## Context

PR #1820 fixes the navigation routing bug where `navigation.navigate()` calls bypass `App.svelte`'s `handleRouting()` function. The fix uses a reactive `$effect` to watch `navigation.currentPath` and trigger routing automatically.

Code review identified redundancies introduced by the fix that should be cleaned up.

## Must Fix (Critical)

### 1. Remove redundant `handleRouting()` from `navigate()` function

**File:** `frontend/src/App.svelte`
**Lines:** 20-23

**Current code:**

```svelte
function navigate(url: string): void {
  navigation.navigate(url);
  handleRouting(navigation.currentPath);
}
```

**Problem:** The `$effect` at lines 382-393 already reacts to `navigation.currentPath` changes and calls `handleRouting()`. Having the call in `navigate()` causes double routing.

**Fix:**

```svelte
function navigate(url: string): void {
  navigation.navigate(url);
}
```

**Verification:**

- Routing triggered once per navigation (not twice)
- All navigation paths still work (sidebar, detection clicks, back/forward)

## Should Fix (High Priority)

### 2. Simplify `onMount` by removing manual routing

**File:** `frontend/src/App.svelte`
**Lines:** 372-376

**Current code:**

```svelte
// Determine current route from URL path (use store which has normalized path)
handleRouting(navigation.currentPath); // Set lastRoutedPath to prevent the reactive $effect from
re-routing immediately lastRoutedPath = navigation.currentPath;
```

**Problem:** This manual routing in `onMount` is redundant because:

- The `$effect` at lines 382-393 handles routing when `appInitialized` becomes true
- We're setting `lastRoutedPath` as a band-aid to prevent the `$effect` from double-routing

**Proposed fix:** Remove both lines and let the `$effect` handle initial routing.

**Risk assessment:**

- **Low risk:** The `$effect` already contains the logic to route when `appInitialized` is true
- **Concern:** The `$effect` has `if (!appInitialized) return;` guard - need to verify timing

**Alternative:** Keep current approach if timing issues discovered during testing.

**Verification:**

- App loads correctly on first visit
- Initial route matches URL
- No flash of wrong content

## Not Fixing (Deferred)

### 3. `loadingComponent` blocking issue

- Known limitation, complex fix
- Tracked for future work

### 4. daisyUI classes in error/loading templates

- Existing code, not part of this change
- Separate cleanup task

### 5. `GenericErrorPage` typed as `any`

- Minor type safety issue
- Can fix separately

## Execution Order

1. Apply fix #1 (remove redundant `handleRouting` from `navigate`)
2. Run tests to verify
3. Apply fix #2 (simplify `onMount`)
4. Run tests to verify
5. If fix #2 causes issues, revert and keep original approach
6. Run full lint and test suite
7. Update PR

## Success Criteria

- [ ] All 1605 tests pass
- [ ] No lint errors
- [ ] Navigation works: sidebar links, detection clicks, browser back/forward
- [ ] No double routing visible in logs
- [ ] Initial page load works correctly

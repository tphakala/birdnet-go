# AST-Grep Integration Guide

## Overview

ast-grep provides advanced static analysis beyond ESLint, focusing on:

- **Svelte 4 → 5 Migration**: Detect legacy patterns needing updates
- **Svelte 5 Best Practices**: Enforce proper rune usage and patterns
- **Security**: XSS, CSRF, data validation vulnerabilities
- **Performance**: Expensive operations, missing optimizations
- **Type Safety**: Patterns ESLint can't detect

## Commands

```bash
# Individual scans
npm run ast:security         # Security vulnerabilities
npm run ast:svelte5          # Svelte 5 specific patterns
npm run ast:migration        # Svelte 4 legacy patterns
npm run ast:best-practices   # Svelte 5 best practices

# Test rule (verifies ast-grep is working)
npm run ast:test            # Should find console usage - for testing

# Comprehensive scan (runs all production rules)
npm run ast:all

# Auto-fix issues where possible
npm run ast:fix

# Include in full project check
npm run check:all

# Taskfile integration
task frontend-lint          # Includes ast:all
task frontend-quality       # Includes ast:all + other checks
```

## Rule Categories

### 🔄 Migration Rules (`ast-grep-svelte4-migration.yml`)

Detects Svelte 4 patterns that should be migrated:

- `let` → `$state()`
- `$:` → `$derived()` or `$effect()`
- `export let` → `$props()`
- `<slot>` → snippets
- `on:event` → `onevent`
- `createEventDispatcher` → callback props
- `$$props`, `$$restProps`, `$$slots`
- `new Component()` → `mount()`
- Event modifiers → handler functions

### ✨ Best Practices (`ast-grep-svelte5-best-practices.yml`)

Enforces proper Svelte 5 patterns:

- No destructuring of `$state` objects
- Pure `$derived` (no side effects)
- `$effect` cleanup functions
- Proper snippet rendering with `?.`
- Type-safe Props interfaces
- Browser API guards
- Each block keys
- Async operation handling
- Binding validation

### 🔒 Security Rules (`ast-grep-security.yml`)

- CSRF token validation
- XSS prevention in `{@html}`
- localStorage validation
- Password/token logging prevention
- Unsafe innerHTML patterns

### 📝 Svelte 5 Rules (`ast-grep-svelte5.yml`)

- Reactive state destructuring prevention
- `$effect` cleanup enforcement
- `$derived` purity validation
- Snippet vs slot detection
- Rune file location checks

### 🎯 General Rules (`.ast-grep.yml`)

- Icon utility usage
- Date formatting conventions
- Logger usage over console
- Missing error context
- Performance patterns
- Test quality

## Key Migration Examples

### Reactive Declarations

```svelte
<!-- Svelte 4 -->
<script>
  let count = 0;
  $: doubled = count * 2;
  $: console.log('count:', count);
</script>

<!-- Svelte 5 -->
<script>
  let count = $state(0);
  const doubled = $derived(count * 2);
  $effect(() => {
    console.log('count:', count);
  });
</script>
```

### Component Props

```svelte
<!-- Svelte 4 -->
<script>
  export let title;
  export let count = 0;
  export { className as class };
</script>

<!-- Svelte 5 -->
<script>
  let { title, count = 0, class: className } = $props();
</script>
```

### Slots to Snippets

```svelte
<!-- Svelte 4 Parent -->
<Card>
  <div slot="header">Title</div>
  Content
</Card>

<!-- Svelte 5 Parent -->
<Card>
  {#snippet header()}
    <div>Title</div>
  {/snippet}
  {#snippet children()}
    Content
  {/snippet}
</Card>

<!-- Svelte 4 Child -->
<slot name="header" />
<slot />

<!-- Svelte 5 Child -->
{@render header?.()}
{@render children?.()}
```

### Event Handlers

```svelte
<!-- Svelte 4 -->
<button on:click={handler}>
<button on:click|preventDefault={submit}>

<!-- Svelte 5 -->
<button onclick={handler}>
<button onclick={(e) => { e.preventDefault(); submit(e); }}>
```

## Common Issues Detected

### ❌ Destructuring Breaks Reactivity

```typescript
// Wrong - loses reactivity
const { count, name } = $state({ count: 0, name: '' });

// Correct - maintains reactivity
const state = $state({ count: 0, name: '' });
state.count++;
```

### ❌ Side Effects in $derived

```typescript
// Wrong - $derived must be pure
const result = $derived(() => {
  console.log('calculating...'); // Side effect!
  return value * 2;
});

// Correct - use $effect for side effects
const result = $derived(value * 2);
$effect(() => {
  console.log('value changed:', value);
});
```

### ❌ Missing Cleanup in $effect

```typescript
// Wrong - memory leak
$effect(() => {
  const timer = setInterval(() => {}, 1000);
});

// Correct - cleanup function
$effect(() => {
  const timer = setInterval(() => {}, 1000);
  return () => clearInterval(timer);
});
```

### ❌ Unsafe HTML Rendering

```svelte
<!-- Wrong - XSS vulnerability -->
{@html userInput}

<!-- Better - validate/sanitize first -->
{@html sanitizedContent}
```

## Integration Points

### Pre-commit (via Husky)

ast-grep runs automatically in `npm run check:all`

### VSCode Integration

Install ast-grep extension for real-time feedback

### CI Pipeline

All rule sets run in CI via `check:all`

## Customization

### Adding New Rules

1. Edit appropriate config file
2. Test with specific command (e.g., `npm run ast:migration`)
3. Add to `.ast-grep-ignore` if needed

### Rule Priorities

- **Error**: Security issues, breaking changes, will cause runtime errors
- **Warning**: Best practices, performance, deprecated patterns
- **Info**: Migration hints, style preferences, optional improvements

## Performance Tips

1. **Run specific checks during development**:

   ```bash
   npm run ast:best-practices  # Quick feedback on current code
   ```

2. **Run full scan before commit**:

   ```bash
   npm run ast:all  # Comprehensive check
   ```

3. **Use auto-fix for simple issues**:
   ```bash
   npm run ast:fix  # Auto-fixes what it can
   ```

## Suppressing False Positives

If a rule triggers incorrectly, you can:

1. **File-level ignore**: Add to `.ast-grep-ignore`
2. **Inline comment** (if ast-grep supports it in future)
3. **Adjust rule specificity**: Edit the YAML config

## Further Resources

- [ast-grep documentation](https://ast-grep.github.io/)
- [Svelte 5 migration guide](https://svelte.dev/docs/svelte/v5-migration-guide)
- [Svelte 5 runes](https://svelte.dev/docs/svelte/what-are-runes)

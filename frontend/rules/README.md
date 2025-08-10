# AST-Grep Rules for Svelte 5

This directory contains ast-grep rules for static analysis of the Svelte 5 frontend codebase.

## Rule Files

### `ast-grep-svelte4-migration.yml`

Detects Svelte 4 patterns that should be migrated to Svelte 5:

- Legacy reactive statements (`$:`)
- Old prop syntax (`export let`)
- Slot usage instead of snippets
- Event directives (`on:`) instead of attributes
- Component instantiation with `new`
- `createEventDispatcher` usage

### `ast-grep-svelte5-best-practices.yml`

Enforces Svelte 5 best practices:

- Proper rune usage (`$state`, `$derived`, `$effect`)
- No destructuring of reactive state
- Pure derived values (no side effects)
- Cleanup functions in effects
- Type-safe prop interfaces
- Browser API guards

### `ast-grep-security.yml`

Security-focused rules:

- XSS prevention in `{@html}`
- CSRF token validation
- localStorage data validation
- No password/token logging
- Safe HTML patterns

### `ast-grep-svelte5.yml`

Svelte 5 specific patterns:

- Reactive state handling
- Effect cleanup enforcement
- Snippet rendering patterns
- Rune file location validation

### `.ast-grep.yml` (General Rules)

Project-specific conventions:

- Icon utility usage
- Date formatting patterns
- Logger usage over console
- Error handling patterns
- Performance optimizations

### `simple-test.yml` (Test Rule)

Verification rule to ensure ast-grep is working:

- Finds console usage (guaranteed to exist)
- Used via `npm run ast:test`
- Should always find several matches in logger.ts and test files

## Usage

Run specific rule sets:

```bash
npm run ast:security         # Security checks
npm run ast:migration        # Find Svelte 4 patterns
npm run ast:best-practices   # Svelte 5 best practices
npm run ast:svelte5          # Svelte 5 patterns
```

Run all rules:

```bash
npm run ast:all
```

## Creating New Rules

1. Add rules to appropriate YAML file
2. Follow ast-grep rule syntax
3. Test with: `ast-grep scan src/ --config rules/your-file.yml`
4. Add npm script if needed

## Rule Structure

Each rule follows this pattern:

```yaml
- id: unique-rule-id
  message: 'Description of the issue'
  severity: error|warning|info
  language: typescript|svelte|javascript
  rule:
    pattern: 'code pattern to match'
    # Optional conditions
    where:
      VARIABLE:
        matches: 'specific pattern'
    not:
      contains: 'exclude pattern'
  note: 'How to fix the issue'
```

## Severity Levels

- **error**: Will cause runtime errors, security issues
- **warning**: Best practices, deprecated patterns
- **info**: Style preferences, optional improvements

## Suppressing Rules

Add files/patterns to `.ast-grep-ignore` in the frontend root to exclude from scanning.

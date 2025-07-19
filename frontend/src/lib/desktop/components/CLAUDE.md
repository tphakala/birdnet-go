# Component Development Guide

## Component Inventory

See [README.md](./README.md) for complete component inventory organized by category.

## Component Guidelines

- **Reuse existing components** - Always check the inventory first before creating new components
- **Extend existing components** - If new requirements can be met with non-breaking changes, prefer extending existing components over creating new ones
- **Document new components** - Any new components must be added to [README.md](./README.md) inventory
- Follow Svelte 5 patterns (runes, snippets)
- Use TypeScript for all components
- Include comprehensive tests for each component
- Use proper accessibility attributes
- Follow naming conventions: PascalCase for components
- Organize by functionality in subdirectories

## Component Structure

```
components/
├── charts/     # Chart-related components
├── data/       # Data display components
├── forms/      # Form inputs and controls
├── layout/     # Layout and navigation
├── media/      # Media playback components
├── modals/     # Modal dialogs
└── ui/         # Generic UI components
```

## Testing

Each component should have corresponding test files:

- Unit tests: `.test.ts`
- Component tests: `.test.svelte`

Run tests with: `npm test`

# Desktop Component Guide

The full component inventory, by category, is in [README.md](./README.md).

## Rules

- **Reuse before you create.** Check the inventory first.
- **Extend before you duplicate.** If a non-breaking change to an existing
  component covers the new need, extend it.
- **Document new components** in the README.md inventory in the same change.
- Svelte 5 patterns (runes, snippets, callback props) and TypeScript `Props`
  interfaces for every component.
- PascalCase file names, grouped by function in the subdirectories below.
- Accessibility attributes are part of the component, not an afterthought
  (labels, `aria-describedby`, keyboard support).
- Every component gets a colocated test (`Component.test.ts`); run with `npm test`.

## Layout

```text
components/
├── data/       # data display (tables, lists, cards)
├── database/   # database maintenance and statistics
├── forms/      # form inputs and settings controls
├── media/      # audio playback and spectrograms
├── modals/     # dialogs
├── review/     # detection review workflow
└── ui/         # generic UI primitives (see ui/AGENTS.md)
```

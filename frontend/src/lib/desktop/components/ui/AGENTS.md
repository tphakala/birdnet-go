# UI Primitives

Generic, reusable UI components. **Read `README.md` in this directory before
building any UI**: it documents every component's props, usage, and features.
Some components also have their own `<Component>.md` with deeper notes.

## Use What Exists

There are dozens of primitives here; check before creating a new one, and
prefer composition or a non-breaking extension over a near-duplicate.

- Layout: `Card`, `CollapsibleCard`, `CollapsibleSection`
- Forms: `Input`, `Select`, `DatePicker` (settings controls live in `../forms/`)
- Feedback: `Badge`, `ProgressBar`, `LoadingSpinner`, `ErrorAlert`, `EmptyState`
- Navigation and overlays: `Modal`, `Pagination`, `ActionMenu`
- Data display: `ProcessTable`, `SystemInfoCard`
- Media and status: `AudioLevelIndicator`, `ThemeToggle`, `TimeOfDayIcon`
- Advanced: `MultiStageOperation`, `NotificationBell`, `SearchBox`

```typescript
import Card from '$lib/desktop/components/ui/Card.svelte';
import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils';
```

## Conventions for New Components

- A TypeScript `Props` interface
- A `className` prop merged with `cn()` from `$lib/utils/cn`, plus `...rest`
  spread onto the root element for HTML attributes
- A `children` snippet where the component wraps content
- Tailwind utility classes only; accessibility attributes built in

## Adding a New Primitive

1. Confirm nothing in README.md already covers it
2. Follow the conventions above and Svelte 5 patterns
3. Add a full entry to README.md (props, example, features)
4. Add a colocated `.test.ts`

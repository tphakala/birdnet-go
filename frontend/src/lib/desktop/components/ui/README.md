# Desktop UI Components Library

A comprehensive collection of reusable Svelte 5 components for the BirdNET-Go desktop application.

## 🎯 Design Principles

- **TypeScript First**: All components are built with TypeScript for type safety
- **Svelte 5 Runes**: Modern reactive state management using `$state`, `$derived`, and `$bindable()`
- **DaisyUI Integration**: Consistent styling with DaisyUI CSS framework
- **Accessibility**: ARIA attributes, semantic HTML, and keyboard navigation
- **Snippet Composition**: Flexible content using Svelte 5 snippets
- **Extensible**: All components accept additional HTML attributes

## 📚 Component Categories

### Layout & Structure

- [Card](#card) - Flexible container with header, body, and footer
- [CollapsibleCard](#collapsiblecard) - Expandable card with toggle functionality
- [CollapsibleSection](#collapsiblesection) - Collapsible content sections

### Form Elements

- [Input](#input) - Text, number, date, and other input types
- [Select](#select) - Dropdown selection component
- [DatePicker](#datepicker) - Date selection interface

### Feedback & Status

- [Badge](#badge) - Status indicators and labels
- [ProgressBar](#progressbar) - Progress visualization with thresholds
- [LoadingSpinner](#loadingspinner) - Loading state indicator
- [EmptyState](#emptystate) - Empty content placeholder
- [ErrorAlert](#erroralert) - Error and notification messages
- [NotificationToast](#notificationtoast) - Toast notifications

### Navigation & Interaction

- [Modal](#modal) - Dialog and confirmation modals
- [Pagination](#pagination) - Page navigation component
- [ActionMenu](#actionmenu) - Context action menu

### Data Display

- [ProcessTable](#processtable) - System process information
- [SystemInfoCard](#systeminfocard) - System information display
- [SettingsCard](#settingscard) - Configuration settings card
- [SettingsSection](#settingssection) - Settings organization

### Media & Audio

- [AudioLevelIndicator](#audiolevelindicator) - Audio level visualization
- [ThemeToggle](#themetoggle) - Theme switching component
- [TimeOfDayIcon](#timeofdayicon) - Time-based icon display

### Advanced

- [MultiStageOperation](#multistageoperation) - Multi-step operation handler
- [NotificationBell](#notificationbell) - Notification indicator
- [SearchBox](#searchbox) - Search input with functionality

### Utilities

- [image-utils.ts](#image-utils) - Image handling utilities

---

## 📖 Component Documentation

### Badge

**File**: `Badge.svelte`

A versatile badge component for status indicators and labels.

```typescript
interface Props {
  variant?:
    | 'primary'
    | 'secondary'
    | 'accent'
    | 'neutral'
    | 'info'
    | 'success'
    | 'warning'
    | 'error'
    | 'ghost';
  size?: 'xs' | 'sm' | 'md' | 'lg';
  text?: string;
  outline?: boolean;
  className?: string;
  children?: Snippet;
}
```

**Usage:**

```svelte
<Badge variant="success" size="sm" text="Active" />
<Badge variant="warning" outline>
  {#snippet children()}⚠️ Warning{/snippet}
</Badge>
```

**Features:**

- 9 color variants
- 4 size options
- Outline styling
- Snippet or text content

---

### Card

**File**: `Card.svelte`

Flexible container component with header, body, and footer sections.

```typescript
interface Props {
  title?: string;
  padding?: boolean;
  className?: string;
  header?: Snippet;
  children?: Snippet;
  footer?: Snippet;
}
```

**Usage:**

```svelte
<Card title="Card Title" padding={true}>
  {#snippet children()}
    Card content goes here
  {/snippet}
  {#snippet footer()}
    <button class="btn btn-primary">Action</button>
  {/snippet}
</Card>
```

**Features:**

- Optional title or custom header
- Configurable padding
- Three content sections

---

### Modal

**File**: `Modal.svelte`

Comprehensive modal component with multiple types and configurations.

```typescript
interface Props {
  isOpen: boolean;
  title?: string;
  size?: 'sm' | 'md' | 'lg' | 'xl' | 'full';
  type?: 'default' | 'confirm' | 'alert';
  confirmLabel?: string;
  cancelLabel?: string;
  confirmVariant?: 'primary' | 'secondary' | 'accent' | 'info' | 'success' | 'warning' | 'error';
  closeOnBackdrop?: boolean;
  closeOnEsc?: boolean;
  showCloseButton?: boolean;
  loading?: boolean;
  className?: string;
  onClose?: () => void;
  onConfirm?: () => void | Promise<void>;
  header?: Snippet;
  children?: Snippet;
  footer?: Snippet;
}
```

**Usage:**

```svelte
<Modal
  isOpen={showModal}
  title="Confirm Action"
  type="confirm"
  confirmLabel="Delete"
  confirmVariant="error"
  onClose={() => (showModal = false)}
  onConfirm={handleDelete}
>
  {#snippet children()}
    Are you sure you want to delete this item?
  {/snippet}
</Modal>
```

**Features:**

- Multiple modal types
- 5 size variants
- Async confirm handlers
- Keyboard/backdrop controls
- Loading states
- Full accessibility

---

### Input

**File**: `Input.svelte`

Comprehensive input component supporting multiple input types.

```typescript
interface Props {
  type?:
    | 'text'
    | 'email'
    | 'password'
    | 'number'
    | 'date'
    | 'datetime-local'
    | 'time'
    | 'url'
    | 'tel';
  value?: string | number;
  id?: string;
  name?: string;
  placeholder?: string;
  disabled?: boolean;
  readonly?: boolean;
  required?: boolean;
  className?: string;
  min?: string | number;
  max?: string | number;
  step?: string | number;
}
```

**Usage:**

```svelte
<Input type="text" bind:value={name} placeholder="Enter name" required />
<Input type="number" bind:value={count} min={0} max={100} step={1} />
```

**Features:**

- Multiple input types
- Two-way binding
- Form validation
- Number constraints

---

### Select

**File**: `Select.svelte`

Dropdown selection component with option support.

```typescript
interface Option {
  value: string;
  label: string;
}

interface Props {
  value: string;
  options: Option[];
  id?: string;
  className?: string;
  disabled?: boolean;
  placeholder?: string;
}
```

**Usage:**

```svelte
<Select
  bind:value={selectedOption}
  options={[
    { value: 'option1', label: 'Option 1' },
    { value: 'option2', label: 'Option 2' },
  ]}
  placeholder="Choose option"
/>
```

**Features:**

- Structured option format
- Two-way binding
- Placeholder support
- Disabled state

---

### ProgressBar

**File**: `ProgressBar.svelte`

Advanced progress bar with dynamic coloring and animations.

```typescript
interface ColorThreshold {
  value: number;
  variant: ProgressVariant;
}

interface Props {
  value: number;
  max?: number;
  size?: 'xs' | 'sm' | 'md' | 'lg';
  variant?: 'primary' | 'secondary' | 'accent' | 'info' | 'success' | 'warning' | 'error';
  showLabel?: boolean;
  labelFormat?: (value: number, max: number) => string;
  colorThresholds?: ColorThreshold[];
  striped?: boolean;
  animated?: boolean;
  className?: string;
  barClassName?: string;
}
```

**Usage:**

```svelte
<ProgressBar
  value={75}
  max={100}
  showLabel={true}
  colorThresholds={[
    { value: 30, variant: 'error' },
    { value: 70, variant: 'warning' },
    { value: 100, variant: 'success' },
  ]}
  striped={true}
  animated={true}
/>
```

**Features:**

- Dynamic color thresholds
- Custom label formatting
- Striped and animated styles
- Full accessibility

---

### LoadingSpinner

**File**: `LoadingSpinner.svelte`

Simple loading indicator with size and color options.

```typescript
interface Props {
  size?: 'xs' | 'sm' | 'md' | 'lg' | 'xl';
  color?: string;
  label?: string;
}
```

**Usage:**

```svelte
<LoadingSpinner size="md" label="Loading..." />
<LoadingSpinner size="lg" color="text-secondary" />
```

**Features:**

- 5 size options
- Custom color support
- Screen reader accessibility

---

### EmptyState

**File**: `EmptyState.svelte`

Empty state placeholder with optional action button.

```typescript
interface ActionConfig {
  label: string;
  onClick: () => void;
}

interface Props {
  icon?: Snippet;
  title?: string;
  description?: string;
  action?: ActionConfig | null;
  className?: string;
  children?: Snippet;
}
```

**Usage:**

```svelte
<EmptyState
  title="No Data Found"
  description="There are no items to display"
  action={{
    label: 'Add Item',
    onClick: handleAddItem,
  }}
/>
```

**Features:**

- Default or custom icons
- Optional action button
- Flexible content

---

### ErrorAlert

**File**: `ErrorAlert.svelte`

Alert component for errors, warnings, and notifications.

```typescript
interface Props {
  message?: string;
  type?: 'error' | 'warning' | 'info' | 'success';
  dismissible?: boolean;
  onDismiss?: () => void;
  className?: string;
  children?: Snippet;
}
```

**Usage:**

```svelte
<ErrorAlert type="error" message="An error occurred" dismissible={true} onDismiss={handleDismiss} />
```

**Features:**

- 4 alert types with icons
- Dismissible functionality
- Message or snippet content

---

### Image Utils

**File**: `image-utils.ts`

Utility functions for image handling.

```typescript
export function handleBirdImageError(e: Event): void;
```

**Usage:**

```svelte
<script>
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils.js';
</script>

<img src="/api/species-image/{species}" alt="Bird" onerror={handleBirdImageError} />
```

**Features:**

- Bird-specific error handling
- Automatic placeholder fallback

---

## 🔧 Development Guidelines

### Creating New Components

1. **Use TypeScript**: Always define proper interfaces for props
2. **Follow Naming**: Use PascalCase for component files
3. **Include Tests**: Create corresponding `.test.ts` files
4. **Document**: Add entry to this README
5. **Accessibility**: Include ARIA attributes and semantic HTML

### Component Template

```svelte
<script lang="ts">
  import type { Snippet } from 'svelte';
  import { cn } from '$lib/utils/cn.js';

  interface Props {
    // Define props here
    className?: string;
    children?: Snippet;
  }

  let { className = '', children, ...rest }: Props = $props();
</script>

<!-- Component markup -->
<div class={cn('base-styles', className)} {...rest}>
  {#if children}
    {@render children()}
  {/if}
</div>
```

### Testing Guidelines

- Create `.test.ts` files for unit tests
- Test component props and behavior
- Include accessibility testing
- Use `@testing-library/svelte` for DOM testing

### Styling Guidelines

- Use DaisyUI classes when possible
- Create component-specific styles in `<style>` blocks
- Support `className` prop for customization
- Follow responsive design principles

---

## 🚀 Usage Best Practices

### Prefer Existing Components

- Always check this inventory before creating new components
- Extend existing components rather than duplicating functionality
- Use composition patterns with snippets for flexibility

### Import Patterns

```svelte
// Correct import path import Badge from '$lib/desktop/components/ui/Badge.svelte'; import {handleBirdImageError}
from '$lib/desktop/components/ui/image-utils.js';
```

### Snippet Usage

```svelte
<Card>
  {#snippet header()}
    <h2>Custom Header</h2>
  {/snippet}

  {#snippet children()}
    <p>Card content</p>
  {/snippet}

  {#snippet footer()}
    <button class="btn">Action</button>
  {/snippet}
</Card>
```

### State Management

```svelte
<script>
  // Reactive state
  let count = $state(0);

  // Derived values
  let doubled = $derived(count * 2);

  // Bindable props
  let { value = $bindable() } = $props();
</script>
```

---

## 📝 Contributing

When adding new components:

1. Follow the established patterns
2. Add TypeScript interfaces
3. Include accessibility features
4. Create test files
5. Update this README
6. Add usage examples

For questions or suggestions, please refer to the main project documentation.

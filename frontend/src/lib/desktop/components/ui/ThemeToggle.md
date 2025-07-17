# ThemeToggle Component

A Svelte 5 TypeScript component that provides a theme switcher between light and dark modes using DaisyUI's swap component.

## Features

- Toggle between light and dark themes
- Persists theme preference in localStorage
- Respects system theme preference (if no user preference is set)
- Smooth animated transition between sun and moon icons
- Optional tooltip on hover
- Multiple size options (sm, md, lg)
- Fully accessible with ARIA labels
- Works with existing theme system (compatible with server-side theme detection)

## Usage

```svelte
<script>
  import ThemeToggle from '$lib/components/ui/ThemeToggle.svelte';
</script>

<ThemeToggle />
```

## Props

- `className` (string, optional): Additional CSS classes for styling
- `showTooltip` (boolean, optional): Whether to show tooltip on hover (default: true)
- `size` ('sm' | 'md' | 'lg', optional): Size of the toggle button (default: 'sm')

## Theme Store

The component uses a custom Svelte store for theme management:

```typescript
import { theme } from '$lib/stores/theme';

// Set theme programmatically
theme.set('dark');
theme.set('light');

// Toggle theme
theme.toggle();

// Subscribe to theme changes
theme.subscribe(currentTheme => {
  console.log('Theme changed to:', currentTheme);
});
```

## How It Works

1. **Initial Theme Detection**:
   - Checks localStorage for saved preference
   - Falls back to system preference via `prefers-color-scheme`
   - Defaults to light theme if no preference

2. **Theme Application**:
   - Sets `data-theme` attribute on document root
   - Sets `data-theme-controller` for compatibility
   - Saves preference to localStorage

3. **System Theme Monitoring**:
   - Listens for system theme changes
   - Only applies if user hasn't set an explicit preference

## Integration with Existing System

The component is designed to work seamlessly with the existing theme system:

1. **Server-side compatibility**: The theme is applied immediately on mount to prevent flash
2. **localStorage sync**: Uses the same 'theme' key as the existing system
3. **DaisyUI integration**: Uses the same swap component and classes

## Examples

### Basic Usage

```svelte
<ThemeToggle />
```

### In a Navigation Bar

```svelte
<nav class="navbar">
  <div class="navbar-end">
    <AudioLevelIndicator />
    <NotificationBell />
    <ThemeToggle />
  </div>
</nav>
```

### Without Tooltip

```svelte
<ThemeToggle showTooltip={false} />
```

### Different Sizes

```svelte
<ThemeToggle size="sm" />
<!-- Small (default) -->
<ThemeToggle size="md" />
<!-- Medium -->
<ThemeToggle size="lg" />
<!-- Large -->
```

### In Settings Panel

```svelte
<div class="flex items-center justify-between">
  <span>Dark Mode</span>
  <ThemeToggle showTooltip={false} />
</div>
```

### Programmatic Control

```svelte
<script>
  import { theme } from '$lib/stores/theme';

  function setDarkTheme() {
    theme.set('dark');
  }
</script>

<button onclick={setDarkTheme}>Enable Dark Mode</button>
```

## Styling

The component uses DaisyUI's swap component classes and can be customized:

- Uses `btn-ghost` for minimal styling
- Supports size modifiers via the `size` prop
- Can be styled with custom classes via `className` prop

## Accessibility

- Proper ARIA label for screen readers
- Keyboard accessible
- Clear visual feedback for current state
- Tooltip provides additional context (when enabled)

## Browser Support

- All modern browsers with localStorage support
- CSS custom properties for theming
- Media queries for system theme detection

## Migration from Old System

When migrating from the HTMX/Alpine.js version:

1. The component maintains the same localStorage key ('theme')
2. Uses the same data attributes on document root
3. Visual appearance matches the original implementation
4. No changes needed to existing CSS that depends on `[data-theme]`

## Performance Considerations

- Theme is applied synchronously to prevent flash
- Store subscription is cleaned up on component destroy
- System theme listener is removed when component unmounts
- Minimal re-renders using Svelte 5's fine-grained reactivity

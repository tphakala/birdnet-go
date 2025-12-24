# SearchBox Component

A Svelte 5 TypeScript component that provides a search input with debounced searching, keyboard shortcuts, and page-specific visibility.

## Features

- Debounced search input (200ms delay)
- Keyboard shortcuts (Cmd/Ctrl + K to focus)
- Page-specific visibility
- Loading state during search
- Responsive design with size variants
- Escape key to clear search
- Form submission support
- Customizable placeholder text

## Usage

```svelte
<script>
  import SearchBox from '$lib/components/ui/SearchBox.svelte';
</script>

<SearchBox
  currentPage="dashboard"
  onSearch={query => console.log('Search:', query)}
  onNavigate={url => (window.location.href = url)}
/>
```

## Props

- `className` (string, optional): Additional CSS classes
- `placeholder` (string, optional): Placeholder text (default: "Search detections")
- `value` (string, optional): Initial search value
- `onSearch` (function, optional): Callback when search is performed
- `onNavigate` (function, optional): Callback for navigation with search URL
- `size` ('sm' | 'md' | 'lg', optional): Size variant (default: 'sm')
- `showOnPages` (string[], optional): Pages where search is visible (default: ['dashboard'])
- `currentPage` (string, optional): Current page name (default: 'dashboard')

## API Integration Status

⚠️ **TODO**: The search functionality currently has no API implementation. When the v2 API endpoints are available, the following needs to be implemented:

### Expected API Endpoint

```
GET /api/v2/detections/search
Parameters:
  - query: string (search term)
  - limit: number (optional, default: 20)
  - offset: number (optional, for pagination)
  - sort: string (optional, e.g., 'date_desc', 'confidence_desc')
```

### Implementation Areas

1. **In `performSearch()` function**:

```typescript
// Replace the TODO section with:
try {
  const params = new URLSearchParams({
    query: searchQuery,
    limit: '20',
  });

  const response = await fetch(`/api/v2/detections/search?${params}`);
  if (response.ok) {
    const results = await response.json();
    // Handle results - update UI or navigate
  }
} catch (error) {
  console.error('Search failed:', error);
  // Show error state
}
```

2. **Navigation**: Currently logs navigation intent. Should integrate with your router:

```typescript
// Instead of onNavigate callback, use your router:
import { goto } from '$app/navigation'; // SvelteKit example
goto(`/search?query=${encodeURIComponent(searchQuery)}`);
```

## Examples

### Basic Dashboard Header

```svelte
<header class="flex items-center justify-between">
  <h1>Dashboard</h1>
  <SearchBox currentPage="dashboard" />
  <div class="flex gap-2">
    <NotificationBell />
    <ThemeToggle />
  </div>
</header>
```

### Custom Configuration

```svelte
<SearchBox
  placeholder="Search birds, locations, dates..."
  showOnPages={['dashboard', 'analytics', 'search']}
  currentPage={currentPageName}
  size="md"
/>
```

### With Search Handler

```svelte
<script>
  async function handleSearch(query) {
    // Perform custom search logic
    const results = await searchAPI(query);
    displayResults(results);
  }
</script>

<SearchBox onSearch={handleSearch} />
```

## Keyboard Shortcuts

- **Cmd/Ctrl + K**: Focus the search input from anywhere on the page
- **Escape**: Clear the search input and remove focus
- **Enter**: Submit the search immediately (bypasses debounce)

## Responsive Behavior

The component adapts to different screen sizes:

- **Mobile**: Smaller input with compact spacing
- **Desktop**: Larger input with keyboard shortcut hint
- **Size variants**:
  - `sm`: Responsive sizing (small on mobile, medium on desktop)
  - `md`: Medium size across all devices
  - `lg`: Large size across all devices

## Page-Specific Visibility

The search box only appears on specified pages:

```svelte
<!-- Only visible on dashboard -->
<SearchBox showOnPages={['dashboard']} currentPage="dashboard" /> ✓ Visible

<!-- Not visible on settings -->
<SearchBox showOnPages={['dashboard']} currentPage="settings" /> ✗ Hidden
```

## Styling

The component uses Tailwind CSS utility classes and can be customized:

- Rounded corners for modern appearance
- Focus outline for accessibility
- Loading spinner during search
- Smooth opacity transitions

## Future Enhancements

When API is available:

1. Implement autocomplete/suggestions dropdown
2. Add search history
3. Support for advanced search filters
4. Real-time result preview
5. Search result highlighting
6. Pagination support

## Accessibility

- Proper ARIA labels
- Keyboard navigation support
- Screen reader announcements for search state
- Clear visual focus indicators

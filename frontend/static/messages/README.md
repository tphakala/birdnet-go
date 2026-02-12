# BirdNET-Go Frontend Internationalization (i18n)

This directory contains translation files for the BirdNET-Go frontend application.

## Structure

Translation files are organized by language code:

- `en.json` - English (default)
- `de.json` - German
- `es.json` - Spanish
- `fi.json` - Finnish
- `fr.json` - French
- `nl.json` - Dutch
- `pl.json` - Polish
- `pt.json` - Portuguese
- `sk.json` - Slovak

## Translation Key Organization

To maximize reusability and avoid duplication, translations are organized by functionality rather than by component:

### 1. Common UI Elements (`common.*`)

```json
{
  "common": {
    "ui": {
      "loading": "Loading...",
      "noData": "No data available",
      "empty": "No items found",
      "error": "An error occurred",
      "retry": "Try again",
      "dismiss": "Dismiss",
      "showMore": "Show more",
      "showLess": "Show less",
      "selectAll": "Select all",
      "selectNone": "Select none",
      "required": "Required",
      "optional": "Optional"
    },
    "buttons": {
      "confirm": "Confirm",
      "cancel": "Cancel",
      "save": "Save",
      "delete": "Delete",
      "edit": "Edit",
      "close": "Close",
      "apply": "Apply",
      "reset": "Reset",
      "back": "Back",
      "next": "Next",
      "previous": "Previous",
      "yes": "Yes",
      "no": "No",
      "ok": "OK"
    },
    "status": {
      "success": "Success",
      "error": "Error",
      "warning": "Warning",
      "info": "Information",
      "pending": "Pending",
      "processing": "Processing",
      "completed": "Completed",
      "failed": "Failed"
    },
    "validation": {
      "required": "This field is required",
      "invalid": "Invalid value",
      "minLength": "Must be at least {min} characters",
      "maxLength": "Must be no more than {max} characters",
      "minValue": "Must be at least {min}",
      "maxValue": "Must be no more than {max}",
      "email": "Invalid email address",
      "url": "Invalid URL",
      "pattern": "Invalid format"
    },
    "aria": {
      "closeModal": "Close modal",
      "dismissAlert": "Dismiss alert",
      "toggleDropdown": "Toggle dropdown",
      "sortAscending": "Sort ascending",
      "sortDescending": "Sort descending",
      "expandSection": "Expand section",
      "collapseSection": "Collapse section",
      "playAudio": "Play audio",
      "pauseAudio": "Pause audio",
      "loading": "Loading content"
    }
  }
}
```

### 2. Form Components (`forms.*`)

```json
{
  "forms": {
    "placeholders": {
      "text": "Enter {field}",
      "select": "Select {field}",
      "search": "Search...",
      "date": "Select date",
      "dateRange": "Select date range",
      "number": "Enter number",
      "password": "Enter password",
      "url": "Enter URL",
      "email": "Enter email"
    },
    "labels": {
      "showPassword": "Show password",
      "hidePassword": "Hide password",
      "copyToClipboard": "Copy to clipboard",
      "clearSelection": "Clear selection",
      "selectOption": "Select an option"
    },
    "help": {
      "passwordStrength": "Password strength: {strength}",
      "charactersRemaining": "{count} characters remaining",
      "dateFormat": "Format: {format}"
    }
  }
}
```

### 3. Data Display Components (`dataDisplay.*`)

```json
{
  "dataDisplay": {
    "table": {
      "noData": "No data available",
      "loading": "Loading data...",
      "error": "Failed to load data",
      "sortBy": "Sort by {column}",
      "rowsPerPage": "Rows per page",
      "pageInfo": "Showing {from} to {to} of {total} results"
    },
    "pagination": {
      "previous": "Previous",
      "next": "Next",
      "page": "Page {current} of {total}",
      "goToPage": "Go to page {page}",
      "firstPage": "First page",
      "lastPage": "Last page"
    },
    "stats": {
      "total": "Total",
      "average": "Average",
      "min": "Minimum",
      "max": "Maximum",
      "count": "Count"
    }
  }
}
```

### 4. Media Components (`media.*`)

```json
{
  "media": {
    "audio": {
      "play": "Play",
      "pause": "Pause",
      "stop": "Stop",
      "volume": "Volume",
      "mute": "Mute",
      "unmute": "Unmute",
      "download": "Download audio",
      "currentTime": "{current} / {total}",
      "loading": "Loading audio...",
      "error": "Failed to load audio"
    }
  }
}
```

### 5. Feature-Specific Translations

Each major feature has its own namespace:

- `navigation.*` - Navigation menu items
- `dashboard.*` - Dashboard page and components
- `detections.*` - Detections page and related features
- `settings.*` - Settings pages and forms
- `about.*` - About page content
- `system.*` - System information page
- `analytics.*` - Analytics and charts
- `search.*` - Search functionality

## Best Practices

### 1. DRY (Don't Repeat Yourself)

- Use common translations across components when possible
- Before adding a new translation, check if it exists in `common.*`
- Example: Use `common.buttons.save` instead of creating `settings.save`, `profile.save`, etc.

### 2. Parameterized Translations

Use placeholders for dynamic content:

```json
{
  "greeting": "Hello, {name}!",
  "itemCount": "{count, plural, =0 {No items} =1 {1 item} other {{count} items}}"
}
```

### 3. Contextual Clarity

When the same word has different meanings in different contexts, create specific keys:

```json
{
  "common.buttons.save": "Save", // Generic save button
  "settings.save.description": "Save your preferences", // Specific context
  "detections.save.recording": "Save recording to disk" // Different meaning
}
```

### 4. Accessibility

Always include translations for aria-labels and screen reader text:

```json
{
  "common.aria.closeModal": "Close modal window",
  "common.aria.loading": "Loading content, please wait"
}
```

### 5. Naming Conventions

- Use lowercase with dots for nesting: `section.subsection.key`
- Use camelCase for multi-word keys: `firstName`, not `first_name`
- Be descriptive but concise: `settings.audio.device` not `s.a.d`

## Component Implementation

### Using Translations in Components

```svelte
<script>
  import { t } from '$lib/i18n/index.js';

  // Simple translation
  const label = t('common.buttons.save');

  // With parameters
  const greeting = t('dashboard.welcome', { name: userName });

  // In template
</script>

<button>{t('common.buttons.save')}</button><p>{t('forms.validation.minLength', { min: 8 })}</p>
```

### Priority Components to Update

1. **High Priority** (Most visible/frequently used):
   - Modal.svelte
   - EmptyState.svelte
   - ErrorAlert.svelte
   - Pagination.svelte
   - DataTable.svelte

2. **Medium Priority** (Form components):
   - All form field components
   - Validation messages
   - Help text

3. **Low Priority** (Already functional):
   - Components with minimal text
   - Developer-facing components

## Adding New Languages

1. Create a new file with the language code (e.g., `it.json` for Italian)
2. Copy the structure from `en.json`
3. Translate all values while keeping the keys identical
4. Add the language to the language selector component

## Testing Translations

1. Change language in the UI language selector
2. Verify all text updates correctly
3. Check for missing translations (will show the key)
4. Test parameterized translations with different values
5. Verify RTL languages display correctly (if supported)

## Contributing Translations

When contributing translations:

1. Maintain the exact same key structure as `en.json`
2. Don't remove keys even if untranslated (leave English as fallback)
3. Ensure special characters are properly escaped in JSON
4. Test your translations in the application
5. Consider cultural context and idioms

## Crowdin Integration

This project is set up to work with Crowdin for collaborative translation management. The file structure and naming conventions are compatible with Crowdin's requirements.

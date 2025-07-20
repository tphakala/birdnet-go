# UI Components Library - LLM Development Guide

## 📖 Component Inventory & Documentation

**SEE `README.md` in this directory for complete component documentation**

The README.md file contains comprehensive documentation of all available UI components including:

- Component interfaces and props
- Usage examples
- Features and capabilities
- Development guidelines

## 🎯 Key Guidelines for LLMs

### 1. Always Use Existing Components First

- **Check the README.md inventory before creating new components**
- Prefer composition and extension over duplication
- 40+ components available covering most UI needs

### 2. Import Paths

```svelte
// UI components import ComponentName from '$lib/desktop/components/ui/ComponentName.svelte'; //
Utilities import {handleBirdImageError} from '$lib/desktop/components/ui/image-utils.js';
```

### 3. Common Component Categories Available

- **Layout**: Card, CollapsibleCard, CollapsibleSection
- **Forms**: Input, Select, DatePicker
- **Feedback**: Badge, ProgressBar, LoadingSpinner, ErrorAlert, EmptyState
- **Navigation**: Modal, Pagination, ActionMenu
- **Data Display**: ProcessTable, SystemInfoCard, SettingsCard
- **Media**: AudioLevelIndicator, ThemeToggle, TimeOfDayIcon
- **Advanced**: MultiStageOperation, NotificationBell, SearchBox

### 4. Required for New Components

If you must create a new component:

1. Add TypeScript interface
2. Follow Svelte 5 patterns ($state, $derived, snippets)
3. Include accessibility features
4. Add entry to README.md with full documentation
5. Create `.test.ts` file
6. Use DaisyUI styling

### 5. Component Patterns

- Use `className` prop for customization
- Support HTML attribute spreading with `{...rest}`
- Include `children` snippet for flexible content
- Define proper TypeScript interfaces

## 🚨 Important

**Always refer to README.md in this directory for complete component documentation and usage examples before implementing any UI functionality.**

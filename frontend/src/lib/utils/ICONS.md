# Centralized Icon System

**Location: `$lib/utils/icons.ts`**

All SVG icons are centralized to avoid duplication and ensure consistency.

## Usage

```svelte
<script>
  import { navigationIcons, actionIcons, systemIcons } from '$lib/utils/icons';
</script>

{@html navigationIcons.close}
{@html actionIcons.search} 
{@html systemIcons.clock}
```

## Categories

- **navigationIcons**: close, arrows, chevrons, menu
- **actionIcons**: edit, delete, save, copy, add, search, filter
- **systemIcons**: clock, calendar, settings, user, loading, eye
- **alertIcons**: error, warning, info, success (paths only)
- **mediaIcons**: play, pause, download, volume (full HTML)
- **dataIcons**: chart, document, folder, table

## Adding New Icons

1. Add to appropriate category in `icons.ts`
2. Use consistent sizing (`h-4 w-4` or `h-5 w-5`)  
3. Include proper stroke/fill attributes
4. Use `stroke="currentColor"` for theming

**NEVER create inline SVGs in components - always use centralized icons!**
# Icon System

**Library: `@lucide/svelte`**

Icons are provided by Lucide, a beautiful and consistent icon library. Import icons directly from `@lucide/svelte` in your components.

## Usage

```svelte
<script>
  import { X, Search, Clock, Settings, Play, Pause } from '@lucide/svelte';
</script>

<button>
  <X class="h-4 w-4" />
  Close
</button>

<button>
  <Search class="h-5 w-5" />
  Search
</button>
```

## Common Icons

| Purpose       | Icon Import                              | Example                                    |
| ------------- | ---------------------------------------- | ------------------------------------------ |
| Close/Cancel  | `X`                                      | `<X class="h-4 w-4" />`                    |
| Search        | `Search`                                 | `<Search class="h-5 w-5" />`               |
| Settings      | `Settings`                               | `<Settings class="h-5 w-5" />`             |
| Play/Pause    | `Play`, `Pause`                          | `<Play class="h-4 w-4" />`                 |
| Navigation    | `ChevronLeft`, `ChevronRight`, `Menu`    | `<Menu class="h-6 w-6" />`                 |
| Actions       | `Edit`, `Trash2`, `Save`, `Copy`, `Plus` | `<Edit class="h-4 w-4" />`                 |
| Status        | `Check`, `AlertCircle`, `Info`, `Ban`    | `<Check class="h-4 w-4" />`                |
| Media         | `Volume2`, `VolumeX`, `Download`         | `<Volume2 class="h-5 w-5" />`              |
| Time/Calendar | `Clock`, `Calendar`                      | `<Clock class="h-4 w-4" />`                |
| User          | `User`, `LogIn`, `LogOut`                | `<User class="h-5 w-5" />`                 |
| Data          | `BarChart2`, `FileText`, `Folder`        | `<BarChart2 class="h-5 w-5" />`            |
| Arrows        | `ArrowLeft`, `ArrowRight`, `ArrowUp`     | `<ArrowLeft class="h-4 w-4" />`            |
| Loading       | `Loader2` (with animation)               | `<Loader2 class="h-4 w-4 animate-spin" />` |

## Styling

Icons accept standard HTML attributes and can be styled with Tailwind classes:

```svelte
<!-- Size -->
<Search class="h-4 w-4" />
<Search class="h-5 w-5" />
<Search class="h-6 w-6" />

<!-- Color (inherits currentColor by default) -->
<Search class="text-gray-500" />
<Search class="text-primary" />

<!-- Stroke width -->
<Search strokeWidth={1.5} />
<Search strokeWidth={2.5} />

<!-- Animation -->
<Loader2 class="h-4 w-4 animate-spin" />
```

## Finding Icons

Browse all available icons at: https://lucide.dev/icons

## Best Practices

1. **Import only what you need** - Tree-shaking removes unused icons
2. **Use consistent sizing** - Stick to `h-4 w-4` or `h-5 w-5` for most UI elements
3. **Inherit color** - Let icons inherit `currentColor` from parent text color
4. **Add aria labels** - For icon-only buttons, add `aria-label` for accessibility

```svelte
<button aria-label="Close" onclick={handleClose}>
  <X class="h-4 w-4" />
</button>
```

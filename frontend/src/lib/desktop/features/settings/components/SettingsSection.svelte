<!--
  Settings Section Component
  
  Purpose: A reusable wrapper for settings sections that provides consistent styling,
  change detection, and collapsible functionality. Built on top of SettingsCard.
  
  Features:
  - Automatic change detection when originalData and currentData provided
  - Manual change detection via hasChanges prop
  - Visual indicator when section has unsaved changes
  - Consistent section styling and spacing
  - Support for title and description
  - Flexible content via Svelte 5 snippets
  
  Props:
  - title: Section title (required)
  - description: Optional section description
  - defaultOpen: Whether section starts expanded (optional)
  - className: Additional CSS classes (optional)
  - originalData: Original data for change detection (optional)
  - currentData: Current data for change detection (optional)
  - hasChanges: Manual override for change detection (optional)
  - children: Section content snippet (optional)
  
  Performance Optimizations:
  - Uses $derived correctly for reactive change detection
  - Avoids JSON.stringify for better performance with proxies
  - Minimal re-renders through proper reactivity
  
  @component
-->
<script lang="ts">
  import SettingsCard from '$lib/desktop/features/settings/components/SettingsCard.svelte';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import type { Snippet } from 'svelte';

  interface Props {
    title: string;
    description?: string;
    defaultOpen?: boolean;
    className?: string;
    originalData?: unknown;
    currentData?: unknown;
    children?: Snippet;
    hasChanges?: boolean;
  }

  let {
    title,
    description,
    className = '',
    originalData,
    currentData,
    children,
    hasChanges,
    ...rest
  }: Props = $props();

  // PERFORMANCE OPTIMIZATION: Use $derived with efficient deep comparison
  let sectionHasChanges = $derived(
    // If hasChanges is explicitly provided, use that
    hasChanges !== undefined
      ? hasChanges
      : // Otherwise, use automatic detection with optimized deep comparison
        hasSettingsChanged(originalData, currentData)
  );
</script>

<SettingsCard {title} {description} {className} hasChanges={sectionHasChanges} {...rest}>
  {#if children}
    {@render children()}
  {/if}
</SettingsCard>

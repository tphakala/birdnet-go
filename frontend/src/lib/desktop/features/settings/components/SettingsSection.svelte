<script lang="ts">
  import SettingsCard from '$lib/desktop/features/settings/components/SettingsCard.svelte';
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

  // Detect if this section has changes
  let sectionHasChanges = $derived(() => {
    // If hasChanges is explicitly provided, use that
    if (hasChanges !== undefined) return hasChanges;

    // Otherwise, use automatic detection if originalData and currentData are provided
    if (!originalData || !currentData) return false;
    return JSON.stringify(originalData) !== JSON.stringify(currentData);
  });
</script>

<SettingsCard
  {title}
  {description}
  {className}
  hasChanges={sectionHasChanges()}
  {...rest}
>
  {#if children}
    {@render children()}
  {/if}
</SettingsCard>

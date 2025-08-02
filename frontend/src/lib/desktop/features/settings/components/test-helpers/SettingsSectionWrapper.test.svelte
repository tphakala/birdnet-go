<!--
  Test wrapper component for SettingsSection
  Used to test snippet props in Svelte 5
-->
<script lang="ts">
  import SettingsSection from '../SettingsSection.svelte';

  export let title = '';
  export let description = '';
  export let hasChanges = undefined;
  export let originalData = undefined;
  export let currentData = undefined;
  export let className = '';
  export let defaultOpen = undefined;
  export let childContent = '';
</script>

<SettingsSection
  {title}
  {description}
  {hasChanges}
  {originalData}
  {currentData}
  {className}
  {defaultOpen}
>
  {#snippet children()}
    {#if childContent}
      <div data-testid="section-content">{childContent}</div>
    {/if}
  {/snippet}
</SettingsSection>

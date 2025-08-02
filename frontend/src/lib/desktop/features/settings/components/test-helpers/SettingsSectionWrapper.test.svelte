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
  export let padding = true;
  export let showCustomHeader = false;
  export let showCustomFooter = false;
  export let childContent = '';
  export let headerContent = '';
  export let footerContent = '';
</script>

<SettingsSection
  {title}
  {description}
  {hasChanges}
  {originalData}
  {currentData}
  {className}
  {defaultOpen}
  {padding}
>
  {#snippet children()}
    {#if childContent}
      <div data-testid="section-content">{childContent}</div>
    {/if}
  {/snippet}

  {#snippet header()}
    {#if showCustomHeader}
      <div data-testid="custom-header">{headerContent}</div>
    {/if}
  {/snippet}

  {#snippet footer()}
    {#if showCustomFooter}
      <div data-testid="custom-footer">{footerContent}</div>
    {/if}
  {/snippet}
</SettingsSection>

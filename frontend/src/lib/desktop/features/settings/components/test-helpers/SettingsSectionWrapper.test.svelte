<!--
  Test wrapper component for SettingsSection
  Used to test snippet props in Svelte 5
-->
<script lang="ts">
  import SettingsSection from '../SettingsSection.svelte';

  export let title: string = '';
  export let description: string = '';
  export let hasChanges: boolean | undefined = undefined;
  export let originalData: unknown | undefined = undefined;
  export let currentData: unknown | undefined = undefined;
  export let className: string = '';
  export let defaultOpen: boolean | undefined = undefined;
  export let childContent: string = '';
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

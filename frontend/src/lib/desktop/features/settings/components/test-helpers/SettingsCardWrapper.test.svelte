<!--
  Test wrapper component for SettingsCard
  Used to test snippet props in Svelte 5
-->
<script lang="ts">
  import SettingsCard from '../SettingsCard.svelte';

  export let title: string = '';
  export let description: string = '';
  export let hasChanges: boolean = false;
  export let className: string = '';
  export let padding: boolean = true;
  export let showCustomHeader: boolean = false;
  export let showCustomFooter: boolean = false;
  export let childContent: string = '';
  export let headerContent: string = '';
  export let footerContent: string = '';
</script>

<SettingsCard {title} {description} {hasChanges} {className} {padding}>
  {#snippet children()}
    {#if childContent}
      <div data-testid="child-content">{childContent}</div>
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
</SettingsCard>

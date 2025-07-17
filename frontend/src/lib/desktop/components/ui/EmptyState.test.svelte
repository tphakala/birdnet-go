<script>
  import EmptyState from './EmptyState.svelte';

  let {
    showCustomIcon = false,
    showChildren = false,
    title = 'Test Empty State',
    description,
    action,
    ...rest
  } = $props();
</script>

<EmptyState {title} {description} {action} {...rest}>
  {#snippet icon()}
    {#if showCustomIcon}
      <svg data-testid="custom-icon" class="h-20 w-20 text-primary" fill="none" viewBox="0 0 24 24">
        <circle cx="12" cy="12" r="10" stroke="currentColor" stroke-width="2" />
      </svg>
    {/if}
  {/snippet}

  {#snippet children()}
    {#if showChildren}
      <div>
        <p>Custom child content</p>
        <p>Additional info</p>
      </div>
    {/if}
  {/snippet}
</EmptyState>

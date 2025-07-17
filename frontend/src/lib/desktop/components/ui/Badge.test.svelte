<script>
  import Badge from './Badge.svelte';
</script>

<Badge variant="info">
  {#snippet children()}
    <svg data-testid="badge-icon" class="w-3 h-3 mr-1" fill="currentColor" viewBox="0 0 20 20">
      <circle cx="10" cy="10" r="8" />
    </svg>
    <span>Status</span>
  {/snippet}
</Badge>

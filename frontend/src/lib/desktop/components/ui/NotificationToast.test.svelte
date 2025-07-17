<script>
  import NotificationToast from './NotificationToast.svelte';

  let { showChildren = false } = $props();
</script>

<NotificationToast message="Main message" type="info">
  {#snippet children()}
    {#if showChildren}
      <div class="text-sm mt-1">Additional details</div>
    {/if}
  {/snippet}
</NotificationToast>

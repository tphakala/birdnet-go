<!-- Test host: a component that holds a draft and registers the unsaved-changes guard for it -->
<script lang="ts">
  import { useUnsavedChangesGuard } from './useUnsavedChangesGuard.svelte';

  let { dirty = true }: { dirty?: boolean } = $props();

  useUnsavedChangesGuard(() => dirty);
</script>

<p>draft</p>

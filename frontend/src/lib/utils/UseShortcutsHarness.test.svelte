<script lang="ts">
  import { useShortcuts } from './useShortcuts.svelte';

  // Test host for useShortcuts: registers one scope for as long as it is mounted
  const SCOPE_ID = 'harness';

  useShortcuts({
    id: SCOPE_ID,
    label: () => SCOPE_ID,
    definitions: [
      {
        id: `${SCOPE_ID}.run`,
        group: 'general',
        binding: { key: 'h' },
        description: () => SCOPE_ID,
        run: () => {},
      },
    ],
  });
</script>

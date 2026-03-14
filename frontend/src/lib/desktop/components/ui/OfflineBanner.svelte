<script lang="ts">
  import { WifiOff } from '@lucide/svelte';
  import { slide } from 'svelte/transition';
  import { t } from '$lib/i18n';
  import { connectionState } from '$lib/stores/connectionState.svelte';
</script>

{#if !connectionState.isOnline}
  <div
    transition:slide={{ duration: 300 }}
    role="alert"
    aria-live="polite"
    class="flex items-center gap-3 rounded-lg bg-[var(--color-warning)] px-4 py-3 text-sm font-medium text-[var(--color-warning-content)]"
  >
    <WifiOff class="h-5 w-5 shrink-0" />
    <span>{t('connectivity.offline')}</span>
  </div>
{/if}

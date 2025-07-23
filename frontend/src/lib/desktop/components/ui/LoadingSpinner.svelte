<script lang="ts">
  import type { HTMLAttributes } from 'svelte/elements';
  import { t } from '$lib/i18n';

  type SpinnerSize = 'xs' | 'sm' | 'md' | 'lg' | 'xl';

  interface Props extends HTMLAttributes<HTMLDivElement> {
    size?: SpinnerSize;
    color?: string;
    label?: string;
  }

  let { size = 'lg', color = 'text-primary', label, ...rest }: Props = $props();

  // Reactive label with default
  let effectiveLabel = $derived(label ?? t('common.ui.loading'));
</script>

<div class="flex items-center justify-center" role="status" aria-label={effectiveLabel} {...rest}>
  <span class="loading loading-spinner loading-{size} {color}"></span>
  <span class="sr-only">{effectiveLabel}</span>
</div>

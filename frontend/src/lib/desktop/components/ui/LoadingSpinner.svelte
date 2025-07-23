<script lang="ts">
  import type { HTMLAttributes } from 'svelte/elements';
  import { t } from '$lib/i18n';

  type SpinnerSize = 'xs' | 'sm' | 'md' | 'lg' | 'xl';

  interface Props extends HTMLAttributes<HTMLDivElement> {
    size?: SpinnerSize;
    color?: string;
    label?: string;
  }

  let { size = 'lg', color = 'text-primary', label = t('common.ui.loading'), ...rest }: Props = $props();
</script>

<div class="flex items-center justify-center" role="status" aria-label={label} {...rest}>
  <span class="loading loading-spinner loading-{size} {color}"></span>
  <span class="sr-only">{label}</span>
</div>

<script lang="ts">
  import type { HTMLAttributes } from 'svelte/elements';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import { safeGet } from '$lib/utils/security';

  type SpinnerSize = 'xs' | 'sm' | 'md' | 'lg' | 'xl';

  interface Props extends HTMLAttributes<HTMLDivElement> {
    size?: SpinnerSize;
    color?: string;
    label?: string;
  }

  let { size = 'lg', color = 'text-[var(--color-primary)]', label, ...rest }: Props = $props();

  // Reactive label with default
  let effectiveLabel = $derived(label ?? t('common.ui.loading'));

  // Size classes using native Tailwind
  const sizeClasses: Record<SpinnerSize, string> = {
    xs: 'w-3 h-3 border',
    sm: 'w-4 h-4 border',
    md: 'w-6 h-6 border-2',
    lg: 'w-10 h-10 border-2',
    xl: 'w-14 h-14 border-[3px]',
  };

  // Base spinner classes using native Tailwind
  const baseSpinnerClasses =
    'inline-block aspect-square border-[var(--color-base-300)] border-t-current rounded-full animate-spin';
</script>

<div class="flex items-center justify-center" role="status" aria-label={effectiveLabel} {...rest}>
  <span class={cn(baseSpinnerClasses, safeGet(sizeClasses, size, ''), color)}></span>
  <span class="sr-only">{effectiveLabel}</span>
</div>

<!--
  Skeleton - one placeholder bar of a loading skeleton.

  Decorative only: the bar is always hidden from assistive technology, so pair a
  skeleton with exactly one visible SkeletonStatus label. Bars shrink with their
  container and stop pulsing under prefers-reduced-motion.

  Size and shape come from `className` (for example `h-4 w-28`, `rounded-full`,
  `inline-block`). `cn()` only joins class names, so an override wins through
  stylesheet order: Tailwind emits `.rounded` before `.rounded-full` and `.block`
  before `.inline-block`, so a caller's shape and display classes beat the base.

  @component
-->
<script lang="ts">
  import type { HTMLAttributes } from 'svelte/elements';
  import { cn } from '$lib/utils/cn';

  interface Props extends HTMLAttributes<HTMLElement> {
    /** Size and shape of the bar, merged after the base classes. */
    className?: string;
  }

  let { className = '', ...rest }: Props = $props();

  const BASE_CLASS =
    'block min-w-0 max-w-full rounded bg-[var(--color-base-300)] animate-pulse motion-reduce:animate-none';
</script>

<!-- A span, so a bar is valid inside text elements; the fixed attributes come after the
     spread so a caller cannot expose a bar to assistive technology -->
<span {...rest} data-skeleton aria-hidden="true" class={cn(BASE_CLASS, className)}></span>

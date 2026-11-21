<!--
  Kbd - key caps for a keyboard shortcut: one key or a combination.

  The caps are visual only (`<kbd aria-hidden="true">`), because glyphs such as
  arrows or a single letter read out as stray characters. Pass `label` with the
  spoken words ("Shift plus Left Arrow") where the shortcut itself is the content,
  as in a shortcut list; it renders as screen reader text. A badge inside a
  control whose name already says what it does passes no label.

  - `size="md"` (default): a raised cap on the surface tokens, for lists.
  - `size="sm"`: a compact outline cap that takes the surrounding text colour, for
    badges inside buttons and menu items. It follows the control's own colour, so
    a dimmed (aria-disabled) or filled button dims or recolours it too, and it fits
    inside a line of button text without changing the control's height.

  The border carries the cap shape, not a shadow, so it stays visible in forced
  colours mode.

  @component
-->
<script lang="ts">
  import type { HTMLAttributes } from 'svelte/elements';
  import { cn } from '$lib/utils/cn';
  import { safeGet } from '$lib/utils/security';

  /** Cap size: `md` for shortcut lists, `sm` for badges inside controls. */
  type KbdSize = 'sm' | 'md';

  interface Props extends HTMLAttributes<HTMLElement> {
    /** Display tokens, one per cap, in press order (for example `['⇧', '←']`). */
    keys: readonly string[];
    /** Spoken form of the whole shortcut, rendered as screen reader text. Omit for badges. */
    label?: string;
    size?: KbdSize;
    className?: string;
  }

  let { keys, label = '', size = 'md', className = '', ...rest }: Props = $props();

  const ROOT_CLASS = 'inline-flex shrink-0 items-center gap-1 align-middle whitespace-nowrap';

  const CAP_CLASS =
    'inline-flex items-center justify-center rounded border font-sans font-semibold leading-none';

  const CAP_SIZE_CLASSES: Record<KbdSize, string> = {
    sm: 'h-4 min-w-4 px-1 text-[0.6875rem] border-current/60',
    md: 'h-6 min-w-6 px-1.5 text-xs border-[var(--border-200)] border-b-2 border-b-[var(--border-300)] bg-[var(--surface-200)] text-[var(--color-base-content)]',
  };
</script>

<span {...rest} class={cn(ROOT_CLASS, className)}>
  {#each keys as key, index (index)}
    <kbd aria-hidden="true" class={cn(CAP_CLASS, safeGet(CAP_SIZE_CLASSES, size, ''))}>{key}</kbd>
  {/each}
  {#if label}
    <span class="sr-only">{label}</span>
  {/if}
</span>

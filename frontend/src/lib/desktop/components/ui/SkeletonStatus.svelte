<!--
  SkeletonStatus - the one visible loading label of a skeleton.

  A `role="status"` element holding only the label text. Place it where a line of
  loaded text appears (a card header's right side, a tile footer, an empty-state
  title), so the skeleton adds no height and nothing moves when the data arrives.
  It sets no font size: pass one in `className` or inherit it from the slot.

  @component
-->
<script lang="ts">
  import type { HTMLAttributes } from 'svelte/elements';
  import { cn } from '$lib/utils/cn';

  interface Props extends HTMLAttributes<HTMLElement> {
    /** Visible loading text, for example `t('common.loading')`. */
    label: string;
    className?: string;
    /** Merged like className, for callers that use the Svelte attribute name. */
    class?: string;
  }

  let { label, className = '', class: classProp = '', ...rest }: Props = $props();
</script>

<span {...rest} role="status" class={cn('text-muted', className, classProp)}>{label}</span>

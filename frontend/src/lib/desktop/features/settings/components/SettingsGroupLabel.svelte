<!--
  SettingsGroupLabel

  Purpose: The small uppercase label that names a subgroup of fields inside a
  settings card.

  Props:
  - as: Element to render: h2 to h6, div or span (default: h4). Pick the heading
    level that fits the page outline, or div/span when the label is not a heading
  - divided: Draws a hairline above the label, with spacing
  - class: Additional CSS classes. Spacing and layout only: the classes are joined,
    not merged, so do not pass a class that sets what the label already sets
  - children: The label text
  - Any other attribute (id, aria-*) goes to the element

  @component
-->
<script lang="ts" module>
  /**
   * Typography of a settings group label. Exported for elements the component
   * cannot render, such as a disclosure button in a SettingsGroup heading snippet.
   */
  export const SETTINGS_GROUP_LABEL_CLASSES =
    'text-xs font-semibold uppercase tracking-wider text-[var(--color-base-content)]/70';

  /** Elements a settings group label can render as. */
  export type SettingsGroupLabelElement = 'h2' | 'h3' | 'h4' | 'h5' | 'h6' | 'div' | 'span';
</script>

<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';
  import { cn } from '$lib/utils/cn';

  interface Props extends HTMLAttributes<HTMLElement> {
    as?: SettingsGroupLabelElement;
    divided?: boolean;
    class?: string;
    children?: Snippet;
  }

  let { as = 'h4', divided = false, class: className, children, ...rest }: Props = $props();

  const DIVIDED_CLASSES = 'mt-2 pt-4 border-t border-[var(--color-base-200)]';
</script>

<svelte:element
  this={as}
  {...rest}
  class={cn(SETTINGS_GROUP_LABEL_CLASSES, divided && DIVIDED_CLASSES, className)}
>
  {@render children?.()}
</svelte:element>

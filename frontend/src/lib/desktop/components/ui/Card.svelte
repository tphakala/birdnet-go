<script module lang="ts">
  type CardVariant = 'default' | 'panel';

  // One class map per variant so both share the same structure. `cn()` does not
  // resolve conflicting utilities, so a variant is a prop rather than a
  // className override.
  const VARIANT_CLASSES = {
    default: {
      root: 'rounded-lg overflow-hidden bg-[var(--color-base-100)] shadow-sm',
      header: 'px-6 py-4',
      title: 'text-xl font-semibold',
      description: 'text-sm opacity-70 mt-1 text-[var(--color-base-content)]',
      body: '',
      bodyWithHeader: 'px-6 pb-6',
      bodyAlone: 'p-6',
      footer: 'px-6 pb-6',
    },
    // The panel is a flex column whose body fills the remaining height, so
    // panels stretched to a shared grid row height keep their footers aligned.
    // No overflow clipping: sticky table headers and popovers inside a panel
    // must not be cut off at its edge.
    panel: {
      root: 'flex flex-col rounded-xl border border-[var(--border-100)] bg-[var(--surface-100)] shadow-sm',
      header: 'px-4 pt-4 pb-3',
      title: 'text-xs font-semibold uppercase tracking-wider text-muted wrap-anywhere',
      description: 'text-xs text-muted mt-1 wrap-anywhere',
      body: 'flex-1 min-h-0',
      bodyWithHeader: 'px-4 pb-4',
      bodyAlone: 'p-4',
      footer: 'px-4 pb-4',
    },
  } as const satisfies Record<CardVariant, Record<string, string>>;
</script>

<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { safeGet } from '$lib/utils/security';
  import type { Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';

  interface Props extends HTMLAttributes<HTMLDivElement> {
    title?: string;
    description?: string;
    /** `default` is the roomy content card; `panel` is the dense bordered panel used by data views. */
    variant?: CardVariant;
    padding?: boolean;
    className?: string;
    /** Classes for the body wrapper, for example `flex flex-col gap-3` to lay out the body itself. */
    bodyClassName?: string;
    header?: Snippet;
    /** Controls at the trailing end of the title row (buttons, toggles, a summary value). */
    headerActions?: Snippet;
    children?: Snippet;
    footer?: Snippet;
  }

  let {
    title = '',
    description,
    variant = 'default',
    padding = true,
    className = '',
    bodyClassName = '',
    header,
    headerActions,
    children,
    footer,
    ...rest
  }: Props = $props();

  let classes = $derived(safeGet(VARIANT_CLASSES, variant, VARIANT_CLASSES.default));
  let hasHeader = $derived(Boolean(title || description || header || headerActions));
</script>

<div class={cn(classes.root, className)} {...rest}>
  {#if hasHeader}
    <div class={classes.header}>
      <!-- The title side grows and may wrap. When the row is too narrow for
           both, the actions move below the title; they wrap their own controls
           only when even a line of their own is too narrow. -->
      <div class="flex flex-wrap items-center justify-between gap-x-3 gap-y-2">
        <div class="min-w-0 flex-auto">
          {#if header}
            {@render header()}
          {:else}
            {#if title}
              <h3 class={classes.title}>{title}</h3>
            {/if}
            {#if description}
              <p class={classes.description}>{description}</p>
            {/if}
          {/if}
        </div>
        {#if headerActions}
          <div class="flex flex-wrap items-center gap-2">
            {@render headerActions()}
          </div>
        {/if}
      </div>
    </div>
  {/if}

  <div
    class={cn(
      classes.body,
      padding && (hasHeader ? classes.bodyWithHeader : classes.bodyAlone),
      bodyClassName
    )}
  >
    {#if children}
      {@render children()}
    {/if}
  </div>

  {#if footer}
    <div class={classes.footer}>
      {@render footer()}
    </div>
  {/if}
</div>

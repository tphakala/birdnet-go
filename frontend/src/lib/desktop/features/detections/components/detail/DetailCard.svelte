<!--
  DetailCard - the one card surface of the detection detail page: a titled
  header and a body.

  - `title` is the small uppercase eyebrow heading; `subtitle` is short muted
    text after it. `header` adds content after the title, or replaces the whole
    header row when there is no title.
  - `padding` sets the body padding: `default` under a header, `snug` for a
    compact panel under a header, `even` for a card without a header.
  - `fill` lays the body out as a column, so a child can take the card's
    remaining height; `grow` lets the card itself take the remaining height of a
    column it sits in.
-->
<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    title?: string;
    /** Short text shown after the title, in muted colour. */
    subtitle?: string;
    /** Header content after the title; the whole header row when there is no title. */
    header?: Snippet;
    padding?: 'default' | 'snug' | 'even';
    /** Lays the body out as a column, so a child can grow to the card's height. */
    fill?: boolean;
    /** Takes the remaining height of a flex column the card sits in. */
    grow?: boolean;
    children?: Snippet;
  }

  let {
    title,
    subtitle,
    header,
    padding = 'default',
    fill = false,
    grow = false,
    children,
  }: Props = $props();
</script>

<section class="dd-card" class:dd-card-grow={grow}>
  {#if title || header}
    <header class="dd-card-header">
      {#if title}
        <h2 class="dd-card-title">{title}</h2>
      {/if}
      {#if subtitle}
        <span class="dd-card-subtitle">{subtitle}</span>
      {/if}
      {@render header?.()}
    </header>
  {/if}
  <div class="dd-card-body dd-card-body-{padding}" class:dd-card-body-fill={fill}>
    {@render children?.()}
  </div>
</section>

<style>
  .dd-card {
    display: flex;
    flex-direction: column;
    min-width: 0;
    background-color: var(--color-base-100);
    border: 1px solid var(--border-100);
    border-radius: var(--radius-box);
  }

  .dd-card-grow {
    flex: 1;
  }

  .dd-card-header {
    display: flex;
    align-items: baseline;
    gap: 0.625rem;
    min-width: 0;
    padding: 0.875rem 1.25rem 0;
  }

  /* The one eyebrow title style. Global inside the card, so a header snippet that lays out
     its own title (RecordingCard) uses it too. */
  .dd-card :global(.dd-card-title) {
    flex: 0 0 auto;
    font-size: 0.6875rem;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--text-subtle);
  }

  .dd-card-subtitle {
    font-size: 0.75rem;
    color: var(--text-subtle);
  }

  .dd-card-body {
    flex: 1;
    min-width: 0;
  }

  .dd-card-body-default {
    padding: 0.75rem 1.25rem 1.25rem;
  }

  .dd-card-body-snug {
    padding: 0.625rem 1.25rem 1rem;
  }

  .dd-card-body-even {
    padding: 1.25rem;
  }

  .dd-card-body-fill {
    display: flex;
    flex-direction: column;
  }
</style>

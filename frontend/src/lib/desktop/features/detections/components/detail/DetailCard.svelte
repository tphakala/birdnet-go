<!--
  DetailCard - card chrome shared by the panels of the detection detail page:
  a titled surface with an optional header aside and a body.
-->
<script lang="ts">
  import type { Snippet } from 'svelte';
  import { generateId } from '$lib/utils/uuid';

  interface Props {
    title: string;
    /** Short text shown after the title, in muted colour. */
    subtitle?: string;
    /** Content placed at the right end of the header. */
    aside?: Snippet;
    /** Removes the body padding, for content that runs edge to edge. */
    flush?: boolean;
    /** Lays the body out as a column, so a child can grow to the card's height. */
    fill?: boolean;
    className?: string;
    children?: Snippet;
  }

  let {
    title,
    subtitle,
    aside,
    flush = false,
    fill = false,
    className = '',
    children,
  }: Props = $props();

  const headingId = generateId('detail-card');
</script>

<section class="dd-card {className}" aria-labelledby={headingId}>
  <header class="dd-card-header">
    <h2 id={headingId} class="dd-card-title">{title}</h2>
    {#if subtitle}
      <span class="dd-card-subtitle">{subtitle}</span>
    {/if}
    {#if aside}
      <div class="dd-card-aside">{@render aside()}</div>
    {/if}
  </header>
  <div class="dd-card-body" class:dd-card-body-flush={flush} class:dd-card-body-fill={fill}>
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

  .dd-card-header {
    display: flex;
    align-items: baseline;
    gap: 0.625rem;
    padding: 0.875rem 1.25rem 0;
  }

  .dd-card-title {
    font-size: 0.6875rem;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
  }

  .dd-card-subtitle {
    font-size: 0.75rem;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
  }

  .dd-card-aside {
    margin-left: auto;
    font-size: 0.75rem;
  }

  .dd-card-body {
    flex: 1;
    min-width: 0;
    padding: 0.75rem 1.25rem 1.25rem;
  }

  .dd-card-body-flush {
    padding: 0.75rem 0 0;
  }

  .dd-card-body-fill {
    display: flex;
    flex-direction: column;
  }
</style>

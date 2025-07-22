<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';

  interface Props extends HTMLAttributes<HTMLDivElement> {
    title?: string;
    padding?: boolean;
    className?: string;
    header?: Snippet;
    children?: Snippet;
    footer?: Snippet;
  }

  let {
    title = '',
    padding = true,
    className = '',
    header,
    children,
    footer,
    ...rest
  }: Props = $props();
</script>

<div class={cn('card bg-base-100 shadow-sm', className)} {...rest}>
  {#if title || header}
    <header class="card-header">
      {#if header}
        {@render header()}
      {:else if title}
        <h2 class="card-title">{title}</h2>
      {/if}
    </header>
  {/if}

  <main class={cn('card-body', { 'p-0': !padding })}>
    {#if children}
      {@render children()}
    {/if}
  </main>

  {#if footer}
    <footer class="card-footer">
      {@render footer()}
    </footer>
  {/if}
</div>

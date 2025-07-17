<script lang="ts">
  import Card from './Card.svelte';
  import { cn } from '$lib/utils/cn.js';
  import type { Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';

  interface Props extends HTMLAttributes<HTMLDivElement> {
    title: string;
    description?: string;
    children: Snippet;
    disabled?: boolean;
    hasChanges?: boolean;
    className?: string;
    loading?: boolean;
    error?: string | null;
  }

  let {
    title,
    description,
    children,
    disabled = false,
    hasChanges = false,
    className = '',
    loading = false,
    error = null,
    ...rest
  }: Props = $props();

  let cardClasses = $derived(
    cn(className, {
      'ring-2 ring-primary': hasChanges,
      'opacity-50 pointer-events-none': disabled,
      'animate-pulse': loading,
    })
  );
</script>

<Card className={cardClasses} {...rest}>
  {#snippet header()}
    <div class="flex items-center justify-between">
      <div>
        <h3 class="text-lg font-semibold">{title}</h3>
        {#if description}
          <p class="text-sm text-base-content/70 mt-1">{description}</p>
        {/if}
      </div>
      {#if hasChanges}
        <div class="badge badge-primary badge-sm">Unsaved Changes</div>
      {/if}
    </div>
  {/snippet}

  {#if error}
    <div class="alert alert-error mb-4" role="alert">
      <svg
        xmlns="http://www.w3.org/2000/svg"
        class="stroke-current shrink-0 h-6 w-6"
        fill="none"
        viewBox="0 0 24 24"
      >
        <path
          stroke-linecap="round"
          stroke-linejoin="round"
          stroke-width="2"
          d="M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z"
        />
      </svg>
      <span>{error}</span>
    </div>
  {/if}

  {#if loading}
    <div class="space-y-4">
      <div class="skeleton h-4 w-full"></div>
      <div class="skeleton h-4 w-3/4"></div>
      <div class="skeleton h-4 w-1/2"></div>
    </div>
  {:else}
    <div class="space-y-4">
      {@render children()}
    </div>
  {/if}
</Card>

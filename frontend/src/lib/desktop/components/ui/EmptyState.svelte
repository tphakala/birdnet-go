<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';

  interface ActionConfig {
    label: string;
    onClick: () => void;
  }

  interface Props extends HTMLAttributes<HTMLDivElement> {
    icon?: Snippet;
    title?: string;
    description?: string;
    action?: ActionConfig | null;
    className?: string;
    children?: Snippet;
  }

  let {
    icon,
    title = '',
    description = '',
    action = null,
    className = '',
    children,
    ...rest
  }: Props = $props();
</script>

<div
  class={cn('flex flex-col items-center justify-center py-12 px-4 text-center', className)}
  {...rest}
>
  {#if icon}
    {@render icon()}
  {:else}
    <svg
      xmlns="http://www.w3.org/2000/svg"
      class="h-16 w-16 text-base-content/20"
      fill="none"
      viewBox="0 0 24 24"
      stroke="currentColor"
      aria-hidden="true"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="1"
        d="M20 13V6a2 2 0 00-2-2H6a2 2 0 00-2 2v7m16 0v5a2 2 0 01-2 2H6a2 2 0 01-2-2v-5m16 0h-2.586a1 1 0 00-.707.293l-2.414 2.414a1 1 0 01-.707.293h-3.172a1 1 0 01-.707-.293l-2.414-2.414A1 1 0 006.586 13H4"
      />
    </svg>
  {/if}

  {#if title}
    <h3 class="mt-4 text-lg font-semibold text-base-content">{title}</h3>
  {/if}

  {#if description}
    <p class="mt-2 text-sm text-base-content/70 max-w-md">{description}</p>
  {/if}

  {#if children}
    <div class="mt-4">
      {@render children()}
    </div>
  {/if}

  {#if action}
    <div class="mt-6">
      <button type="button" class="btn btn-primary" onclick={action.onClick}>
        {action.label}
      </button>
    </div>
  {/if}
</div>

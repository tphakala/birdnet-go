<!--
  Empty State Component

  Purpose: A reusable empty state component for settings pages that displays
  helpful guidance when no data is available or configured.

  Features:
  - Customizable icon, title, and description
  - Optional hint box with bullet points
  - Action buttons (primary and secondary)
  - Consistent styling across settings pages

  Props:
  - icon: Lucide icon component to display
  - title: Main heading text
  - description: Explanatory text
  - hints: Optional array of hint strings to display
  - primaryAction: Primary button configuration
  - secondaryAction: Secondary button configuration

  @component
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Component } from 'svelte';
  import { Lightbulb } from '@lucide/svelte';
  import type { IconProps } from '@lucide/svelte';

  interface ActionConfig {
    label: string;
    icon?: Component<IconProps>;
    onclick: () => void;
  }

  interface Props {
    icon: Component<IconProps>;
    title: string;
    description: string;
    hints?: string[];
    hintsTitle?: string;
    primaryAction?: ActionConfig;
    secondaryAction?: ActionConfig;
    class?: string;
  }

  let {
    icon: Icon,
    title,
    description,
    hints,
    hintsTitle,
    primaryAction,
    secondaryAction,
    class: className,
  }: Props = $props();
</script>

<div
  class={cn(
    'empty-state flex flex-col items-center justify-center py-12 px-6 text-center',
    className
  )}
>
  <!-- Icon -->
  <div class="mb-4 p-4 rounded-full bg-base-200/50">
    <Icon class="size-10 text-base-content/40" aria-hidden="true" />
  </div>

  <!-- Title -->
  <h3 class="text-lg font-semibold text-base-content mb-2">
    {title}
  </h3>

  <!-- Description -->
  <p class="text-base-content/70 max-w-md mb-6">
    {description}
  </p>

  <!-- Hints Box -->
  {#if hints && hints.length > 0}
    <div class="bg-base-200/50 rounded-lg p-4 mb-6 max-w-md text-left">
      <div class="flex items-center gap-2 mb-2">
        <Lightbulb class="size-4 text-info" aria-hidden="true" />
        <span class="text-sm font-medium text-base-content/80">
          {hintsTitle || 'Tips'}
        </span>
      </div>
      <ul class="space-y-1.5 text-sm text-base-content/70">
        {#each hints as hint, index (index)}
          <li class="flex items-start gap-2">
            <span class="text-base-content/40 mt-0.5">•</span>
            <span>{hint}</span>
          </li>
        {/each}
      </ul>
    </div>
  {/if}

  <!-- Actions -->
  {#if primaryAction || secondaryAction}
    <div class="flex flex-wrap items-center justify-center gap-3">
      {#if primaryAction}
        {@const PrimaryIcon = primaryAction.icon}
        <button type="button" class="btn btn-primary btn-sm gap-2" onclick={primaryAction.onclick}>
          {#if PrimaryIcon}
            <PrimaryIcon class="size-4" aria-hidden="true" />
          {/if}
          {primaryAction.label}
        </button>
      {/if}

      {#if secondaryAction}
        {@const SecondaryIcon = secondaryAction.icon}
        <button type="button" class="btn btn-ghost btn-sm gap-2" onclick={secondaryAction.onclick}>
          {#if SecondaryIcon}
            <SecondaryIcon class="size-4" aria-hidden="true" />
          {/if}
          {secondaryAction.label}
        </button>
      {/if}
    </div>
  {/if}
</div>

<style>
  .empty-state {
    min-height: 300px;
  }
</style>

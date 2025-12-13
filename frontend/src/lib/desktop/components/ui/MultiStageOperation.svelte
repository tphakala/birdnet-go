<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Stage } from './MultiStageOperation.types';
  import { Check, X, Clock, BookmarkMinus, Info } from '@lucide/svelte';

  interface Props {
    stages: Stage[];
    className?: string;
    showProgress?: boolean;
    variant?: 'default' | 'compact' | 'timeline';
    onStageClick?: (_stageId: string) => void;
  }

  let {
    stages = [],
    className = '',
    showProgress = true,
    variant = 'default',
    onStageClick,
  }: Props = $props();

  // Derived state
  let overallProgress = $derived.by(() => {
    const completed = stages.filter(s => s.status === 'completed').length;
    const total = stages.filter(s => s.status !== 'skipped').length;
    return total > 0 ? Math.round((completed / total) * 100) : 0;
  });

  function getStatusColor(status: Stage['status']): string {
    switch (status) {
      case 'completed':
        return 'text-success';
      case 'error':
        return 'text-error';
      case 'in_progress':
        return 'text-info';
      case 'skipped':
        return 'text-base-content/60';
      default:
        return 'text-base-content/30';
    }
  }

  function getProgressBarColor(status: Stage['status']): string {
    switch (status) {
      case 'completed':
        return 'bg-success';
      case 'error':
        return 'bg-error';
      case 'in_progress':
        return 'bg-info';
      default:
        return 'bg-base-300';
    }
  }

  function handleStageClick(stageId: string) {
    if (onStageClick) {
      onStageClick(stageId);
    }
  }
</script>

<div class={cn('multi-stage-operation', className)}>
  {#if showProgress && variant !== 'compact'}
    <div class="mb-6">
      <div class="flex justify-between text-sm mb-2">
        <span class="font-medium">Overall Progress</span>
        <span class="text-base-content/70">{overallProgress}%</span>
      </div>
      <div class="w-full bg-base-300 rounded-full h-2 overflow-hidden">
        <div
          class="bg-primary h-full transition-all duration-300 ease-out"
          style:width="{overallProgress}%"
        ></div>
      </div>
    </div>
  {/if}

  {#if variant === 'timeline'}
    <div class="relative">
      {#each stages as stage, index (stage.id)}
        <div class="flex gap-4 pb-8 last:pb-0">
          <!-- Timeline line -->
          {#if index < stages.length - 1}
            <div class="absolute left-6 top-12 bottom-0 w-0.5 bg-base-300"></div>
          {/if}

          <!-- Icon -->
          <div
            class={cn(
              'relative z-10 shrink-0 w-12 h-12 rounded-full flex items-center justify-center transition-colors',
              stage.status === 'completed'
                ? 'bg-success/20'
                : stage.status === 'error'
                  ? 'bg-error/20'
                  : stage.status === 'in_progress'
                    ? 'bg-info/20'
                    : 'bg-base-200'
            )}
          >
            {#if stage.status === 'completed'}
              <Check class={cn('size-6', getStatusColor(stage.status))} />
            {:else if stage.status === 'error'}
              <X class={cn('size-6', getStatusColor(stage.status))} />
            {:else if stage.status === 'in_progress'}
              <Clock class={cn('size-6', getStatusColor(stage.status))} />
            {:else if stage.status === 'skipped'}
              <BookmarkMinus class={cn('size-6', getStatusColor(stage.status))} />
            {:else}
              <Info class={cn('size-6', getStatusColor(stage.status))} />
            {/if}
          </div>

          <!-- Content -->
          <div class="flex-1 pt-1">
            <button
              type="button"
              onclick={() => handleStageClick(stage.id)}
              disabled={!onStageClick}
              class={cn(
                'text-left w-full',
                onStageClick && 'hover:bg-base-200 rounded-lg p-2 -m-2 transition-colors'
              )}
            >
              <h3 class={cn('font-medium', stage.status === 'in_progress' ? 'text-primary' : '')}>
                {stage.title}
              </h3>

              {#if stage.description}
                <p class="text-sm text-base-content/70 mt-1">{stage.description}</p>
              {/if}

              {#if stage.message}
                <p
                  class={cn(
                    'text-sm mt-2',
                    stage.status === 'error' ? 'text-error' : 'text-base-content/60'
                  )}
                >
                  {stage.message}
                </p>
              {/if}
              {#if stage.error && stage.error !== stage.message}
                <p class="text-sm mt-1 text-error">
                  <strong>Error:</strong>
                  {stage.error}
                </p>
              {/if}

              {#if stage.status === 'in_progress' && stage.progress !== undefined}
                <div class="mt-2">
                  <div class="w-full bg-base-300 rounded-full h-1.5 overflow-hidden">
                    <div
                      class="bg-info h-full transition-all duration-300 ease-out"
                      style:width="{stage.progress}%"
                    ></div>
                  </div>
                </div>
              {/if}
            </button>
          </div>
        </div>
      {/each}
    </div>
  {:else if variant === 'compact'}
    <div class="space-y-2">
      {#each stages as stage (stage.id)}
        <button
          type="button"
          onclick={() => handleStageClick(stage.id)}
          disabled={!onStageClick}
          class={cn(
            'flex items-center gap-3 w-full p-3 rounded-lg transition-colors',
            stage.status === 'in_progress' ? 'bg-base-200' : '',
            onStageClick && 'hover:bg-base-200'
          )}
        >
          {#if stage.status === 'completed'}
            <Check class={cn('size-5 shrink-0', getStatusColor(stage.status))} />
          {:else if stage.status === 'error'}
            <X class={cn('size-5 shrink-0', getStatusColor(stage.status))} />
          {:else if stage.status === 'in_progress'}
            <Clock class={cn('size-5 shrink-0', getStatusColor(stage.status))} />
          {:else if stage.status === 'skipped'}
            <BookmarkMinus class={cn('size-5 shrink-0', getStatusColor(stage.status))} />
          {:else}
            <Info class={cn('size-5 shrink-0', getStatusColor(stage.status))} />
          {/if}

          <div class="flex-1 text-left">
            <div class="font-medium text-sm">{stage.title}</div>
            {#if stage.message}
              <div
                class={cn(
                  'text-xs mt-0.5',
                  stage.status === 'error' ? 'text-error' : 'text-base-content/60'
                )}
              >
                {stage.message}
              </div>
            {/if}
            {#if stage.error && stage.error !== stage.message}
              <div class="text-xs mt-0.5 text-error">
                {stage.error}
              </div>
            {/if}
          </div>

          {#if stage.status === 'in_progress' && stage.progress !== undefined}
            <div class="text-xs text-base-content/60">{stage.progress}%</div>
          {/if}
        </button>
      {/each}
    </div>
  {:else}
    <!-- Default variant -->
    <div class="space-y-4">
      {#each stages as stage, index (stage.id)}
        <div class={cn('card', stage.status === 'in_progress' ? 'ring-2 ring-primary' : '')}>
          <button
            type="button"
            onclick={() => handleStageClick(stage.id)}
            disabled={!onStageClick}
            class={cn(
              'card-body p-4 text-left w-full',
              onStageClick && 'hover:bg-base-200 transition-colors'
            )}
          >
            <div class="flex items-start gap-4">
              <div
                class={cn(
                  'shrink-0 w-10 h-10 rounded-full flex items-center justify-center',
                  stage.status === 'completed'
                    ? 'bg-success/20'
                    : stage.status === 'error'
                      ? 'bg-error/20'
                      : stage.status === 'in_progress'
                        ? 'bg-info/20'
                        : 'bg-base-200'
                )}
              >
                {#if stage.status === 'completed'}
                  <Check class={cn('size-5', getStatusColor(stage.status))} />
                {:else if stage.status === 'error'}
                  <X class={cn('size-5', getStatusColor(stage.status))} />
                {:else if stage.status === 'in_progress'}
                  <Clock class={cn('size-5', getStatusColor(stage.status))} />
                {:else if stage.status === 'skipped'}
                  <BookmarkMinus class={cn('size-5', getStatusColor(stage.status))} />
                {:else}
                  <Info class={cn('size-5', getStatusColor(stage.status))} />
                {/if}
              </div>

              <div class="flex-1">
                <div class="flex items-center justify-between">
                  <h3 class="font-medium">{stage.title}</h3>
                  <span class="text-xs text-base-content/60">
                    Step {index + 1} of {stages.length}
                  </span>
                </div>

                {#if stage.description}
                  <p class="text-sm text-base-content/70 mt-1">{stage.description}</p>
                {/if}

                {#if stage.error}
                  <div class="alert alert-error mt-3">
                    <Info class="size-4" />
                    <span class="text-sm">{stage.error}</span>
                  </div>
                {:else if stage.message}
                  <p class="text-sm text-base-content/60 mt-2">{stage.message}</p>
                {/if}

                {#if stage.status === 'in_progress' && stage.progress !== undefined}
                  <div class="mt-3">
                    <div class="flex justify-between text-xs mb-1">
                      <span class="text-base-content/60">Progress</span>
                      <span class="text-base-content/70">{stage.progress}%</span>
                    </div>
                    <div class="w-full bg-base-300 rounded-full h-2 overflow-hidden">
                      <div
                        class={cn(
                          'h-full transition-all duration-300 ease-out',
                          getProgressBarColor(stage.status)
                        )}
                        style:width="{stage.progress}%"
                      ></div>
                    </div>
                  </div>
                {/if}
              </div>
            </div>
          </button>
        </div>
      {/each}
    </div>
  {/if}
</div>

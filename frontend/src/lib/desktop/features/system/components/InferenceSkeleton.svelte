<!--
  InferenceSkeleton - loading placeholder for the AI Models & Inference page.

  Mirrors the loaded layout so the page does not jump when the snapshot arrives.
  One polite status region carries the screen reader label; the bars are hidden
  from assistive technology and stop pulsing under prefers-reduced-motion.

  @component
-->
<script lang="ts">
  import { t } from '$lib/i18n';

  const CARD_CLASS =
    'bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm';
  const BAR_CLASS = 'rounded bg-[var(--color-base-300)] animate-pulse motion-reduce:animate-none';
  /** Value widths for the hardware rows, varied so the bars do not read as a table. */
  const HARDWARE_VALUE_WIDTHS = ['w-56', 'w-48', 'w-40', 'w-12', 'w-16', 'w-32'];
</script>

{#snippet bar(sizeClass: string)}
  <div class="{BAR_CLASS} {sizeClass}"></div>
{/snippet}

{#snippet bars(count: number, sizeClass: string)}
  {#each { length: count } as _, i (i)}
    {@render bar(sizeClass)}
  {/each}
{/snippet}

{#snippet pillRow()}
  <div class="flex items-center gap-3">
    {@render bar('h-4 w-32')}
    {@render bar('h-5 w-20 rounded-full')}
  </div>
{/snippet}

<div class="space-y-4" role="status" aria-live="polite" data-testid="inference-skeleton">
  <span class="sr-only">{t('system.inference.loading')}</span>

  <div aria-hidden="true" class="space-y-4">
    <div class="grid grid-cols-1 md:grid-cols-2 gap-3 items-start">
      <!-- Hardware -->
      <div class={CARD_CLASS}>
        {@render bar('h-3 w-24 mb-3')}
        <div class="grid grid-cols-[auto_1fr] items-center gap-x-3 gap-y-2.5">
          {#each HARDWARE_VALUE_WIDTHS as width (width)}
            {@render bar('h-4 w-28')}
            {@render bar(`h-4 ${width}`)}
          {/each}
        </div>
      </div>

      <!-- Inference backends -->
      <div class={CARD_CLASS}>
        {@render bar('h-3 w-32 mb-3')}
        <div class="space-y-2.5">
          {#each { length: 3 } as _, i (i)}
            {@render pillRow()}
          {/each}
        </div>
        <div class="mt-3 pt-3 border-t border-[var(--border-100)]">
          {@render pillRow()}
        </div>
      </div>
    </div>

    <!-- Models -->
    <div>
      {@render bar('h-3 w-20 mb-3')}
      <div class="{CARD_CLASS} flex flex-col gap-3">
        <!-- Header: icon, name, backend badge, activity indicator -->
        <div class="flex items-center gap-2">
          {@render bar('h-4 w-4')}
          {@render bar('h-4 w-40')}
          {@render bar('h-5 w-16 rounded-full')}
          {@render bar('h-4 w-16 ml-auto')}
        </div>

        <!-- Spec and stats lines -->
        <div class="flex flex-wrap gap-x-4 gap-y-1">{@render bars(3, 'h-3 w-24')}</div>
        <div class="flex flex-wrap gap-x-4 gap-y-1">{@render bars(6, 'h-3 w-24')}</div>

        <!-- Latency chart -->
        <div class="space-y-1">
          {@render bar('h-3 w-28')}
          {@render bar('h-10 w-full')}
        </div>

        <!-- Last heard: two columns of rows -->
        <div class="space-y-2">
          {@render bar('h-3 w-24')}
          <div class="grid grid-cols-1 lg:grid-cols-2 gap-x-6 gap-y-2">
            <div class="space-y-2">{@render bars(4, 'h-3 w-full')}</div>
            <div class="space-y-2">{@render bars(4, 'h-3 w-full')}</div>
          </div>
        </div>

        <!-- Approx RAM and sources -->
        {@render bar('h-3 w-40')}
        <div class="flex gap-1.5">{@render bars(2, 'h-5 w-28 rounded-full')}</div>
      </div>
    </div>
  </div>
</div>

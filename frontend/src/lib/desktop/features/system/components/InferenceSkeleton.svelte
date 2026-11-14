<!--
  InferenceSkeleton - loading placeholder for the AI Models & Inference page.

  Mirrors the loaded layout so the page does not jump when the snapshot arrives.
  Built from the shared Skeleton bars and one SkeletonStatus label, shown in the
  model card header where the loaded card shows its activity state, so the
  skeleton adds no height of its own.

  Sized for the common host. Three loaded parts depend on the host or the
  configuration, and the skeleton includes each of them:
  - the hardware card's Advanced row, which shows whenever the host reports
    capability tokens (every build with TensorFlow Lite does);
  - the Privacy Guard section, which the server reports whenever the privacy
    filter is enabled (the default);
  - the ONNX backend row with its Initialized pill and version, which wraps to
    a second line in the half-width card of a narrow window.
  A host without them sees the page grow shorter when the data arrives.

  @component
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import Skeleton from '$lib/desktop/components/ui/Skeleton.svelte';
  import SkeletonStatus from '$lib/desktop/components/ui/SkeletonStatus.svelte';

  const CARD_CLASS =
    'bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm';
  /** Value widths for the hardware rows, varied so the bars do not read as a table. */
  const HARDWARE_VALUE_WIDTHS = ['w-56', 'w-48', 'w-40', 'w-12', 'w-16', 'w-32'];
  const DIVIDER_CLASS = 'mt-3 pt-3 border-t border-[var(--border-100)]';
</script>

{#snippet bar(sizeClass: string)}
  <Skeleton className={sizeClass} />
{/snippet}

{#snippet bars(count: number, sizeClass: string)}
  {#each { length: count } as _, i (i)}
    {@render bar(sizeClass)}
  {/each}
{/snippet}

{#snippet pillRow()}
  <div class="flex items-center gap-3 flex-wrap">
    {@render bar('h-4 w-32')}
    {@render bar('h-5 w-20 rounded-full')}
  </div>
{/snippet}

<!-- A section title is one text-xs line (16 px); the bar is shorter than the line -->
{#snippet title(widthClass: string)}
  <div class="mb-3 h-4">{@render bar(`h-3 ${widthClass}`)}</div>
{/snippet}

<!-- A text-xs line of a card (16 px) holding bars that are 12 px high -->
{#snippet statLine(count: number)}
  <div class="flex flex-wrap items-center gap-x-4 gap-y-1 min-h-4">
    {@render bars(count, 'h-3 w-24')}
  </div>
{/snippet}

<div class="space-y-4" data-testid="inference-skeleton">
  <div class="grid grid-cols-1 md:grid-cols-2 gap-3 items-start">
    <!-- Hardware -->
    <div class={CARD_CLASS}>
      {@render title('w-24')}
      <!-- Rows are one text-sm line (20 px), like the loaded list -->
      <div class="grid grid-cols-[auto_minmax(0,1fr)] auto-rows-5 items-center gap-x-3 gap-y-2.5">
        {#each HARDWARE_VALUE_WIDTHS as width (width)}
          {@render bar('h-4 w-28')}
          {@render bar(`h-4 ${width}`)}
        {/each}
      </div>
      <!-- Advanced disclosure -->
      <div class={DIVIDER_CLASS}>
        <div class="flex h-5 items-center">{@render bar('h-4 w-20')}</div>
      </div>
    </div>

    <!-- Inference backends -->
    <div class={CARD_CLASS}>
      {@render title('w-32')}
      <div class="space-y-2.5">
        {@render pillRow()}
        <!-- ONNX: Available, Initialized and the version, which wraps in a narrow card -->
        <div class="flex items-center gap-3 flex-wrap">
          {@render bar('h-4 w-32')}
          {@render bar('h-5 w-20 rounded-full')}
          {@render bar('h-5 w-24 rounded-full')}
          {@render bar('h-4 w-28')}
        </div>
        {@render pillRow()}
      </div>
      <div class={DIVIDER_CLASS}>
        {@render pillRow()}
      </div>
    </div>
  </div>

  <!-- Privacy Guard (Silero VAD) -->
  <div>
    {@render title('w-24')}
    <div class="{CARD_CLASS} flex flex-col gap-3">
      <div class="flex items-center gap-2 flex-wrap">
        {@render bar('h-4 w-4')}
        {@render bar('h-4 w-24')}
        {@render bar('h-5 w-14 rounded-full')}
        {@render bar('h-5 w-10 rounded-full')}
      </div>
      <!-- The description is one text line of about 1030 px that wraps in a card narrower
           than that: two bars that wrap like its words do -->
      <p class="text-xs leading-snug">
        {@render bar('inline-block align-middle h-3 w-[40rem]')}
        {@render bar('inline-block align-middle h-3 w-[24rem]')}
      </p>
      {@render statLine(2)}
      {@render statLine(4)}
      <div>
        <div class="mb-1 h-4">{@render bar('h-3 w-24')}</div>
        <div class="h-4">{@render bar('h-3 w-40')}</div>
      </div>
    </div>
  </div>

  <!-- Models -->
  <div>
    {@render title('w-20')}
    <div class="{CARD_CLASS} flex flex-col gap-3">
      <!-- Header: icon, name, backend badge, then the loading label where the loaded
             card shows its activity state -->
      <div class="flex items-center gap-2 flex-wrap">
        {@render bar('h-4 w-4')}
        {@render bar('h-4 w-40')}
        {@render bar('h-5 w-16 rounded-full')}
        <SkeletonStatus label={t('system.inference.loading')} className="ml-auto text-xs" />
      </div>

      <!-- Spec and stats lines -->
      <div class="flex flex-wrap gap-x-4 gap-y-1">{@render bars(3, 'h-3 w-24')}</div>
      <div class="flex flex-wrap gap-x-4 gap-y-1">{@render bars(6, 'h-3 w-24')}</div>

      <!-- Latency chart -->
      <div class="space-y-1">
        {@render bar('h-3 w-28')}
        {@render bar('h-10 w-full')}
      </div>

      <!-- Last heard: title, hint, then two columns of rows. The rows keep the loaded
           feed's minimum height so the cards below do not jump when it fills in. -->
      <div class="space-y-2">
        {@render bar('h-3 w-24')}
        {@render bar('h-2.5 w-3/4')}
        <div class="grid min-h-[12rem] grid-cols-1 content-start lg:grid-cols-2 gap-x-6 gap-y-2">
          <div class="space-y-2">{@render bars(4, 'h-3 w-full')}</div>
          <div class="space-y-2">{@render bars(4, 'h-3 w-full')}</div>
        </div>
      </div>

      <!-- Approx RAM, then the sources label and chips -->
      {@render bar('h-3 w-40')}
      <div class="space-y-1">
        {@render bar('h-3 w-16')}
        <div class="flex gap-1.5">{@render bars(2, 'h-5 w-28 rounded-full')}</div>
      </div>
    </div>
  </div>
</div>

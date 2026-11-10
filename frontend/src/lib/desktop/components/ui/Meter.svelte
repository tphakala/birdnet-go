<!--
  Meter Component

  Purpose: Show a static measurement (disk usage, CPU share, table size) as a thin bar with
  role="meter". For the progress of a running task use ProgressBar instead.

  Features:
  - Fill colors come only from the --viz-* tokens, picked by `tone`; every fill reaches 3:1
    against the card and against the track in both themes (see color-contrast.test.ts)
  - `thresholds` switch the tone as the value rises (for example 70 and 90 percent)
  - `segments` draw stacked parts (RAM used plus buffers and cache), separated by a 2 px gap;
    a `hatched` part reads as secondary without needing a second color
  - The track is a decorative groove: print the value as text beside the meter
  - Width changes animate, except under prefers-reduced-motion

  @component
-->
<script lang="ts" module>
  /** Fill tone: a chart series slot or a status. */
  export type MeterTone =
    | 'series-1'
    | 'series-2'
    | 'series-3'
    | 'series-4'
    | 'series-5'
    | 'series-6'
    | 'series-7'
    | 'series-8'
    | 'success'
    | 'warning'
    | 'error';

  export type MeterSize = 'xs' | 'sm';

  /** From `value` upwards (in the units of `value` and `max`), the fill takes `tone`. */
  export interface MeterThreshold {
    value: number;
    tone: MeterTone;
  }

  /** One stacked part, in the units of `value` and `max`. */
  export interface MeterSegment {
    value: number;
    tone: MeterTone;
    pattern?: 'solid' | 'hatched';
  }
</script>

<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { HTMLAttributes } from 'svelte/elements';

  interface Props extends HTMLAttributes<HTMLDivElement> {
    /** Measured value, from 0 to `max`; out-of-range values are clamped, non-finite ones read as 0 */
    value: number;
    max?: number;
    /** Fill tone when no threshold is reached */
    tone?: MeterTone;
    /** Tone changes by value; the highest threshold at or below the value wins */
    thresholds?: MeterThreshold[];
    /** Stacked parts drawn instead of a single fill; `value` stays the reported value */
    segments?: MeterSegment[];
    /** Bar height: xs (0.375rem) for table rows, sm (0.5rem) for cards */
    size?: MeterSize;
    /** Accessible name */
    label: string;
    /** Human-readable value for assistive technology, such as "3.2 GB of 8 GB" */
    valueText?: string;
    className?: string;
  }

  let {
    value,
    max = 100,
    tone = 'series-1',
    thresholds = [],
    segments,
    size = 'sm',
    label,
    valueText,
    className = '',
    ...rest
  }: Props = $props();

  const finite = (n: number): number => (Number.isFinite(n) ? n : 0);
  const clamp = (n: number, hi: number): number => Math.min(Math.max(finite(n), 0), hi);
  const percent = (n: number): string => (range > 0 ? `${(n / range) * 100}%` : '0%');

  let range = $derived(Math.max(finite(max), 0));
  let current = $derived(clamp(value, range));

  let currentTone = $derived(
    thresholds
      .filter(threshold => current >= threshold.value)
      .reduce<MeterThreshold | null>(
        (best, threshold) => (best === null || threshold.value > best.value ? threshold : best),
        null
      )?.tone ?? tone
  );

  // Parts in order, each cut so the stack never runs past max; empty parts draw nothing, so no
  // stray gap appears.
  let fills = $derived.by(() => {
    if (!segments) {
      return [{ tone: currentTone, pattern: 'solid', basis: percent(current) }];
    }
    let used = 0;
    const parts: { tone: MeterTone; pattern: string; basis: string }[] = [];
    for (const segment of segments) {
      const part = clamp(segment.value, range - used);
      used += part;
      if (part > 0) {
        parts.push({
          tone: segment.tone,
          pattern: segment.pattern ?? 'solid',
          basis: percent(part),
        });
      }
    }
    return parts;
  });
</script>

<div
  role="meter"
  aria-label={label}
  aria-valuenow={current}
  aria-valuemin={0}
  aria-valuemax={range}
  aria-valuetext={valueText}
  class={cn('meter', size === 'xs' ? 'h-1.5' : 'h-2', className)}
  {...rest}
>
  {#each fills as fill, i (i)}
    <div
      class="meter-fill"
      data-tone={fill.tone}
      data-pattern={fill.pattern}
      style:flex-basis={fill.basis}
    ></div>
  {/each}
</div>

<style>
  /* Component CSS rather than utilities: the tone-to-token map, the hatch pattern, the segment
     gap that flex-shrink keeps inside the track, and the forced-colors fallback have no
     utility-class equivalent that stays readable. */

  /* Full width by default, in Tailwind's components layer so a width utility passed in
     className (w-12 in a table cell) still wins; the unlayered rules below would beat it. */
  @layer components {
    .meter {
      width: 100%;
    }
  }

  .meter {
    display: flex;
    gap: 2px;
    overflow: hidden;
    border-radius: 9999px;
    background-color: var(--viz-track);
  }

  .meter-fill {
    flex-grow: 0;
    flex-shrink: 1;
    min-width: 0;
    background: var(--meter-fill);
    transition: flex-basis 400ms cubic-bezier(0.25, 1, 0.5, 1);
  }

  /* Only the data end is rounded; joints between parts stay square on either side of the gap */
  .meter-fill:last-child {
    border-start-end-radius: 9999px;
    border-end-end-radius: 9999px;
  }

  .meter-fill[data-pattern='hatched'] {
    background: repeating-linear-gradient(135deg, var(--meter-fill) 0 2px, transparent 2px 4px);
  }

  .meter-fill[data-tone='series-1'] {
    --meter-fill: var(--viz-1);
  }

  .meter-fill[data-tone='series-2'] {
    --meter-fill: var(--viz-2);
  }

  .meter-fill[data-tone='series-3'] {
    --meter-fill: var(--viz-3);
  }

  .meter-fill[data-tone='series-4'] {
    --meter-fill: var(--viz-4);
  }

  .meter-fill[data-tone='series-5'] {
    --meter-fill: var(--viz-5);
  }

  .meter-fill[data-tone='series-6'] {
    --meter-fill: var(--viz-6);
  }

  .meter-fill[data-tone='series-7'] {
    --meter-fill: var(--viz-7);
  }

  .meter-fill[data-tone='series-8'] {
    --meter-fill: var(--viz-8);
  }

  .meter-fill[data-tone='success'] {
    --meter-fill: var(--viz-success);
  }

  .meter-fill[data-tone='warning'] {
    --meter-fill: var(--viz-warning);
  }

  .meter-fill[data-tone='error'] {
    --meter-fill: var(--viz-error);
  }

  @media (prefers-reduced-motion: reduce) {
    .meter-fill {
      transition: none;
    }
  }

  /* Forced colors drop backgrounds; outline the track and paint fills in a system color */
  @media (forced-colors: active) {
    .meter {
      border: 1px solid CanvasText;
    }

    .meter-fill {
      forced-color-adjust: none;
      background: Highlight;
    }

    .meter-fill[data-pattern='hatched'] {
      background: repeating-linear-gradient(135deg, Highlight 0 2px, transparent 2px 4px);
    }
  }
</style>

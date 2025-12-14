<!--
  FilterIcon Component

  Purpose: Renders audio filter type icons based on filter type.
  This component provides a type-safe way to display filter icons.

  Props:
  - filter: The filter type (LowPass, HighPass, BandReject)
  - className: Optional CSS classes for sizing/styling

  Note: Uses {@html} for SVG rendering - safe because icons are static build-time
  assets, not user-generated content.

  @component
-->
<script lang="ts">
  import type { HTMLAttributes } from 'svelte/elements';
  import { cn } from '$lib/utils/cn.js';

  // Import all filter icons as raw SVG strings
  import LowPassIcon from '$lib/assets/icons/filters/lowpass.svg?raw';
  import HighPassIcon from '$lib/assets/icons/filters/highpass.svg?raw';
  import BandRejectIcon from '$lib/assets/icons/filters/bandreject.svg?raw';

  // Filter type definition
  export type FilterType = 'LowPass' | 'HighPass' | 'BandReject';

  interface Props extends HTMLAttributes<HTMLElement> {
    filter: FilterType;
    className?: string;
  }

  let { filter, className = '', ...rest }: Props = $props();

  // Map filter types to their SVG content
  const filterIcons: Record<FilterType, string> = {
    LowPass: LowPassIcon,
    HighPass: HighPassIcon,
    BandReject: BandRejectIcon,
  };

  // Runtime type guard to satisfy static analysis (object injection sink warning)
  const isFilterType = (v: unknown): v is FilterType => typeof v === 'string' && v in filterIcons;

  // Get the icon for the current filter type with runtime validation
  let iconSvg = $derived(isFilterType(filter) ? filterIcons[filter] : LowPassIcon);
</script>

<span
  class={cn('size-5 shrink-0 [&>svg]:size-full [&>svg]:block', className)}
  aria-hidden="true"
  {...rest}
>
  {@html iconSvg}
</span>

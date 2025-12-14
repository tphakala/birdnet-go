<!--
  FilterIcon Component

  Purpose: Renders audio filter type icons based on filter type.
  This component provides a type-safe way to display filter icons
  without using raw HTML injection.

  Props:
  - filter: The filter type (LowPass, HighPass, BandReject)
  - className: Optional CSS classes for sizing/styling

  @component
-->
<script lang="ts">
  // Import all filter icons as raw SVG strings
  import LowPassIcon from '$lib/assets/icons/filters/lowpass.svg?raw';
  import HighPassIcon from '$lib/assets/icons/filters/highpass.svg?raw';
  import BandRejectIcon from '$lib/assets/icons/filters/bandreject.svg?raw';

  // Filter type definition
  export type FilterType = 'LowPass' | 'HighPass' | 'BandReject';

  interface Props {
    filter: FilterType;
    className?: string;
  }

  let { filter, className = 'size-5' }: Props = $props();

  // Map filter types to their SVG content
  const filterIcons: Record<FilterType, string> = {
    LowPass: LowPassIcon,
    HighPass: HighPassIcon,
    BandReject: BandRejectIcon,
  };

  // Get the icon for the current filter type
  let iconSvg = $derived(filterIcons[filter] || LowPassIcon);
</script>

<!--
  Note: We use {@html} here because the SVG icons are static assets
  imported at build time, not user-generated content. The icons are
  trusted and sanitized by the build process.
-->
<span class="{className} shrink-0 [&>svg]:size-full [&>svg]:block">
  {@html iconSvg}
</span>

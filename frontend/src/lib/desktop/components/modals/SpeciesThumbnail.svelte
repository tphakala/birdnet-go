<!--
  SpeciesThumbnail.svelte
  
  Reusable component for displaying species thumbnail images.
  Extracted from ReviewModal to reduce duplication and improve maintainability.
  
  Features:
  - Responsive image sizing
  - Error handling with fallback
  - Lazy loading
  - Proper aspect ratio and styling
  
  Props:
  - scientificName: string - Scientific name for image URL
  - commonName: string - Common name for alt text
  - size?: 'sm' | 'md' | 'lg' - Thumbnail size variant
  - className?: string - Additional CSS classes
-->
<script lang="ts">
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils.js';

  interface Props {
    scientificName: string;
    commonName: string;
    size?: 'sm' | 'md' | 'lg';
    className?: string;
  }

  let { scientificName, commonName, size = 'lg', className = '' }: Props = $props();

  // Size classes for responsive thumbnails
  const sizeClasses = $derived(() => {
    switch (size) {
      case 'sm':
        return 'w-20 h-15';
      case 'md':
        return 'w-24 h-18';
      case 'lg':
        return 'w-32 h-24';
      default:
        return 'w-32 h-24';
    }
  });
</script>

<div
  class={`${sizeClasses()} relative overflow-hidden rounded-lg bg-base-100 shadow-md flex-shrink-0 ${className}`}
>
  <img
    src="/api/v2/media/species-image?name={encodeURIComponent(scientificName)}"
    alt={commonName}
    class="w-full h-full object-cover"
    onerror={handleBirdImageError}
    loading="lazy"
  />
</div>

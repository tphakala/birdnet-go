<!--
  BirdThumbnailPopup Component
  
  A hover-triggered popup that displays a larger bird thumbnail with species information.
  Designed for use in data tables where space is limited but users need to see more detail.
  
  Features:
  - Shows larger image on hover with smooth transitions
  - Displays species common and scientific names
  - Smart positioning that adapts based on available space:
    - Positions below thumbnail when there's space
    - Positions above thumbnail when near bottom of viewport
    - Adjusts horizontally to stay within viewport bounds
  - Uses svelte-portal to escape overflow containers
  - Handles image loading states and errors gracefully
  - Fully accessible with proper ARIA attributes
  - Responsive design that works on mobile (tap to show)
  
  Props:
  - thumbnailUrl: URL for the bird thumbnail image
  - commonName: Common name of the bird species
  - scientificName: Scientific name of the bird species  
  - detectionUrl: URL to link to when thumbnail is clicked
  - className: Additional CSS classes for the trigger thumbnail
-->

<script lang="ts">
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils.js';
  import Portal from 'svelte-portal';
  import { dataIcons } from '$lib/utils/icons';

  interface Props {
    thumbnailUrl: string;
    commonName: string;
    scientificName: string;
    detectionUrl: string;
    className?: string;
  }

  let { thumbnailUrl, commonName, scientificName, detectionUrl, className = '' }: Props = $props();

  // State for popup visibility and positioning
  let showPopup = $state(false);
  let popupX = $state(0);
  let popupY = $state(0);
  let popupPosition = $state<'above' | 'below'>('below');
  let triggerElement: HTMLElement | undefined = $state();
  let popupElement: HTMLElement | undefined = $state();
  let imageLoaded = $state(false);
  let imageError = $state(false);

  // Show popup and calculate position
  function handleMouseEnter(event: MouseEvent) {
    if (!triggerElement) return;

    showPopup = true;
    calculatePosition(event);
    imageLoaded = false;
    imageError = false;
  }

  // Hide popup
  function handleMouseLeave() {
    showPopup = false;
  }

  // Update position on mouse move for better UX
  function handleMouseMove(event: MouseEvent) {
    if (showPopup) {
      calculatePosition(event);
    }
  }

  // Calculate optimal popup position
  function calculatePosition(_event: MouseEvent) {
    if (!triggerElement) return;

    const triggerRect = triggerElement.getBoundingClientRect();
    const viewportWidth = window.innerWidth;
    const viewportHeight = window.innerHeight;
    const popupWidth = 320; // Popup width
    const popupHeight = 280; // Popup height
    const offsetX = 10; // Horizontal offset from trigger
    const offsetY = 10; // Vertical offset from trigger when below
    const offsetYAbove = 20; // Larger vertical offset when above for better separation

    // Calculate available space in each direction
    const spaceAbove = triggerRect.top;
    const spaceBelow = viewportHeight - triggerRect.bottom;
    const spaceLeft = triggerRect.left;
    const spaceRight = viewportWidth - triggerRect.right;

    // Determine horizontal position
    let x: number;
    if (spaceRight >= popupWidth + offsetX) {
      // Position to the right of trigger
      x = triggerRect.right + offsetX;
    } else if (spaceLeft >= popupWidth + offsetX) {
      // Position to the left of trigger
      x = triggerRect.left - popupWidth - offsetX;
    } else {
      // Center horizontally if not enough space on sides
      x = Math.max(
        10,
        Math.min(viewportWidth / 2 - popupWidth / 2, viewportWidth - popupWidth - 10)
      );
    }

    // Determine vertical position
    // Add extra buffer to trigger earlier (200px buffer)
    const earlyTriggerBuffer = 200;
    let y: number;

    if (spaceBelow >= popupHeight + offsetY + earlyTriggerBuffer) {
      // Position below trigger
      y = triggerRect.bottom + offsetY;
      popupPosition = 'below';
    } else if (spaceAbove >= popupHeight + offsetYAbove) {
      // Position above trigger with larger offset
      y = triggerRect.top - popupHeight - offsetYAbove;
      popupPosition = 'above';
    } else {
      // Position at the top of viewport if not enough space
      y = 10;
      popupPosition = 'below';
    }

    // Ensure popup stays within viewport bounds
    x = Math.max(10, Math.min(x, viewportWidth - popupWidth - 10));
    y = Math.max(10, Math.min(y, viewportHeight - popupHeight - 10));

    popupX = x;
    popupY = y;
  }

  // Handle image load success
  function handleImageLoad() {
    imageLoaded = true;
    imageError = false;
  }

  // Handle image load error - wraps imported handler and updates component state
  function handleImageError(event: Event) {
    handleBirdImageError(event);
    imageLoaded = false;
    imageError = true;
  }

  // Handle focus events for keyboard users
  function handleFocus() {
    // Don't show popup on focus to avoid interference with screen readers
    // Users can press Enter/Space to navigate
  }

  function handleBlur() {
    showPopup = false;
  }
</script>

<!-- Trigger thumbnail with popup -->
<div class="relative inline-block">
  <!-- Trigger thumbnail -->
  <a
    href={detectionUrl}
    bind:this={triggerElement}
    class="inline-block {className} relative"
    onmouseenter={handleMouseEnter}
    onmouseleave={handleMouseLeave}
    onmousemove={handleMouseMove}
    onfocus={handleFocus}
    onblur={handleBlur}
    aria-label="View {commonName} detections"
    aria-describedby={showPopup ? 'bird-popup' : undefined}
  >
    <!-- Thumbnail placeholder -->
    <div class="thumbnail-placeholder w-8 h-8 rounded bg-base-200"></div>
    <img
      src={thumbnailUrl}
      alt={commonName}
      class="thumbnail-image w-8 h-8 rounded object-cover cursor-pointer hover:opacity-80 transition-opacity"
      onerror={handleImageError}
      loading="lazy"
    />
  </a>

  <!-- Popup overlay -->
  {#if showPopup}
    <Portal>
      <div
        bind:this={popupElement}
        id="bird-popup"
        class="fixed z-50 bg-base-100 border border-base-300 rounded-lg shadow-xl p-4 transition-opacity duration-200"
        style:left="{popupX}px"
        style:top="{popupY}px"
        style:width="320px"
        role="tooltip"
        aria-live="polite"
      >
        <!-- Popup content -->
        <div class="space-y-3">
          <!-- Species information header -->
          <div class="text-center space-y-1">
            <h3 class="font-semibold text-base-content text-sm leading-tight">
              {commonName}
            </h3>
            <p class="text-base-content/70 text-xs italic">
              {scientificName}
            </p>
          </div>

          <!-- Large image container -->
          <div class="relative w-full h-48 bg-base-200 rounded-lg overflow-hidden">
            {#if !imageLoaded && !imageError}
              <!-- Loading state -->
              <div class="absolute inset-0 flex items-center justify-center">
                <div class="loading loading-spinner loading-md"></div>
              </div>
            {/if}

            {#if imageError}
              <!-- Error state -->
              <div
                class="absolute inset-0 flex flex-col items-center justify-center text-base-content/50"
              >
                <div class="mb-2">
                  {@html dataIcons.imagePlaceholder}
                </div>
                <p class="text-xs text-center">Image not available</p>
              </div>
            {:else}
              <!-- Large image -->
              <img
                src={thumbnailUrl}
                alt={`Large view of ${commonName}`}
                class="w-full h-full object-cover transition-opacity duration-200"
                class:opacity-0={!imageLoaded}
                class:opacity-100={imageLoaded}
                onload={handleImageLoad}
                onerror={handleImageError}
              />
            {/if}
          </div>

          <!-- Action hint -->
          <div class="text-center">
            <p class="text-xs text-base-content/50">Click to view detections</p>
          </div>
        </div>

        <!-- Popup arrow pointing to trigger -->
        {#if popupPosition === 'below'}
          <!-- Arrow at top of popup -->
          <div
            class="absolute w-3 h-3 bg-base-100 border-l border-t border-base-300 rotate-45 -z-10"
            style:left="20px"
            style:top="-6px"
          ></div>
        {:else}
          <!-- Arrow at bottom of popup -->
          <div
            class="absolute w-3 h-3 bg-base-100 border-r border-b border-base-300 rotate-45 -z-10"
            style:left="20px"
            style:bottom="-6px"
          ></div>
        {/if}
      </div>
    </Portal>
  {/if}
</div>

<style>
  /* Thumbnail placeholder - animated shimmer */
  .thumbnail-placeholder {
    position: absolute;
    top: 0;
    left: 0;
    background: linear-gradient(
      90deg,
      oklch(var(--b2) / 0.5) 0%,
      oklch(var(--b2) / 0.3) 50%,
      oklch(var(--b2) / 0.5) 100%
    );
    background-size: 200% 100%;
    animation: shimmer 1.5s infinite;
  }

  @keyframes shimmer {
    0% {
      background-position: 200% 0;
    }
    100% {
      background-position: -200% 0;
    }
  }

  /* Thumbnail image - covers placeholder when loaded */
  .thumbnail-image {
    position: relative;
    z-index: 1;
    background-color: oklch(var(--b1));
  }

  /* Ensure popup appears above other elements */
  .fixed {
    pointer-events: auto;
  }

  /* Smooth entrance animation */
  #bird-popup {
    animation: popupFadeIn 0.2s ease-out;
  }

  @keyframes popupFadeIn {
    from {
      opacity: 0;
      transform: translateY(-10px) scale(0.95);
    }
    to {
      opacity: 1;
      transform: translateY(0) scale(1);
    }
  }

  /* Ensure popup is accessible on all themes */
  #bird-popup {
    backdrop-filter: blur(8px);
    background-color: hsl(var(--b1) / 0.95);
  }

  /* Mobile responsiveness */
  @media (max-width: 640px) {
    #bird-popup {
      width: calc(100vw - 20px) !important;
      left: 10px !important;
      right: 10px !important;
    }
  }

  /* Respect reduced motion preference */
  @media (prefers-reduced-motion: reduce) {
    .thumbnail-placeholder {
      animation: none;
      background: oklch(var(--b2) / 0.4);
    }
  }
</style>

<!--
  BirdThumbnailPopup Component
  
  A hover-triggered popup that displays a larger bird thumbnail with species information.
  Designed for use in data tables where space is limited but users need to see more detail.
  
  Features:
  - Shows larger image on hover with smooth transitions
  - Displays species common and scientific names
  - Intelligent positioning to avoid viewport edges
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

  interface Props {
    thumbnailUrl: string;
    commonName: string;
    scientificName: string;
    detectionUrl: string;
    className?: string;
  }

  let {
    thumbnailUrl,
    commonName,
    scientificName,
    detectionUrl,
    className = ''
  }: Props = $props();

  // State for popup visibility and positioning
  let showPopup = $state(false);
  let popupX = $state(0);
  let popupY = $state(0);
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
  function calculatePosition(event: MouseEvent) {
    const mouseX = event.clientX;
    const mouseY = event.clientY;
    const offsetX = 20; // Offset from cursor
    const offsetY = 20;
    
    const viewportWidth = window.innerWidth;
    const viewportHeight = window.innerHeight;
    const popupWidth = 320; // Estimated popup width
    const popupHeight = 280; // Estimated popup height
    
    // Default position: bottom-right of cursor
    let x = mouseX + offsetX;
    let y = mouseY + offsetY;
    
    // Adjust if popup would go off right edge
    if (x + popupWidth > viewportWidth) {
      x = mouseX - popupWidth - offsetX;
    }
    
    // Adjust if popup would go off bottom edge
    if (y + popupHeight > viewportHeight) {
      y = mouseY - popupHeight - offsetY;
    }
    
    // Ensure popup stays within viewport
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

  // Handle image load error
  function handleImageError() {
    imageLoaded = false;
    imageError = true;
  }

  // Handle keyboard events for accessibility
  function handleKeyDown(event: KeyboardEvent) {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      // Navigate to detection URL on keyboard activation
      window.location.href = detectionUrl;
    } else if (event.key === 'Escape') {
      showPopup = false;
    }
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
    onkeydown={handleKeyDown}
    onfocus={handleFocus}
    onblur={handleBlur}
    role="button"
    tabindex="0"
    aria-label="View larger image and details for {commonName}"
    aria-describedby={showPopup ? 'bird-popup' : undefined}
  >
    <!-- Thumbnail placeholder -->
    <div class="thumbnail-placeholder w-8 h-8 rounded bg-base-200"></div>
    <img
      src={thumbnailUrl}
      alt={commonName}
      class="thumbnail-image w-8 h-8 rounded object-cover cursor-pointer hover:opacity-80 transition-opacity"
      onerror={handleBirdImageError}
      loading="lazy"
    />
  </a>

  <!-- Popup overlay -->
  {#if showPopup}
    <div
      bind:this={popupElement}
      id="bird-popup"
      class="fixed z-50 bg-base-100 border border-base-300 rounded-lg shadow-xl p-4 transition-opacity duration-200"
      style:left="{popupX}px" style:top="{popupY}px" style:width="320px"
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
            <div class="absolute inset-0 flex flex-col items-center justify-center text-base-content/50">
              <svg class="w-8 h-8 mb-2" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" 
                      d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z" />
              </svg>
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
          <p class="text-xs text-base-content/50">
            Click to view detections
          </p>
        </div>
      </div>

      <!-- Popup arrow pointing to trigger -->
      <div class="absolute w-3 h-3 bg-base-100 border-l border-t border-base-300 rotate-45 -z-10"
           style:left="20px" style:top="-6px"></div>
    </div>
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
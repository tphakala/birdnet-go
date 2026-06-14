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
  - Uses a local portal action to escape overflow containers
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
  import type { ImageAttribution } from '$lib/types/detection.types';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { localizeSpeciesName } from '$lib/utils/speciesDisplay';
  import { validateProtocolURL } from '$lib/utils/security';
  import { t } from '$lib/i18n';
  import { portal } from '$lib/utils/portal';
  import { dropdown } from '$lib/utils/transitions';
  import { loggers } from '$lib/utils/logger';
  import { Image } from '@lucide/svelte';

  const logger = loggers.ui;

  interface Props {
    thumbnailUrl: string;
    commonName: string;
    scientificName: string;
    detectionUrl: string;
    className?: string;
  }

  let { thumbnailUrl, commonName, scientificName, detectionUrl, className = '' }: Props = $props();

  // Localized common name for display in the visitor's UI locale. Falls back to
  // the server-provided common name, then the scientific name.
  const displayName = $derived(localizeSpeciesName(scientificName, commonName));

  // The popup width is fixed (also applied via style:width below), so the
  // constant is the authoritative width. The height is content-driven, so
  // calculatePosition() measures the mounted element and uses the height
  // estimate only as the first-pass fallback before the popup is in the DOM.
  const POPUP_WIDTH = 320;
  const POPUP_HEIGHT_ESTIMATE = 320;

  // Positioning tuning constants (px).
  const POPUP_VIEWPORT_MARGIN = 10; // minimum gap kept from the viewport edges
  const POPUP_OFFSET_X = 10; // horizontal gap from the trigger
  const POPUP_OFFSET_Y = 10; // vertical gap from the trigger when below
  const POPUP_OFFSET_Y_ABOVE = 20; // larger vertical gap when above, for separation
  const POPUP_ARROW_SIZE = 12; // arrow square size, matches the w-3/h-3 classes
  // Flip to the 'above' placement once free space below drops under the popup
  // height plus this buffer, so it flips before the trigger reaches the edge.
  const POPUP_EARLY_FLIP_BUFFER = 200;

  // State for popup visibility and positioning
  let showPopup = $state(false);
  let popupX = $state(0);
  // Vertical anchor. When the popup sits below the trigger we pin its `top`;
  // when it sits above we pin its `bottom` so the bottom edge aligns to the row
  // top regardless of the popup's rendered height. Exactly one is non-null.
  let popupTop = $state<number | null>(0);
  let popupBottom = $state<number | null>(null);
  // Horizontal offset (px) of the arrow within the popup, so it points at the
  // trigger's center instead of a fixed corner.
  let popupArrowX = $state(20);
  let popupPosition = $state<'above' | 'below'>('below');
  let triggerElement: HTMLElement | undefined = $state();
  let popupElement: HTMLElement | undefined = $state();
  let imageLoaded = $state(false);
  let imageError = $state(false);

  // Image attribution state
  let imageAttribution = $state<ImageAttribution | null>(null);
  let lastFetchedScientificName = '';

  // Only allow http(s) license links. The attribution payload comes from an
  // external image provider, so a javascript:/data: URL must never reach href.
  const safeLicenseURL = $derived(
    imageAttribution?.licenseURL &&
      validateProtocolURL(imageAttribution.licenseURL, ['http', 'https'])
      ? imageAttribution.licenseURL
      : undefined
  );

  // Fetch image attribution when popup opens. Attribution is non-critical: a
  // failure only suppresses the credit overlay, it never blocks the popup.
  async function fetchImageAttribution() {
    const name = scientificName?.trim();
    if (!name || lastFetchedScientificName === name) return;
    // Mark as fetched up front so concurrent/repeat hovers do not re-request,
    // and clear any prior attribution so a reused row cannot show it as stale.
    lastFetchedScientificName = name;
    imageAttribution = null;

    try {
      const url = buildAppUrl(`/api/v2/media/species-image/info?name=${encodeURIComponent(name)}`);
      const response = await fetch(url);
      if (response.ok) {
        const data = (await response.json()) as ImageAttribution;
        // Only apply if this row still represents the requested species (it may
        // have been reused for a different one while the request was in flight).
        if (lastFetchedScientificName === name) {
          imageAttribution = data;
        }
      } else if (response.status >= 500 && lastFetchedScientificName === name) {
        // Transient server error: allow a later hover to retry. A 4xx (e.g. a
        // 404 when a species has no attribution) is permanent, so keep the
        // dedupe key to avoid re-querying the API on every subsequent hover.
        lastFetchedScientificName = '';
      }
    } catch (error) {
      // Clear the dedupe key so a later hover can retry after a network error,
      // unless a newer request for a different species has superseded this one.
      if (lastFetchedScientificName === name) {
        lastFetchedScientificName = '';
      }
      logger.debug('Failed to fetch bird image attribution', { error, species: name });
    }
  }

  // Show popup and calculate position
  function handleMouseEnter() {
    if (!triggerElement) return;

    showPopup = true;
    // First pass positions the popup with size estimates (it is not in the DOM
    // yet). Re-measure on the next frame, once mounted, so the 'above' branch
    // uses the real height. Mirrors ActionMenu/AudioSettingsButton.
    calculatePosition();
    globalThis.requestAnimationFrame(() => {
      if (showPopup) calculatePosition();
    });
    imageLoaded = false;
    imageError = false;
    fetchImageAttribution();
  }

  // Hide popup
  function handleMouseLeave() {
    showPopup = false;
  }

  // Keep the popup anchored to its trigger while open. It is portaled to
  // <body> with position:fixed, so without this it would drift away from the
  // row on scroll/resize. Placement is trigger-relative (not cursor-relative),
  // so there is no need to recompute on mousemove.
  $effect(() => {
    if (!showPopup) return;

    const reposition = () => calculatePosition();
    // Capture phase so scrolls inside nested overflow containers are caught too.
    window.addEventListener('scroll', reposition, true);
    window.addEventListener('resize', reposition);

    return () => {
      window.removeEventListener('scroll', reposition, true);
      window.removeEventListener('resize', reposition);
    };
  });

  // Calculate optimal popup position.
  //
  // Vertical placement uses the real rendered popup height once it is mounted
  // (offsetHeight, which ignores the dropdown transition's scale transform);
  // POPUP_HEIGHT_ESTIMATE is only the fallback for the first synchronous pass,
  // before the popup exists in the DOM. Width is fixed via POPUP_WIDTH.
  function calculatePosition() {
    if (!triggerElement) return;

    const triggerRect = triggerElement.getBoundingClientRect();
    const viewportWidth = window.innerWidth;
    const viewportHeight = window.innerHeight;
    const popupWidth = POPUP_WIDTH;
    // offsetHeight is 0 before the first layout pass (or while hidden); fall
    // back to the estimate then instead of positioning against a 0 height.
    const measuredHeight = popupElement?.offsetHeight ?? 0;
    const popupHeight = measuredHeight > 0 ? measuredHeight : POPUP_HEIGHT_ESTIMATE;

    // Calculate available space in each direction
    const spaceAbove = triggerRect.top;
    const spaceBelow = viewportHeight - triggerRect.bottom;
    const spaceLeft = triggerRect.left;
    const spaceRight = viewportWidth - triggerRect.right;

    // Determine horizontal position
    let x: number;
    if (spaceRight >= popupWidth + POPUP_OFFSET_X) {
      // Position to the right of trigger
      x = triggerRect.right + POPUP_OFFSET_X;
    } else if (spaceLeft >= popupWidth + POPUP_OFFSET_X) {
      // Position to the left of trigger
      x = triggerRect.left - popupWidth - POPUP_OFFSET_X;
    } else {
      // Center horizontally if not enough space on sides
      x = Math.max(
        POPUP_VIEWPORT_MARGIN,
        Math.min(
          viewportWidth / 2 - popupWidth / 2,
          viewportWidth - popupWidth - POPUP_VIEWPORT_MARGIN
        )
      );
    }
    // Ensure popup stays within viewport bounds horizontally
    popupX = Math.max(
      POPUP_VIEWPORT_MARGIN,
      Math.min(x, viewportWidth - popupWidth - POPUP_VIEWPORT_MARGIN)
    );

    // Align the arrow's midpoint (not its left edge) with the trigger center,
    // clamped so the arrow stays within the popup body and clear of its corners.
    const triggerCenterX = triggerRect.left + triggerRect.width / 2;
    popupArrowX = Math.max(
      POPUP_ARROW_SIZE,
      Math.min(triggerCenterX - popupX - POPUP_ARROW_SIZE / 2, popupWidth - POPUP_ARROW_SIZE * 2)
    );

    // Determine vertical position. Flip to 'above' early via the buffer (before
    // the trigger reaches the very edge of the viewport).
    if (spaceBelow >= popupHeight + POPUP_OFFSET_Y + POPUP_EARLY_FLIP_BUFFER) {
      // Below: anchor the popup's TOP edge just under the trigger row. Height
      // independent, which is why the 'below' case never misaligned.
      popupPosition = 'below';
      popupTop = triggerRect.bottom + POPUP_OFFSET_Y;
      popupBottom = null;
    } else if (spaceAbove >= popupHeight + POPUP_OFFSET_Y_ABOVE) {
      // Above: anchor the popup's BOTTOM edge just above the trigger row by
      // pinning `bottom` to the viewport. Aligning the bottom edge means the
      // exact height is irrelevant and the popup grows upward from the row.
      popupPosition = 'above';
      popupBottom = viewportHeight - triggerRect.top + POPUP_OFFSET_Y_ABOVE;
      popupTop = null;
    } else {
      // Not enough room either way: pin near the top of the viewport.
      popupPosition = 'below';
      popupTop = POPUP_VIEWPORT_MARGIN;
      popupBottom = null;
    }
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

  // Close the popup if the trigger loses focus. The popup is intentionally not
  // opened on focus, to avoid interfering with screen readers; keyboard users
  // press Enter/Space to navigate to the detail page instead.
  function handleBlur() {
    showPopup = false;
  }
</script>

<!-- Trigger thumbnail with popup -->
<div class="relative flex">
  <!-- Trigger thumbnail -->
  <a
    href={detectionUrl}
    bind:this={triggerElement}
    class="flex {className} relative"
    onmouseenter={handleMouseEnter}
    onmouseleave={handleMouseLeave}
    onblur={handleBlur}
    aria-label={t('components.birdThumbnail.viewDetections', { name: displayName })}
    aria-describedby={showPopup ? 'bird-popup' : undefined}
  >
    <!-- Thumbnail placeholder -->
    <div class="thumbnail-placeholder w-8 h-6 rounded-sm bg-[var(--color-base-200)]"></div>
    <img
      src={thumbnailUrl}
      alt={displayName}
      class="thumbnail-image w-8 h-6 rounded-sm object-cover cursor-pointer hover:opacity-80 transition-opacity"
      onerror={handleImageError}
      loading="lazy"
    />
  </a>

  <!-- Popup overlay -->
  {#if showPopup}
    <div
      bind:this={popupElement}
      use:portal
      id="bird-popup"
      in:dropdown
      out:dropdown={{ duration: 100 }}
      class="fixed z-50 bg-[var(--color-base-100)] border border-[var(--color-base-300)] rounded-lg shadow-xl p-4"
      style:left="{popupX}px"
      style:top={popupTop !== null ? `${popupTop}px` : undefined}
      style:bottom={popupBottom !== null ? `${popupBottom}px` : undefined}
      style:width="{POPUP_WIDTH}px"
      role="tooltip"
      aria-live="polite"
    >
      <!-- Popup content -->
      <div class="space-y-3">
        <!-- Species information header -->
        <div class="text-center space-y-1">
          <h3 class="font-semibold text-[var(--color-base-content)] text-sm leading-tight">
            {displayName}
          </h3>
          <p
            class="text-xs italic"
            style:color="color-mix(in srgb, var(--color-base-content) 70%, transparent)"
          >
            {scientificName}
          </p>
        </div>

        <!-- Large image container -->
        <div
          class="relative w-full aspect-[4/3] bg-[var(--color-base-200)] rounded-lg overflow-hidden"
        >
          {#if !imageLoaded && !imageError}
            <!-- Loading state -->
            <div
              class="absolute inset-0 flex items-center justify-center"
              role="status"
              aria-label={t('common.ui.loading')}
            >
              <div class="loading loading-spinner loading-md"></div>
            </div>
          {/if}

          {#if imageError}
            <!-- Error state -->
            <div
              class="absolute inset-0 flex flex-col items-center justify-center"
              style:color="color-mix(in srgb, var(--color-base-content) 50%, transparent)"
            >
              <Image class="size-8 mb-2" />
              <p class="text-xs text-center">{t('common.ui.imageNotAvailable')}</p>
            </div>
          {:else}
            <!-- Large image -->
            <img
              src={thumbnailUrl}
              alt={t('components.birdThumbnail.largeView', { name: displayName })}
              class="w-full h-full object-contain transition-opacity duration-200"
              class:opacity-0={!imageLoaded}
              class:opacity-100={imageLoaded}
              onload={handleImageLoad}
              onerror={handleImageError}
            />
          {/if}

          <!-- Photo credit overlay -->
          {#if imageAttribution?.authorName && imageLoaded}
            <div
              class="thumbnail-credit"
              aria-label={t('common.aria.imageCredit', { name: imageAttribution.authorName })}
            >
              <span class="credit-text">{imageAttribution.authorName}</span>
              {#if imageAttribution.licenseName}
                <span class="credit-separator">·</span>
                {#if safeLicenseURL}
                  <a
                    href={safeLicenseURL}
                    target="_blank"
                    rel="noopener noreferrer"
                    class="credit-license">{imageAttribution.licenseName}</a
                  >
                {:else}
                  <span class="credit-license">{imageAttribution.licenseName}</span>
                {/if}
              {/if}
            </div>
          {/if}
        </div>

        <!-- Action hint -->
        <div class="text-center">
          <p
            class="text-xs"
            style:color="color-mix(in srgb, var(--color-base-content) 50%, transparent)"
          >
            {t('components.birdThumbnail.clickToView')}
          </p>
        </div>
      </div>

      <!-- Popup arrow pointing to trigger -->
      {#if popupPosition === 'below'}
        <!-- Arrow at top of popup -->
        <div
          class="absolute w-3 h-3 bg-[var(--color-base-100)] border-l border-t border-[var(--color-base-300)] rotate-45 -z-10"
          style:left="{popupArrowX}px"
          style:top="-6px"
        ></div>
      {:else}
        <!-- Arrow at bottom of popup -->
        <div
          class="absolute w-3 h-3 bg-[var(--color-base-100)] border-r border-b border-[var(--color-base-300)] rotate-45 -z-10"
          style:left="{popupArrowX}px"
          style:bottom="-6px"
        ></div>
      {/if}
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
      color-mix(in srgb, var(--color-base-200) 50%, transparent) 0%,
      color-mix(in srgb, var(--color-base-200) 30%, transparent) 50%,
      color-mix(in srgb, var(--color-base-200) 50%, transparent) 100%
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
    background-color: var(--color-base-100);
  }

  /* Ensure popup appears above other elements */
  .fixed {
    pointer-events: auto;
  }

  /* Theme accessibility */
  #bird-popup {
    backdrop-filter: blur(8px);
    background-color: color-mix(in srgb, var(--color-base-100) 95%, transparent);
  }

  /* Mobile responsiveness */
  @media (max-width: 640px) {
    #bird-popup {
      width: calc(100vw - 20px) !important;
      left: 10px !important;
      right: 10px !important;
    }
  }

  /* Photo credit — bottom-right corner of image */
  .thumbnail-credit {
    position: absolute;
    right: 0;
    bottom: 0;
    display: flex;
    align-items: center;
    gap: 0.2rem;
    padding: 0.2rem 0.4rem;
    background: oklch(10% 0 0deg / 0.55);
    border-top-left-radius: 0.375rem;
  }

  .credit-text {
    font-size: 0.5625rem;
    color: oklch(95% 0 0deg);
    line-height: 1;
    white-space: nowrap;
  }

  .credit-separator {
    font-size: 0.5625rem;
    color: oklch(70% 0 0deg);
    flex-shrink: 0;
    line-height: 1;
  }

  .credit-license {
    font-size: 0.5625rem;
    color: oklch(80% 0 0deg);
    text-decoration: none;
    line-height: 1;
    flex-shrink: 0;
    white-space: nowrap;
  }

  a.credit-license:hover {
    color: white;
    text-decoration: underline;
  }

  /* Respect reduced motion preference */
  @media (prefers-reduced-motion: reduce) {
    .thumbnail-placeholder {
      animation: none;
      background: color-mix(in srgb, var(--color-base-200) 40%, transparent);
    }
  }
</style>

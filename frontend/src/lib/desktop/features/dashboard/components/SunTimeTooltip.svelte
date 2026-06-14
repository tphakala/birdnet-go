<!--
  SunTimeTooltip.svelte - sunrise/sunset icon with a styled hover tooltip.

  Renders the sunrise or sunset icon for a daylight-row cell and shows the
  formatted time in a small styled tooltip on hover.

  The tooltip is portaled to <body> and positioned with position:fixed against
  the icon's viewport rect. This is deliberate: the daylight row lives inside
  the daily-summary grid's overflow-x:auto scroll wrapper. An in-DOM,
  absolutely-positioned tooltip there both widened the wrapper's scrollable area
  (a permanent horizontal scrollbar, since the icon sits near the grid's right
  edge) and was itself clipped by that same container. Portaling the tooltip out
  of the scroll wrapper avoids both problems while keeping the styled look.

  Mirrors the positioning approach used by BirdThumbnailPopup.svelte.
-->

<script lang="ts">
  import { t } from '$lib/i18n';
  import { portal } from '$lib/utils/portal';
  import { Z_INDEX } from '$lib/utils/z-index';
  import { Sunrise, Sunset } from '@lucide/svelte';

  interface Props {
    sunType: 'sunrise' | 'sunset';
    time: string; // formatted "HH:MM"
  }

  let { sunType, time }: Props = $props();

  // Unique id per instance for aria-describedby. Math.random (not
  // crypto.randomUUID) because BirdNET-Go commonly runs on plain HTTP, where
  // the secure-context crypto API is undefined.
  const tooltipId = `sun-tooltip-${Math.random().toString(36).slice(2, 10)}`;

  // Positioning tuning (px).
  const VIEWPORT_MARGIN = 8; // minimum gap kept from viewport edges
  const OFFSET_Y = 6; // gap between the icon and the tooltip
  const TOOLTIP_HEIGHT_ESTIMATE = 20; // fallback before the tooltip is mounted

  let show = $state(false);
  let triggerEl: HTMLElement | undefined = $state();
  let tooltipEl: HTMLElement | undefined = $state();
  let tipLeft = $state(0);
  // Exactly one of tipTop / tipBottom is non-null: 'below' pins the top edge,
  // 'above' pins the bottom edge so the height never has to be known exactly.
  let tipTop = $state<number | null>(null);
  let tipBottom = $state<number | null>(null);

  // Localized accessible label, e.g. "Sunrise at 05:23". Reuses the existing
  // daylight keys, so no new i18n strings are introduced.
  const label = $derived(t(`dashboard.dailySummary.daylight.${sunType}`, { time }));

  function position() {
    if (!triggerEl) return;
    const rect = triggerEl.getBoundingClientRect();
    const vw = window.innerWidth;
    const vh = window.innerHeight;
    const width = tooltipEl?.offsetWidth ?? 0;
    const height = tooltipEl?.offsetHeight || TOOLTIP_HEIGHT_ESTIMATE;

    // Center horizontally over the icon, clamped so the tooltip (drawn with
    // translateX(-50%)) stays within the viewport margins.
    const half = width / 2;
    const centerX = rect.left + rect.width / 2;
    tipLeft = Math.max(VIEWPORT_MARGIN + half, Math.min(centerX, vw - VIEWPORT_MARGIN - half));

    // Prefer above the icon; flip below only when there is not enough room.
    if (rect.top >= height + OFFSET_Y + VIEWPORT_MARGIN) {
      tipBottom = vh - rect.top + OFFSET_Y;
      tipTop = null;
    } else {
      tipTop = rect.bottom + OFFSET_Y;
      tipBottom = null;
    }
  }

  function open() {
    show = true;
    // First pass uses size estimates (the tooltip is not mounted yet); the rAF
    // pass re-measures once it is in the DOM, like BirdThumbnailPopup.
    position();
    globalThis.requestAnimationFrame(() => {
      if (show) position();
    });
  }

  function close() {
    show = false;
  }

  // Keep the tooltip anchored to its icon while shown. It is portaled to <body>
  // with position:fixed, so without this it would drift on scroll/resize.
  $effect(() => {
    if (!show) return;

    const reposition = () => position();
    // Capture phase so scrolls inside nested overflow containers are caught too.
    window.addEventListener('scroll', reposition, true);
    window.addEventListener('resize', reposition);

    return () => {
      window.removeEventListener('scroll', reposition, true);
      window.removeEventListener('resize', reposition);
    };
  });
</script>

<div
  bind:this={triggerEl}
  class="sun-icon-wrapper"
  role="img"
  aria-label={label}
  aria-describedby={show ? tooltipId : undefined}
  onmouseenter={open}
  onmouseleave={close}
>
  {#if sunType === 'sunrise'}
    <Sunrise class="size-3.5 text-orange-700" aria-hidden="true" />
  {:else}
    <Sunset class="size-3.5 text-rose-700" aria-hidden="true" />
  {/if}
</div>

{#if show}
  <span
    bind:this={tooltipEl}
    use:portal
    id={tooltipId}
    role="tooltip"
    class="sun-tooltip sun-tooltip-{sunType}"
    style:z-index={Z_INDEX.PORTAL_TOOLTIP}
    style:left="{tipLeft}px"
    style:top={tipTop !== null ? `${tipTop}px` : undefined}
    style:bottom={tipBottom !== null ? `${tipBottom}px` : undefined}
  >
    {time}
  </span>
{/if}

<style>
  /* Centers the sunrise/sunset icon within its daylight-row grid cell. */
  .sun-icon-wrapper {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 100%;
    height: 1.25rem; /* 20px - matches grid-daylight-height */
  }

  /* Portaled to <body>; positioned via fixed coordinates set inline above.
     translateX(-50%) centers the box on the computed `left`. */
  .sun-tooltip {
    position: fixed;
    transform: translateX(-50%);
    padding: 2px 6px;
    font-size: 10px;
    font-weight: 600;
    white-space: nowrap;
    border-radius: 4px;
    pointer-events: none;
    animation: sun-tooltip-fade 0.12s ease-out;
  }

  @keyframes sun-tooltip-fade {
    from {
      opacity: 0;
    }

    to {
      opacity: 1;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .sun-tooltip {
      animation: none;
    }
  }

  /* Sunrise tooltip - orange theme */
  .sun-tooltip-sunrise {
    background-color: #fff7ed; /* orange-50 */
    color: #c2410c; /* orange-700 */
    border: 1px solid #fed7aa; /* orange-200 */
    box-shadow: 0 2px 8px rgb(251 146 60 / 0.25);
  }

  :global([data-theme='dark']) .sun-tooltip-sunrise {
    background-color: #431407; /* orange-950 */
    color: #fdba74; /* orange-300 */
    border: 1px solid #7c2d12; /* orange-900 */
  }

  /* Sunset tooltip - rose/pink theme */
  .sun-tooltip-sunset {
    background-color: #fff1f2; /* rose-50 */
    color: #be123c; /* rose-700 */
    border: 1px solid #fecdd3; /* rose-200 */
    box-shadow: 0 2px 8px rgb(251 113 133 / 0.25);
  }

  :global([data-theme='dark']) .sun-tooltip-sunset {
    background-color: #4c0519; /* rose-950 */
    color: #fda4af; /* rose-300 */
    border: 1px solid #881337; /* rose-900 */
  }
</style>

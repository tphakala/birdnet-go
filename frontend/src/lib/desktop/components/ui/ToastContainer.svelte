<!--
  ToastContainer Component
  
  A container component that manages and displays toast notifications.
  Should be placed once at the root level of your app layout.
  
  Features:
  - Displays multiple toast messages
  - Groups toasts by position
  - Uses NotificationToast component for individual toasts
  - Handles toast removal
  
  Usage:
  Import this component and place it in your root layout.
  Use toastActions from $lib/stores/toast to show toasts.
-->

<script lang="ts">
  import { toasts, toastActions } from '$lib/stores/toast';
  import NotificationToast from './NotificationToast.svelte';
  import type { ToastMessage, ToastPosition } from '$lib/stores/toast';
  import { safeGet } from '$lib/utils/security';
  import { t } from '$lib/i18n';
  import { untrack } from 'svelte';

  // Group toasts by position using Record with pre-initialized keys
  const toastsByPosition = $derived.by(() => {
    const result: Record<ToastPosition, ToastMessage[]> = {
      'top-left': [],
      'top-center': [],
      'top-right': [],
      'bottom-left': [],
      'bottom-center': [],
      'bottom-right': [],
    };

    for (const toast of $toasts) {
      const position = toast.position || 'top-right';
      // eslint-disable-next-line security/detect-object-injection -- Safe: position is guaranteed to be a valid ToastPosition key (either from toast.position or defaulting to 'top-right')
      result[position].push(toast);
    }

    return result;
  });

  // Position container classes
  const positionClasses: Record<ToastPosition, string> = {
    'top-left': 'top-4 left-4',
    'top-center': 'top-4 left-1/2 -translate-x-1/2',
    'top-right': 'top-4 right-4',
    'bottom-left': 'bottom-4 left-4',
    'bottom-center': 'bottom-4 left-1/2 -translate-x-1/2',
    'bottom-right': 'bottom-4 right-4',
  };

  // Translated accessible name for each position's notification region. Each
  // entry calls t() with a literal key so the i18n usage checker sees every
  // key, and t() runs at render time so the label follows a locale change.
  const regionLabels: Record<ToastPosition, () => string> = {
    'top-left': () => t('common.aria.toastRegion.topLeft'),
    'top-center': () => t('common.aria.toastRegion.topCenter'),
    'top-right': () => t('common.aria.toastRegion.topRight'),
    'bottom-left': () => t('common.aria.toastRegion.bottomLeft'),
    'bottom-center': () => t('common.aria.toastRegion.bottomCenter'),
    'bottom-right': () => t('common.aria.toastRegion.bottomRight'),
  };

  function regionLabel(position: ToastPosition): string {
    // eslint-disable-next-line security/detect-object-injection -- Safe: position is a ToastPosition key
    return regionLabels[position]();
  }

  function handleClose(id: string) {
    toastActions.remove(id);
  }

  // Focus tracking. Plain variables on purpose: they are read only when the toast
  // list changes and by event handlers, never by the template.
  let focusedToastId: string | null = null;
  // Where focus was before it entered the toasts
  let returnFocusTo: HTMLElement | null = null;
  // Toast ids per region (in region order) as of the previous update, to find a removed toast's neighbours
  let previousOrder: string[][] | null = null;

  // Attributes written in this file's template and in NotificationToast
  const REGION_SELECTOR = '[data-toast-region]';
  const TOAST_SELECTOR = '[data-toast-id]';
  const TOAST_CLOSE_SELECTOR = '[data-toast-close]';

  function isInsideRegions(node: Node): boolean {
    for (const region of document.querySelectorAll(REGION_SELECTOR)) {
      if (region.contains(node)) return true;
    }
    return false;
  }

  // Node, not HTMLElement: a press on SVG content has an SVGElement target
  function isOutsideRegions(node: unknown): node is Node {
    return node instanceof Node && !isInsideRegions(node);
  }

  function handleFocusIn(event: FocusEvent) {
    if (!(event.target instanceof HTMLElement)) return;
    const toastId = event.target.closest<HTMLElement>(TOAST_SELECTOR)?.dataset.toastId;
    if (!toastId) return;
    // Entering from outside the toasts: remember where from. Moving between
    // toasts keeps the earlier answer.
    if (focusedToastId === null) {
      const from = event.relatedTarget;
      returnFocusTo = from instanceof HTMLElement && isOutsideRegions(from) ? from : null;
    }
    focusedToastId = toastId;
  }

  function handleFocusOut(event: FocusEvent) {
    // A null relatedTarget may be the toast being removed (or the window losing
    // focus), so only a move to an element outside the toasts ends the tracking
    if (isOutsideRegions(event.relatedTarget)) focusedToastId = null;
  }

  // A click on the page moves the user's attention away even when it takes no focus
  function handlePointerDown(event: MouseEvent) {
    if (isOutsideRegions(event.target)) focusedToastId = null;
  }

  function closeButtonOf(toastId: string): HTMLElement | null {
    for (const wrapper of document.querySelectorAll<HTMLElement>(TOAST_SELECTOR)) {
      if (wrapper.dataset.toastId === toastId) {
        return wrapper.querySelector<HTMLElement>(TOAST_CLOSE_SELECTOR);
      }
    }
    return null;
  }

  /**
   * After the toast list changed: when the toast that held focus is gone and
   * focus fell to <body>, move it to the next toast, else the previous one, else
   * back to where it came from. Focus the user has already moved is left alone.
   */
  function restoreFocusAfterRemoval(current: string[][], previous: string[][] | null) {
    const removedId = focusedToastId;
    if (removedId === null) return;
    if (current.some(ids => ids.includes(removedId))) return;

    focusedToastId = null;
    const active = document.activeElement;
    if (active && active !== document.body) return;

    // The toast's region, in the previous and the current order
    const regionIndex = previous?.findIndex(ids => ids.includes(removedId)) ?? -1;
    const before = regionIndex >= 0 ? (previous?.at(regionIndex) ?? []) : [];
    const remaining = regionIndex >= 0 ? (current.at(regionIndex) ?? []) : [];
    const index = before.indexOf(removedId);
    const next = before.slice(index + 1).find(id => remaining.includes(id));
    const earlier = before
      .slice(0, Math.max(index, 0))
      .filter(id => remaining.includes(id))
      .at(-1);
    const neighbour = next ?? earlier;
    const target = neighbour ? closeButtonOf(neighbour) : null;
    if (neighbour && target) {
      // The neighbour now holds focus: keep tracking it, and keep the return target
      focusedToastId = neighbour;
      target.focus();
    } else if (returnFocusTo?.isConnected && isOutsideRegions(returnFocusTo)) {
      returnFocusTo.focus();
    }
  }

  $effect(() => {
    const current = Object.values(toastsByPosition).map(list => list.map(toast => toast.id));
    untrack(() => {
      restoreFocusAfterRemoval(current, previousOrder);
      previousOrder = current;
    });
  });
</script>

<svelte:document onpointerdown={handlePointerDown} />

<!-- Render a toast container for every position, even when empty, so each live region exists before a toast is added to it -->
{#each Object.entries(toastsByPosition) as [position, positionToasts] (position)}
  <!-- z-[2000] = Z_INDEX.TOAST: toasts must stay above all overlays, including the mobile sidebar drawer (z-[200]) -->
  <div
    class="fixed z-[2000] pointer-events-none {safeGet(
      positionClasses,
      position as ToastPosition,
      ''
    )}"
    role="region"
    aria-live="polite"
    aria-label={regionLabel(position as ToastPosition)}
    data-toast-region
    onfocusin={handleFocusIn}
    onfocusout={handleFocusOut}
  >
    <div class="flex flex-col gap-2">
      {#each positionToasts as toast (toast.id)}
        <div class="pointer-events-auto" data-toast-id={toast.id}>
          <NotificationToast
            type={toast.type}
            message={toast.message}
            duration={toast.duration}
            actions={toast.actions}
            position={toast.position}
            showIcon={toast.showIcon}
            onClose={() => handleClose(toast.id)}
          />
        </div>
      {/each}
    </div>
  </div>
{/each}

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
  let focusedToastPosition: ToastPosition | null = null;
  // Where focus was before it entered the toasts
  let returnFocusTo: HTMLElement | null = null;
  // Toast ids per position as of the previous update, to find a removed toast's neighbours
  let previousOrder: Record<ToastPosition, string[]> | null = null;

  const REGION_SELECTOR = '[data-toast-region]';

  function isOutsideRegions(node: unknown): node is HTMLElement {
    return node instanceof HTMLElement && !node.closest(REGION_SELECTOR);
  }

  function handleFocusIn(event: FocusEvent, position: ToastPosition) {
    if (!(event.target instanceof HTMLElement)) return;
    const toastId = event.target.closest<HTMLElement>('[data-toast-id]')?.dataset.toastId;
    if (!toastId) return;
    // Entering from outside the toasts: remember where from. Moving between
    // toasts keeps the earlier answer.
    if (focusedToastId === null) {
      returnFocusTo = isOutsideRegions(event.relatedTarget) ? event.relatedTarget : null;
    }
    focusedToastId = toastId;
    focusedToastPosition = position;
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
    for (const wrapper of document.querySelectorAll<HTMLElement>('[data-toast-id]')) {
      if (wrapper.dataset.toastId === toastId) {
        return wrapper.querySelector<HTMLElement>('[data-toast-close]');
      }
    }
    return null;
  }

  /**
   * After the toast list changed: when the toast that held focus is gone and
   * focus fell to <body>, move it to the next toast, else the previous one, else
   * back to where it came from. Focus the user has already moved is left alone.
   */
  function restoreFocusAfterRemoval(
    current: Record<ToastPosition, string[]>,
    previous: Record<ToastPosition, string[]> | null
  ) {
    const removedId = focusedToastId;
    const position = focusedToastPosition;
    if (removedId === null || position === null) return;
    // eslint-disable-next-line security/detect-object-injection -- Safe: position is a ToastPosition key
    const remaining = current[position];
    if (remaining.includes(removedId)) return;

    focusedToastId = null;
    const active = document.activeElement;
    if (active && active !== document.body) return;

    // eslint-disable-next-line security/detect-object-injection -- Safe: position is a ToastPosition key
    const before = previous?.[position] ?? [];
    const index = before.indexOf(removedId);
    const next = before.slice(index + 1).find(id => remaining.includes(id));
    const earlier = before
      .slice(0, Math.max(index, 0))
      .filter(id => remaining.includes(id))
      .at(-1);
    const neighbour = next ?? earlier;
    const target = neighbour ? closeButtonOf(neighbour) : null;
    if (target) {
      target.focus();
    } else if (returnFocusTo?.isConnected && isOutsideRegions(returnFocusTo)) {
      returnFocusTo.focus();
    }
  }

  $effect(() => {
    const idsOf = (position: ToastPosition) =>
      // eslint-disable-next-line security/detect-object-injection -- Safe: position is a ToastPosition key
      toastsByPosition[position].map(toast => toast.id);
    const current: Record<ToastPosition, string[]> = {
      'top-left': idsOf('top-left'),
      'top-center': idsOf('top-center'),
      'top-right': idsOf('top-right'),
      'bottom-left': idsOf('bottom-left'),
      'bottom-center': idsOf('bottom-center'),
      'bottom-right': idsOf('bottom-right'),
    };
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
    onfocusin={event => handleFocusIn(event, position as ToastPosition)}
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

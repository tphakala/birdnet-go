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

  // Group toasts by position using Map to avoid object injection
  const toastsByPosition = $derived(() => {
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
      const existingToasts = safeGet(result, position, []);
      if (existingToasts) {
        existingToasts.push(toast);
      }
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

  function handleClose(id: string) {
    toastActions.remove(id);
  }
</script>

<!-- Render toast containers for each position that has toasts -->
{#each Object.entries(toastsByPosition()) as [position, positionToasts]}
  <div
    class="fixed z-50 pointer-events-none {safeGet(positionClasses, position as ToastPosition, '')}"
    role="region"
    aria-live="polite"
    aria-label="{position} notifications"
  >
    <div class="flex flex-col gap-2">
      {#each positionToasts as toast (toast.id)}
        <div class="pointer-events-auto">
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

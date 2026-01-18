<!--
  ActionMenu Component
  
  A dropdown menu component that provides action buttons for detection items.
  Displays common actions like review, toggle species visibility, lock/unlock, and delete.
  
  Features:
  - Automatically positions menu to stay within viewport
  - High z-index (9999) to appear above all other elements
  - Keyboard navigation support (Escape to close)
  - Click-outside-to-close behavior
  - Responsive positioning (above/below button based on available space)
  - Fixed positioning to handle scrollable containers
  
  @component
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Detection } from '$lib/types/detection.types';
  import { MoreVertical, SquarePen, Eye, EyeOff, Lock, LockOpen, Trash2 } from '@lucide/svelte';

  interface Props {
    /** The detection object containing data for this action menu */
    detection: Detection;
    /** Whether the species is currently excluded from detection */
    isExcluded?: boolean;
    /** Callback fired when user clicks review action */
    onReview?: () => void;
    /** Callback fired when user toggles species visibility */
    onToggleSpecies?: () => void;
    /** Callback fired when user toggles detection lock status */
    onToggleLock?: () => void;
    /** Callback fired when user deletes the detection */
    onDelete?: () => void;
    /** Additional CSS classes to apply to the component */
    className?: string;
    /** Callback fired when menu opens */
    onMenuOpen?: () => void;
    /** Callback fired when menu closes */
    onMenuClose?: () => void;
  }

  let {
    detection,
    isExcluded = false,
    onReview,
    onToggleSpecies,
    onToggleLock,
    onDelete,
    className = '',
    onMenuOpen,
    onMenuClose,
  }: Props = $props();

  let isOpen = $state(false);
  let buttonElement: HTMLButtonElement;
  // svelte-ignore non_reactive_update
  let menuElement: HTMLUListElement;

  /**
   * Updates the menu position to ensure it stays within the viewport.
   * Uses fixed positioning and calculates optimal placement above or below the button.
   */
  function updateMenuPosition() {
    if (!menuElement || !buttonElement) return;

    const buttonRect = buttonElement.getBoundingClientRect();
    const spaceBelow = window.innerHeight - buttonRect.bottom;
    const spaceAbove = buttonRect.top;
    const menuHeight = menuElement.offsetHeight;

    // Position menu relative to viewport
    menuElement.style.position = 'fixed';
    menuElement.style.zIndex = '9999';

    // Determine vertical position
    if (spaceBelow < menuHeight && spaceAbove > spaceBelow) {
      menuElement.style.bottom = `${window.innerHeight - buttonRect.top + 8}px`;
      menuElement.style.top = 'auto';
    } else {
      menuElement.style.top = `${buttonRect.bottom + 8}px`;
      menuElement.style.bottom = 'auto';
    }

    // Always align menu's right edge with button's right edge
    menuElement.style.left = 'auto';
    menuElement.style.right = `${window.innerWidth - buttonRect.right}px`;
  }

  /** Toggles the menu open/closed state and updates position when opening */
  function handleOpen(event: MouseEvent) {
    // Prevent event from bubbling to parent elements (like detection row click handlers)
    event.stopPropagation();

    // Toggle menu open/closed
    isOpen = !isOpen;

    // Call appropriate callback
    if (isOpen) {
      onMenuOpen?.();
      globalThis.requestAnimationFrame(updateMenuPosition);
    } else {
      onMenuClose?.();
    }
  }

  /** Executes an action callback and closes the menu */
  function handleAction(action: (() => void) | undefined) {
    isOpen = false;
    onMenuClose?.();
    if (action) {
      action();
    }
  }

  /** Closes menu when clicking outside the menu or button */
  function handleClickOutside(event: MouseEvent) {
    if (
      isOpen &&
      menuElement &&
      !menuElement.contains(event.target as Node) &&
      buttonElement &&
      !buttonElement.contains(event.target as Node)
    ) {
      isOpen = false;
      onMenuClose?.();
    }
  }

  /** Closes menu when Escape key is pressed */
  function handleKeydown(event: KeyboardEvent) {
    if (isOpen && event.key === 'Escape') {
      isOpen = false;
      onMenuClose?.();
      buttonElement?.focus();
    }
  }

  // PERFORMANCE OPTIMIZATION: Use Svelte 5 $effect instead of legacy onMount
  // $effect provides better reactivity and automatic cleanup management
  // Only attach event listeners when menu is open to reduce global event overhead
  $effect(() => {
    if (isOpen) {
      // Update menu position on window resize and scroll
      function handleResize() {
        updateMenuPosition();
      }

      // Attach event listeners only when menu is open
      document.addEventListener('click', handleClickOutside);
      document.addEventListener('keydown', handleKeydown);
      window.addEventListener('resize', handleResize);
      window.addEventListener('scroll', handleResize, true);

      return () => {
        // Automatic cleanup when effect re-runs or component unmounts
        document.removeEventListener('click', handleClickOutside);
        document.removeEventListener('keydown', handleKeydown);
        window.removeEventListener('resize', handleResize);
        window.removeEventListener('scroll', handleResize, true);
      };
    }
  });

  // PERFORMANCE OPTIMIZATION: Cleanup effect using Svelte 5 pattern
  // Automatically handles component unmount cleanup
  $effect(() => {
    return () => {
      // If menu is open when component unmounts, call onMenuClose to keep count synchronized
      if (isOpen && onMenuClose) {
        onMenuClose();
      }
    };
  });
</script>

<div class={cn('dropdown relative', className)}>
  <button
    bind:this={buttonElement}
    onclick={handleOpen}
    class="btn btn-ghost btn-sm min-h-8 h-8 w-8 p-1"
    aria-label="Actions menu"
    aria-haspopup="true"
    aria-expanded={isOpen}
  >
    <MoreVertical class="size-5" />
  </button>

  {#if isOpen}
    <ul
      bind:this={menuElement}
      class="fixed menu p-2 shadow-lg bg-base-100 rounded-box w-52 border border-base-300"
      role="menu"
    >
      <li>
        <button
          onclick={() => handleAction(onReview)}
          class="text-sm w-full text-left"
          role="menuitem"
        >
          <div class="flex items-center gap-2">
            <SquarePen class="size-4" />
            <span>Review detection</span>
            {#if detection.verified === 'correct'}
              <span class="badge badge-success badge-sm">✓</span>
            {:else if detection.verified === 'false_positive'}
              <span class="badge badge-error badge-sm">✗</span>
            {/if}
          </div>
        </button>
      </li>

      <li>
        <button
          onclick={() => handleAction(onToggleSpecies)}
          class="text-sm w-full text-left"
          role="menuitem"
        >
          <div class="flex items-center gap-2">
            {#if isExcluded}
              <Eye class="size-4" />
            {:else}
              <EyeOff class="size-4" />
            {/if}
            <span>{isExcluded ? 'Show species' : 'Ignore species'}</span>
          </div>
        </button>
      </li>

      <li>
        <button
          onclick={() => handleAction(onToggleLock)}
          class="text-sm w-full text-left"
          role="menuitem"
        >
          <div class="flex items-center gap-2">
            {#if detection.locked}
              <Lock class="size-4" />
            {:else}
              <LockOpen class="size-4" />
            {/if}
            <span>{detection.locked ? 'Unlock detection' : 'Lock detection'}</span>
          </div>
        </button>
      </li>

      {#if !detection.locked}
        <li>
          <button
            onclick={() => handleAction(onDelete)}
            class="text-sm w-full text-left text-error"
            role="menuitem"
          >
            <div class="flex items-center gap-2">
              <Trash2 class="size-4" />
              <span>Delete detection</span>
            </div>
          </button>
        </li>
      {/if}
    </ul>
  {/if}
</div>

<style>
  .menu {
    animation: fadeIn 0.2s ease-out;

    /* Ensure menu is always on top - fallback for CSS-only scenarios */
    z-index: 9999 !important;
  }

  @keyframes fadeIn {
    from {
      opacity: 0;
      transform: scale(0.95);
    }

    to {
      opacity: 1;
      transform: scale(1);
    }
  }
</style>

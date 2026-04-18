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
  import type { HTMLAttributes } from 'svelte/elements';
  import {
    MoreVertical,
    SquarePen,
    Eye,
    EyeOff,
    Lock,
    LockOpen,
    Trash2,
    Download,
    CircleCheck,
    CircleX,
  } from '@lucide/svelte';
  import { dropdown } from '$lib/utils/transitions';
  import { auth } from '$lib/stores/auth';
  import { t } from '$lib/i18n';

  let canEdit = $derived(!$auth.security.enabled || $auth.security.accessAllowed);

  interface Props extends HTMLAttributes<HTMLDivElement> {
    /** The detection object containing data for this action menu */
    detection: Detection;
    /** Whether the species is currently excluded from detection */
    isExcluded?: boolean;
    /** Callback fired when user marks the detection as correct */
    onMarkCorrect?: () => void;
    /** Callback fired when user marks the detection as false positive */
    onMarkFalsePositive?: () => void;
    /** Callback fired when user clicks review action */
    onReview?: () => void;
    /** Callback fired when user toggles species visibility */
    onToggleSpecies?: () => void;
    /** Callback fired when user toggles detection lock status */
    onToggleLock?: () => void;
    /** Callback fired when user deletes the detection */
    onDelete?: () => void;
    /** Callback fired when user downloads the detection audio */
    onDownload?: () => void;
    /** Additional CSS classes to apply to the component */
    className?: string;
    /** Visual variant - `default` for in-row use, `overlay` for spectrogram overlay */
    variant?: 'default' | 'overlay';
    /** Callback fired when menu opens */
    onMenuOpen?: () => void;
    /** Callback fired when menu closes */
    onMenuClose?: () => void;
  }

  let {
    detection,
    isExcluded = false,
    onMarkCorrect,
    onMarkFalsePositive,
    onReview,
    onToggleSpecies,
    onToggleLock,
    onDelete,
    onDownload,
    className = '',
    variant = 'default',
    onMenuOpen,
    onMenuClose,
    ...rest
  }: Props = $props();

  let isOpen = $state(false);
  // svelte-ignore non_reactive_update
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
    buttonElement?.focus();
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

{#if canEdit || onDownload}
  <div {...rest} class={cn('relative', className)}>
    <button
      bind:this={buttonElement}
      onclick={handleOpen}
      class={cn(
        'am-trigger inline-flex items-center justify-center w-8 h-8 p-1 transition-colors',
        variant === 'overlay'
          ? 'am-trigger-overlay text-white bg-black/50 hover:bg-slate-700/80 backdrop-blur-sm rounded-full'
          : 'am-trigger-default text-[var(--color-base-content)] hover:bg-[var(--color-base-200)] rounded-md'
      )}
      aria-label="Actions menu"
      aria-haspopup="true"
      aria-expanded={isOpen}
    >
      <MoreVertical class="size-5" />
    </button>

    {#if isOpen}
      {@const itemHoverClass =
        variant === 'overlay' ? 'hover:bg-white/10' : 'hover:bg-[var(--color-base-200)]'}
      <ul
        bind:this={menuElement}
        in:dropdown
        out:dropdown={{ duration: 100 }}
        class={cn(
          'fixed z-[1100] p-2 shadow-lg rounded-lg w-52 border',
          variant === 'overlay'
            ? 'bg-[var(--color-base-100)]/95 border-[var(--color-base-300)]/60 backdrop-blur-md'
            : 'bg-[var(--color-base-100)] border-[var(--color-base-300)]'
        )}
        role="menu"
      >
        {#if canEdit && !detection.locked && (onMarkCorrect || onMarkFalsePositive)}
          {#if onMarkCorrect}
            <li>
              <button
                onclick={() => handleAction(onMarkCorrect)}
                class={cn(
                  'text-sm w-full text-left px-3 py-2 rounded-md transition-colors',
                  itemHoverClass
                )}
                role="menuitem"
              >
                <div class="flex items-center gap-2">
                  <CircleCheck class="size-4 text-[var(--color-success)]" />
                  <span>{t('dashboard.recentDetections.actions.markCorrect')}</span>
                  {#if detection.verified === 'correct'}
                    <span
                      class="ml-auto inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-[var(--color-success)]/15 text-[var(--color-success)]"
                      >✓</span
                    >
                  {/if}
                </div>
              </button>
            </li>
          {/if}
          {#if onMarkFalsePositive}
            <li>
              <button
                onclick={() => handleAction(onMarkFalsePositive)}
                class={cn(
                  'text-sm w-full text-left px-3 py-2 rounded-md transition-colors',
                  itemHoverClass
                )}
                role="menuitem"
              >
                <div class="flex items-center gap-2">
                  <CircleX class="size-4 text-[var(--color-error)]" />
                  <span>{t('dashboard.recentDetections.actions.markFalsePositive')}</span>
                  {#if detection.verified === 'false_positive'}
                    <span
                      class="ml-auto inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-[var(--color-error)]/15 text-[var(--color-error)]"
                      >✗</span
                    >
                  {/if}
                </div>
              </button>
            </li>
          {/if}
          <li role="separator" class="my-1 h-px bg-[var(--color-base-300)]"></li>
        {/if}

        {#if canEdit && onReview}
          <li>
            <button
              onclick={() => handleAction(onReview)}
              class={cn(
                'text-sm w-full text-left px-3 py-2 rounded-md transition-colors',
                itemHoverClass
              )}
              role="menuitem"
            >
              <div class="flex items-center gap-2">
                <SquarePen class="size-4" />
                <span>{t('dashboard.recentDetections.actions.review')}</span>
                {#if detection.verified === 'correct'}
                  <span
                    class="ml-auto inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-[var(--color-success)]/15 text-[var(--color-success)]"
                    >✓</span
                  >
                {:else if detection.verified === 'false_positive'}
                  <span
                    class="ml-auto inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-[var(--color-error)]/15 text-[var(--color-error)]"
                    >✗</span
                  >
                {/if}
              </div>
            </button>
          </li>
        {/if}

        {#if canEdit && onToggleSpecies}
          <li>
            <button
              onclick={() => handleAction(onToggleSpecies)}
              class={cn(
                'text-sm w-full text-left px-3 py-2 rounded-md transition-colors',
                itemHoverClass
              )}
              role="menuitem"
            >
              <div class="flex items-center gap-2">
                {#if isExcluded}
                  <Eye class="size-4" />
                {:else}
                  <EyeOff class="size-4" />
                {/if}
                {#if isExcluded}
                  <span>{t('dashboard.recentDetections.actions.showSpecies')}</span>
                {:else}
                  <span>{t('dashboard.recentDetections.actions.ignoreSpecies')}</span>
                {/if}
              </div>
            </button>
          </li>
        {/if}

        {#if canEdit && onToggleLock}
          <li>
            <button
              onclick={() => handleAction(onToggleLock)}
              class={cn(
                'text-sm w-full text-left px-3 py-2 rounded-md transition-colors',
                itemHoverClass
              )}
              role="menuitem"
            >
              <div class="flex items-center gap-2">
                {#if detection.locked}
                  <Lock class="size-4" />
                {:else}
                  <LockOpen class="size-4" />
                {/if}
                {#if detection.locked}
                  <span>{t('dashboard.recentDetections.actions.unlockDetection')}</span>
                {:else}
                  <span>{t('dashboard.recentDetections.actions.lockDetection')}</span>
                {/if}
              </div>
            </button>
          </li>
        {/if}

        {#if onDownload}
          <li>
            <button
              onclick={() => handleAction(onDownload)}
              class={cn(
                'text-sm w-full text-left px-3 py-2 rounded-md transition-colors',
                itemHoverClass
              )}
              role="menuitem"
            >
              <div class="flex items-center gap-2">
                <Download class="size-4" />
                <span>{t('media.audio.download')}</span>
              </div>
            </button>
          </li>
        {/if}

        {#if canEdit && !detection.locked && onDelete}
          <li role="separator" class="my-1 h-px bg-[var(--color-base-300)]"></li>
          <li>
            <button
              onclick={() => handleAction(onDelete)}
              class={cn(
                'text-sm w-full text-left px-3 py-2 rounded-md text-[var(--color-error)] transition-colors',
                variant === 'overlay'
                  ? 'hover:bg-[var(--color-error)]/20'
                  : 'hover:bg-[var(--color-error)]/10'
              )}
              role="menuitem"
            >
              <div class="flex items-center gap-2">
                <Trash2 class="size-4" />
                <span>{t('dashboard.recentDetections.actions.deleteDetection')}</span>
              </div>
            </button>
          </li>
        {/if}
      </ul>
    {/if}
  </div>
{/if}

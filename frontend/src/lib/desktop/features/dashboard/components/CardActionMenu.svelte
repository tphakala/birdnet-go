<!--
  CardActionMenu.svelte

  A dropdown action menu styled for overlay on dark spectrogram cards.
  Features a 3-dot vertical trigger button with semi-transparent background.

  Props:
  - detection: Detection - The detection object
  - onReview?: () => void - Review action callback
  - onToggleSpecies?: () => void - Toggle species action callback
  - onToggleLock?: () => void - Toggle lock action callback
  - onDelete?: () => void - Delete action callback
  - onDownload?: () => void - Download audio action callback
  - onMenuOpen?: () => void - Menu open callback
  - onMenuClose?: () => void - Menu close callback
-->
<script lang="ts">
  import type { Detection } from '$lib/types/detection.types';
  import {
    MoreVertical,
    SquarePen,
    Eye,
    EyeOff,
    Lock,
    LockOpen,
    Trash2,
    Download,
  } from '@lucide/svelte';
  import { t } from '$lib/i18n';

  interface Props {
    detection: Detection;
    isExcluded?: boolean;
    onReview?: () => void;
    onToggleSpecies?: () => void;
    onToggleLock?: () => void;
    onDelete?: () => void;
    onDownload?: () => void;
    onMenuOpen?: () => void;
    onMenuClose?: () => void;
  }

  let {
    detection,
    isExcluded = false,
    onReview,
    onToggleSpecies,
    onToggleLock,
    onDelete,
    onDownload,
    onMenuOpen,
    onMenuClose,
  }: Props = $props();

  let isOpen = $state(false);
  let buttonElement: HTMLButtonElement;
  // svelte-ignore non_reactive_update
  let menuElement: HTMLUListElement;

  function updateMenuPosition() {
    if (!menuElement || !buttonElement) return;

    const buttonRect = buttonElement.getBoundingClientRect();
    const spaceBelow = window.innerHeight - buttonRect.bottom;
    const spaceAbove = buttonRect.top;
    const menuHeight = menuElement.offsetHeight;

    menuElement.style.position = 'fixed';
    menuElement.style.zIndex = '9999';

    if (spaceBelow < menuHeight && spaceAbove > spaceBelow) {
      menuElement.style.bottom = `${window.innerHeight - buttonRect.top + 8}px`;
      menuElement.style.top = 'auto';
    } else {
      menuElement.style.top = `${buttonRect.bottom + 8}px`;
      menuElement.style.bottom = 'auto';
    }

    menuElement.style.left = 'auto';
    menuElement.style.right = `${window.innerWidth - buttonRect.right}px`;
  }

  function handleOpen(event: MouseEvent) {
    event.stopPropagation();
    isOpen = !isOpen;

    if (isOpen) {
      onMenuOpen?.();
      globalThis.requestAnimationFrame(updateMenuPosition);
    } else {
      onMenuClose?.();
    }
  }

  function handleAction(action: (() => void) | undefined) {
    isOpen = false;
    onMenuClose?.();
    action?.();
  }

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

  function handleKeydown(event: KeyboardEvent) {
    if (isOpen && event.key === 'Escape') {
      isOpen = false;
      onMenuClose?.();
      buttonElement?.focus();
    }
  }

  $effect(() => {
    if (isOpen) {
      function handleResize() {
        updateMenuPosition();
      }

      document.addEventListener('click', handleClickOutside);
      document.addEventListener('keydown', handleKeydown);
      window.addEventListener('resize', handleResize);
      window.addEventListener('scroll', handleResize, true);

      return () => {
        document.removeEventListener('click', handleClickOutside);
        document.removeEventListener('keydown', handleKeydown);
        window.removeEventListener('resize', handleResize);
        window.removeEventListener('scroll', handleResize, true);
      };
    }
  });

  $effect(() => {
    return () => {
      if (isOpen && onMenuClose) {
        onMenuClose();
      }
    };
  });
</script>

<div>
  <button
    bind:this={buttonElement}
    onclick={handleOpen}
    class="menu-trigger"
    aria-label={t('dashboard.recentDetections.actions.menuLabel', {
      species: detection.commonName,
    })}
    aria-haspopup="true"
    aria-expanded={isOpen}
  >
    <MoreVertical class="size-5" />
  </button>

  {#if isOpen}
    <ul bind:this={menuElement} class="action-menu" role="menu">
      <li>
        <button onclick={() => handleAction(onReview)} class="menu-item" role="menuitem">
          <SquarePen class="size-4" />
          <span>{t('dashboard.recentDetections.actions.review')}</span>
          {#if detection.verified === 'correct'}
            <span class="badge badge-success badge-sm">✓</span>
          {:else if detection.verified === 'false_positive'}
            <span class="badge badge-error badge-sm">✗</span>
          {/if}
        </button>
      </li>

      <li>
        <button onclick={() => handleAction(onToggleSpecies)} class="menu-item" role="menuitem">
          {#if isExcluded}
            <Eye class="size-4" />
            <span>{t('dashboard.recentDetections.actions.showSpecies')}</span>
          {:else}
            <EyeOff class="size-4" />
            <span>{t('dashboard.recentDetections.actions.ignoreSpecies')}</span>
          {/if}
        </button>
      </li>

      <li>
        <button onclick={() => handleAction(onToggleLock)} class="menu-item" role="menuitem">
          {#if detection.locked}
            <Lock class="size-4" />
            <span>{t('dashboard.recentDetections.actions.unlockDetection')}</span>
          {:else}
            <LockOpen class="size-4" />
            <span>{t('dashboard.recentDetections.actions.lockDetection')}</span>
          {/if}
        </button>
      </li>

      <li>
        <button onclick={() => handleAction(onDownload)} class="menu-item" role="menuitem">
          <Download class="size-4" />
          <span>{t('media.audio.download')}</span>
        </button>
      </li>

      {#if !detection.locked}
        <li class="menu-separator"></li>
        <li>
          <button
            onclick={() => handleAction(onDelete)}
            class="menu-item delete-item"
            role="menuitem"
          >
            <Trash2 class="size-4" />
            <span>{t('dashboard.recentDetections.actions.deleteDetection')}</span>
          </button>
        </li>
      {/if}
    </ul>
  {/if}
</div>

<style>
  .menu-trigger {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 2rem;
    height: 2rem;
    border-radius: 9999px;
    background-color: rgb(0 0 0 / 0.5);
    backdrop-filter: blur(4px);
    color: white;
    transition: background-color 0.15s ease;
  }

  .menu-trigger:hover {
    background-color: rgb(51 65 85 / 0.8);
  }

  .action-menu {
    position: fixed;
    min-width: 13rem;
    padding: 0.5rem;
    background-color: rgb(30 41 59);
    border: 1px solid rgb(51 65 85);
    border-radius: 0.5rem;
    box-shadow: 0 10px 25px rgb(0 0 0 / 0.4);
    animation: menuFadeIn 0.15s ease-out;
    z-index: 9999 !important;
  }

  /* Light theme menu */
  :global([data-theme='light']) .action-menu {
    background-color: var(--color-base-100);
    border-color: var(--color-base-300);
    box-shadow: 0 10px 25px rgb(0 0 0 / 0.15);
  }

  .menu-item {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    width: 100%;
    padding: 0.5rem 0.75rem;
    border-radius: 0.375rem;
    font-size: 0.875rem;
    color: rgb(226 232 240);
    text-align: left;
    transition: background-color 0.1s ease;
  }

  :global([data-theme='light']) .menu-item {
    color: var(--color-base-content);
  }

  .menu-item:hover {
    background-color: rgb(51 65 85 / 0.5);
  }

  :global([data-theme='light']) .menu-item:hover {
    background-color: var(--color-base-200);
  }

  .delete-item:hover {
    background-color: rgb(239 68 68 / 0.15);
  }

  :global([data-theme='light']) .delete-item:hover {
    background-color: rgb(239 68 68 / 0.1);
  }

  .menu-separator {
    height: 1px;
    margin: 0.25rem 0;
    background-color: rgb(51 65 85);
  }

  :global([data-theme='light']) .menu-separator {
    background-color: var(--color-base-300);
  }

  @keyframes menuFadeIn {
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

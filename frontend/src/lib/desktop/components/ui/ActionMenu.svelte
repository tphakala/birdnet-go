<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { onMount } from 'svelte';
  import type { Detection } from '$lib/types/detection.types';
  import { actionIcons, systemIcons } from '$lib/utils/icons';

  interface Props {
    detection: Detection;
    isExcluded?: boolean;
    onReview?: () => void;
    onToggleSpecies?: () => void;
    onToggleLock?: () => void;
    onDelete?: () => void;
    className?: string;
  }

  let {
    detection,
    isExcluded = false,
    onReview,
    onToggleSpecies,
    onToggleLock,
    onDelete,
    className = '',
  }: Props = $props();

  let isOpen = $state(false);
  let buttonElement: HTMLButtonElement;
  // svelte-ignore non_reactive_update
  let menuElement: HTMLUListElement;

  // Update menu position when opened
  function updateMenuPosition() {
    if (!menuElement || !buttonElement) return;

    const buttonRect = buttonElement.getBoundingClientRect();
    const spaceBelow = window.innerHeight - buttonRect.bottom;
    const spaceAbove = buttonRect.top;
    const menuHeight = menuElement.offsetHeight;

    // Position menu relative to viewport
    menuElement.style.position = 'fixed';
    menuElement.style.zIndex = '50';

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

  function handleOpen() {
    isOpen = true;
    requestAnimationFrame(updateMenuPosition);
  }

  function handleAction(action: () => void | undefined) {
    isOpen = false;
    if (action) {
      action();
    }
  }

  // Close menu when clicking outside
  function handleClickOutside(event: MouseEvent) {
    if (
      isOpen &&
      menuElement &&
      !menuElement.contains(event.target as Node) &&
      buttonElement &&
      !buttonElement.contains(event.target as Node)
    ) {
      isOpen = false;
    }
  }

  // Close menu on escape key
  function handleKeydown(event: KeyboardEvent) {
    if (isOpen && event.key === 'Escape') {
      isOpen = false;
      buttonElement?.focus();
    }
  }

  onMount(() => {
    document.addEventListener('click', handleClickOutside);
    document.addEventListener('keydown', handleKeydown);

    return () => {
      document.removeEventListener('click', handleClickOutside);
      document.removeEventListener('keydown', handleKeydown);
    };
  });
</script>

<div class={cn('dropdown relative', className)}>
  <button
    bind:this={buttonElement}
    onclick={handleOpen}
    class="btn btn-ghost btn-xs"
    aria-label="Actions menu"
    aria-haspopup="true"
    aria-expanded={isOpen}
  >
    {@html actionIcons.more}
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
            {@html actionIcons.edit}
            <span>Review detection</span>
            {#if detection.review?.verified === 'correct'}
              <span class="badge badge-success badge-sm">✓</span>
            {:else if detection.review?.verified === 'false_positive'}
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
              {@html systemIcons.eye}
            {:else}
              {@html systemIcons.eyeOff}
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
              {@html actionIcons.lock}
            {:else}
              {@html actionIcons.unlock}
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
              {@html actionIcons.delete}
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

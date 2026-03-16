<!--
  ResizableContainer.svelte

  Purpose: A wrapper that adds a drag-to-resize handle at the bottom of a scrollable container.
  Users can drag the handle to expand or shrink the container height.

  Props:
  - minHeight: Minimum height in pixels (default: 200)
  - maxHeight: Maximum height in pixels (default: 800)
  - defaultHeight: Initial height in pixels (default: 448, ~28rem)
  - className: Additional CSS classes for the scrollable container
  - children: Content snippet

  @component
-->
<script lang="ts">
  import { GripHorizontal } from '@lucide/svelte';
  import { cn } from '$lib/utils/cn';
  import type { Snippet } from 'svelte';
  import { t } from '$lib/i18n';

  interface Props {
    minHeight?: number;
    maxHeight?: number;
    defaultHeight?: number;
    className?: string;
    children?: Snippet;
  }

  let {
    minHeight = 200,
    maxHeight = 800,
    defaultHeight = 448,
    className = '',
    children,
  }: Props = $props();

  let height = $state(0);

  // Initialize height from prop once on mount
  $effect(() => {
    if (height === 0) {
      height = defaultHeight;
    }
  });
  let isDragging = $state(false);
  let startY = 0;
  let startHeight = 0;

  function onPointerDown(e: globalThis.PointerEvent) {
    isDragging = true;
    startY = e.clientY;
    startHeight = height;
    (e.target as HTMLElement).setPointerCapture(e.pointerId);
  }

  function onPointerMove(e: globalThis.PointerEvent) {
    if (!isDragging) return;
    const delta = e.clientY - startY;
    height = Math.min(maxHeight, Math.max(minHeight, startHeight + delta));
  }

  function onPointerUp() {
    isDragging = false;
  }

  const KEYBOARD_RESIZE_STEP = 24;

  function onKeyDown(event: KeyboardEvent) {
    switch (event.key) {
      case 'ArrowUp':
        event.preventDefault();
        height = Math.max(minHeight, height - KEYBOARD_RESIZE_STEP);
        break;
      case 'ArrowDown':
        event.preventDefault();
        height = Math.min(maxHeight, height + KEYBOARD_RESIZE_STEP);
        break;
      case 'Home':
        event.preventDefault();
        height = minHeight;
        break;
      case 'End':
        event.preventDefault();
        height = maxHeight;
        break;
    }
  }
</script>

<div class="flex flex-col">
  <!-- Scrollable content -->
  <div class={cn('overflow-y-auto overflow-x-auto', className)} style:height="{height}px">
    {#if children}
      {@render children()}
    {/if}
  </div>

  <!-- Drag handle: focusable separator per WAI-ARIA (has aria-valuenow) -->
  <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
  <div
    role="separator"
    aria-orientation="horizontal"
    aria-label={t('common.aria.resizeHandle')}
    aria-valuenow={height}
    aria-valuemin={minHeight}
    aria-valuemax={maxHeight}
    tabindex={0}
    class={cn(
      'flex items-center justify-center h-5 cursor-row-resize select-none',
      'border-t border-[var(--border-100)]',
      'text-[var(--color-base-content)]/30 hover:text-[var(--color-base-content)]/60',
      'hover:bg-[var(--color-base-200)]/50 transition-colors',
      isDragging && 'text-[var(--color-primary)] bg-[var(--color-base-200)]/50'
    )}
    onpointerdown={onPointerDown}
    onpointermove={onPointerMove}
    onpointerup={onPointerUp}
    onpointercancel={onPointerUp}
    onkeydown={onKeyDown}
  >
    <GripHorizontal class="size-4" />
  </div>
</div>

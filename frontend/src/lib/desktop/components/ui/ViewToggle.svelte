<!--
  ViewToggle.svelte

  A reusable toggle for switching between table (list) and card (grid) views.

  Props:
  - view: 'table' | 'cards' - Current view mode
  - onViewChange: (view: 'table' | 'cards') => void - View change callback
  - className?: string - Additional CSS classes
-->
<script lang="ts">
  import { LayoutList, LayoutGrid } from '@lucide/svelte';
  import { cn } from '$lib/utils/cn';
  import { t } from '$lib/i18n';

  interface Props {
    view: 'table' | 'cards';
    onViewChange: (_view: 'table' | 'cards') => void;
    className?: string;
  }

  let { view, onViewChange, className = '' }: Props = $props();
</script>

<div
  class={cn('view-toggle', className)}
  role="radiogroup"
  aria-label={t('detections.viewToggle.label')}
>
  <button
    type="button"
    class={cn('view-toggle-btn', view === 'table' && 'view-toggle-btn-active')}
    role="radio"
    aria-checked={view === 'table'}
    aria-label={t('detections.viewToggle.table')}
    onclick={() => onViewChange('table')}
  >
    <LayoutList class="size-4" />
  </button>
  <button
    type="button"
    class={cn('view-toggle-btn', view === 'cards' && 'view-toggle-btn-active')}
    role="radio"
    aria-checked={view === 'cards'}
    aria-label={t('detections.viewToggle.cards')}
    onclick={() => onViewChange('cards')}
  >
    <LayoutGrid class="size-4" />
  </button>
</div>

<style>
  .view-toggle {
    display: inline-flex;
    border-radius: 0.5rem;
    border: 1px solid oklch(var(--b3));
    overflow: hidden;
  }

  .view-toggle-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 0.375rem 0.625rem;
    background: transparent;
    border: none;
    cursor: pointer;
    color: oklch(var(--bc) / 0.5);
    transition: all 150ms ease;
  }

  .view-toggle-btn:hover:not(.view-toggle-btn-active) {
    color: oklch(var(--bc) / 0.75);
    background-color: oklch(var(--b2) / 0.5);
  }

  .view-toggle-btn-active {
    background-color: oklch(var(--p));
    color: oklch(var(--pc));
  }

  .view-toggle-btn + .view-toggle-btn {
    border-left: 1px solid oklch(var(--b3));
  }

  .view-toggle-btn-active + .view-toggle-btn,
  .view-toggle-btn + .view-toggle-btn-active {
    border-left-color: oklch(var(--p));
  }
</style>

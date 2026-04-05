<!--
  EditorCard - Inline card editor container matching AlertRuleEditor design.

  Features:
  - Primary-colored border to visually distinguish the editor
  - Header bar with title and close button
  - Body area with consistent padding
  - Optional footer snippet for action buttons

  @component
-->
<script lang="ts">
  import { X } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import type { Snippet } from 'svelte';

  interface Props {
    title: string;
    onClose: () => void;
    children: Snippet;
    footer?: Snippet;
  }

  let { title, onClose, children, footer }: Props = $props();
</script>

<div
  class="rounded-lg bg-[var(--color-base-100)] border border-[var(--color-primary)] overflow-hidden"
>
  <!-- Header bar -->
  <div class="px-5 py-3 border-b border-[var(--color-base-300)] flex items-center justify-between">
    <h3 class="text-sm font-semibold text-[var(--color-base-content)]">
      {title}
    </h3>
    <button
      type="button"
      class="w-7 h-7 rounded-md flex items-center justify-center hover:bg-[var(--color-base-200)] transition-colors cursor-pointer"
      aria-label={t('common.close')}
      onclick={onClose}
    >
      <X class="w-4 h-4 text-[var(--color-base-content)]/60" />
    </button>
  </div>

  <!-- Body -->
  <div class="p-5 space-y-4">
    {@render children()}
  </div>

  <!-- Footer -->
  {#if footer}
    <div class="px-5 pb-5">
      {@render footer()}
    </div>
  {/if}
</div>

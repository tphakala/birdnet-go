<script lang="ts">
  interface Props {
    id: string;
    label: string;
    isCollapsed: boolean;
  }

  let { id, label, isCollapsed }: Props = $props();
</script>

<div
  {id}
  class={isCollapsed
    ? 'sr-only'
    : 'px-3 py-1.5 text-xs font-semibold uppercase tracking-wide text-[var(--color-base-content)]/50 select-none'}
>
  {label}
</div>

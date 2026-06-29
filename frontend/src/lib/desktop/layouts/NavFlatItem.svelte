<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Component } from 'svelte';

  export interface NavFlatItemProps {
    icon: Component;
    label: string;
    url: string;
    active: boolean;
    isCollapsed: boolean;
    onNavigate: (_url: string) => void;
    showTooltip: (_event: MouseEvent | FocusEvent, _text: string) => void;
    hideTooltip: () => void;
    ariaLabel?: string;
  }

  let {
    icon: Icon,
    label,
    url,
    active,
    isCollapsed,
    onNavigate,
    showTooltip,
    hideTooltip,
    ariaLabel,
  }: NavFlatItemProps = $props();

  const menuItemBase =
    'flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm font-medium transition-all duration-150 w-full text-left';
  const menuItemDefault =
    'text-[var(--color-base-content)]/80 hover:text-[var(--color-base-content)] hover:menu-hover';
  const menuItemActive = 'menu-item-active';
  const focusRingClasses =
    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-offset-1 focus-visible:ring-offset-[var(--color-base-100)]';

  let computedAriaLabel = $derived(ariaLabel ?? label);

  let tooltipText = $derived(label);
</script>

<button
  type="button"
  onclick={() => onNavigate(url)}
  onmouseenter={e => isCollapsed && showTooltip(e, tooltipText)}
  onmouseleave={hideTooltip}
  onfocus={e => isCollapsed && showTooltip(e, tooltipText)}
  onblur={hideTooltip}
  aria-label={computedAriaLabel}
  aria-current={active ? 'page' : undefined}
  class={cn(
    menuItemBase,
    isCollapsed ? 'justify-center px-0' : '',
    active ? menuItemActive : menuItemDefault,
    focusRingClasses
  )}
>
  <Icon class="size-5 shrink-0" />
  {#if !isCollapsed}
    <span>{label}</span>
  {/if}
</button>

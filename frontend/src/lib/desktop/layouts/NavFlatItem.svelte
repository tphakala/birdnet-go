<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { t } from '$lib/i18n';
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
    comingSoon?: boolean;
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
    comingSoon = false,
    ariaLabel,
  }: NavFlatItemProps = $props();

  const menuItemBase =
    'flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm font-medium transition-all duration-150 w-full text-left';
  const menuItemDefault =
    'text-[var(--color-base-content)]/80 hover:text-[var(--color-base-content)] hover:menu-hover';
  const menuItemActive = 'menu-item-active';
  const focusRingClasses =
    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-offset-1 focus-visible:ring-offset-[var(--color-base-100)]';

  let badgeText = $derived(t('analytics.comingSoon.badge'));

  let computedAriaLabel = $derived(ariaLabel ?? (comingSoon ? `${label} (${badgeText})` : label));

  let tooltipText = $derived(comingSoon ? `${label} (${badgeText})` : label);
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
    {#if comingSoon}
      <span class="badge badge-primary badge-sm ml-auto">{badgeText}</span>
    {/if}
  {/if}
</button>

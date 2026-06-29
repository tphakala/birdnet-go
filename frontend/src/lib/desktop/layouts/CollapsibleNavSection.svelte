<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { ChevronDown } from '@lucide/svelte';
  import { flyout } from '$lib/utils/transitions';
  import type { Component } from 'svelte';

  export interface NavButtonItem {
    type?: 'button';
    icon: Component;
    label: string;
    url: string;
    routeKey: string;
  }

  export interface NavLinkItem {
    type: 'link';
    icon: Component;
    label: string;
    href: string;
    ariaLabel?: string;
    trailingIcon?: Component;
  }

  export type NavItem = NavButtonItem | NavLinkItem;

  interface Props {
    icon: Component;
    label: string;
    ariaLabel?: string;
    items: NavItem[];
    isCollapsed: boolean;
    expanded: boolean;
    routeActive: boolean;
    routeCache: Record<string, boolean>;
    onToggleExpanded: () => void;
    onNavigate: (_url: string) => void;
    showTooltip: (_event: MouseEvent, _text: string) => void;
    hideTooltip: () => void;
    activeFlyout: string | null;
    sectionId: string;
    onToggleFlyout: (_sectionId: string) => void;
  }

  let {
    icon: Icon,
    label,
    ariaLabel,
    items,
    isCollapsed,
    expanded,
    routeActive,
    routeCache,
    onToggleExpanded,
    onNavigate,
    showTooltip,
    hideTooltip,
    activeFlyout,
    sectionId,
    onToggleFlyout,
  }: Props = $props();

  let buttonRef = $state<HTMLButtonElement | null>(null);
  let flyoutPosition = $state({ top: 0, left: 0 });

  let flyoutOpen = $derived(activeFlyout === sectionId);

  function handleToggleFlyout() {
    hideTooltip();
    if (!flyoutOpen && buttonRef) {
      const rect = buttonRef.getBoundingClientRect();
      flyoutPosition = {
        top: rect.top,
        left: rect.right + 8,
      };
    }
    onToggleFlyout(sectionId);
  }

  function handleWindowKeydown(event: KeyboardEvent) {
    if (!flyoutOpen) return;
    if (event.key === 'Escape') {
      event.preventDefault();
      onToggleFlyout(sectionId);
      buttonRef?.focus();
    }
  }

  const menuItemBase =
    'flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm font-medium transition-all duration-150 w-full text-left';
  const menuItemCollapsed = 'justify-center px-0';
  const subitemClass =
    'flex items-center gap-2 w-full px-3 py-2 rounded-md text-sm transition-colors duration-150';
</script>

<svelte:window onkeydown={handleWindowKeydown} />

<div class="flex flex-col relative flyout-container">
  {#if isCollapsed}
    <!-- Collapsed: Icon with flyout -->
    <div class="relative">
      <button
        bind:this={buttonRef}
        onclick={handleToggleFlyout}
        onmouseenter={e => !flyoutOpen && showTooltip(e, label)}
        onmouseleave={hideTooltip}
        class={cn(
          menuItemBase,
          menuItemCollapsed,
          routeActive ? 'text-[var(--color-primary)]' : 'text-[var(--color-base-content)]/80',
          'hover:text-[var(--color-base-content)] hover:menu-hover',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-offset-1 focus-visible:ring-offset-[var(--color-base-100)]'
        )}
        aria-expanded={flyoutOpen}
        aria-label={ariaLabel ?? label}
      >
        <Icon class="size-5 shrink-0" />
      </button>
    </div>
    <!-- Flyout submenu (fixed positioning to escape overflow container) -->
    {#if flyoutOpen}
      <div
        in:flyout
        out:flyout={{ duration: 100 }}
        class="fixed bg-[var(--color-base-100)] border border-[var(--color-base-200)] rounded-lg shadow-xl min-w-48 z-[100]"
        style:top="{flyoutPosition.top}px"
        style:left="{flyoutPosition.left}px"
      >
        <div
          class="px-3 py-2 border-b border-[var(--color-base-200)] font-medium text-sm text-[var(--color-base-content)]"
        >
          {label}
        </div>
        <div class="p-1 max-h-[calc(100vh-8rem)] overflow-y-auto">
          {#each items as item (item.type === 'link' ? item.label : item.routeKey)}
            {#if item.type === 'link'}
              <a
                href={item.href}
                target="_blank"
                rel="noopener noreferrer"
                class={cn(
                  subitemClass,
                  'text-[var(--color-base-content)]/80 hover:text-[var(--color-base-content)] hover:menu-hover',
                  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-offset-1 focus-visible:ring-offset-[var(--color-base-100)]'
                )}
                aria-label={item.ariaLabel}
              >
                <item.icon class="size-4 shrink-0" />
                {item.label}
                {#if item.trailingIcon}
                  <item.trailingIcon class="size-3 opacity-40 ml-auto" />
                {/if}
              </a>
            {:else}
              <button
                onclick={() => onNavigate(item.url)}
                aria-current={routeCache[item.routeKey] ? 'page' : undefined}
                class={cn(
                  subitemClass,
                  routeCache[item.routeKey]
                    ? 'menu-subitem-active'
                    : 'text-[var(--color-base-content)]/80 hover:text-[var(--color-base-content)] hover:menu-hover',
                  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-offset-1 focus-visible:ring-offset-[var(--color-base-100)]'
                )}
              >
                <item.icon class="size-4 shrink-0" />{item.label}
              </button>
            {/if}
          {/each}
        </div>
      </div>
    {/if}
  {:else}
    <!-- Expanded: Regular collapsible -->
    <button
      onclick={onToggleExpanded}
      class={cn(
        menuItemBase,
        routeActive ? 'text-[var(--color-primary)]' : 'text-[var(--color-base-content)]/80',
        'hover:text-[var(--color-base-content)] hover:menu-hover',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-offset-1 focus-visible:ring-offset-[var(--color-base-100)]'
      )}
      aria-expanded={expanded}
    >
      <Icon class="size-5 shrink-0" />
      <span class="flex-1">{label}</span>
      <ChevronDown
        class={cn('size-4 shrink-0 transition-transform duration-200', {
          'rotate-180': expanded,
        })}
      />
    </button>

    {#if expanded}
      <div
        class="ml-4 pl-4 border-l-2 mt-1 flex flex-col gap-0.5"
        style:border-color="color-mix(in oklch, var(--color-primary) 30%, transparent)"
      >
        {#each items as item (item.type === 'link' ? item.label : item.routeKey)}
          {#if item.type === 'link'}
            <a
              href={item.href}
              target="_blank"
              rel="noopener noreferrer"
              class={cn(
                subitemClass,
                'text-[var(--color-base-content)]/80 hover:text-[var(--color-base-content)] hover:menu-hover',
                'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-offset-1 focus-visible:ring-offset-[var(--color-base-100)]'
              )}
              aria-label={item.ariaLabel}
            >
              <item.icon class="size-4 shrink-0" />
              {item.label}
              {#if item.trailingIcon}
                <item.trailingIcon class="size-3 opacity-40 ml-auto" />
              {/if}
            </a>
          {:else}
            <button
              onclick={() => onNavigate(item.url)}
              aria-current={routeCache[item.routeKey] ? 'page' : undefined}
              class={cn(
                subitemClass,
                routeCache[item.routeKey]
                  ? 'menu-subitem-active'
                  : 'text-[var(--color-base-content)]/80 hover:text-[var(--color-base-content)] hover:menu-hover',
                'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-offset-1 focus-visible:ring-offset-[var(--color-base-100)]'
              )}
            >
              <item.icon class="size-4 shrink-0" />{item.label}
            </button>
          {/if}
        {/each}
      </div>
    {/if}
  {/if}
</div>

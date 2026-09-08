<!--
  Settings Tabs Component

  Purpose: A reusable tabbed navigation component for settings pages that provides
  consistent styling, keyboard navigation, change indicators, and accessibility.

  Features:
  - Underline-style tabs with colored indicator for active tab
  - Icon + label display for each tab
  - Per-tab change indicator badges
  - Full keyboard navigation (Arrow keys, Home, End)
  - ARIA compliance for screen readers
  - Lazy content rendering (only active tab content is mounted)
  - Integrated save/reset actions bar

  Props:
  - tabs: Array of tab definitions
  - activeTab: Currently active tab ID (bindable)
  - queryParam: URL parameter for this tab group (default: tab)
  - defaultTab: Fallback when the URL has no valid tab (defaults to initial activeTab)
  - onTabChange: Callback when tab changes
  - showActions: Whether to show the save/reset actions bar (default: true)
  - class: Additional CSS classes

  @component
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Snippet, Component } from 'svelte';
  import { untrack } from 'svelte';
  import { navigation } from '$lib/stores/navigation.svelte';
  import type { IconProps } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import SettingsPageActions from './SettingsPageActions.svelte';

  export interface TabDefinition {
    id: string;
    label: string;
    icon?: Component<IconProps>;
    hasChanges?: boolean;
    content: Snippet;
  }

  interface Props {
    tabs: TabDefinition[];
    activeTab: string;
    queryParam?: string;
    defaultTab?: string;
    onTabChange?: (_tabId: string) => void;
    showActions?: boolean;
    class?: string;
  }

  let {
    tabs,
    activeTab = $bindable(),
    queryParam = 'tab',
    defaultTab,
    onTabChange,
    showActions = true,
    class: className,
  }: Props = $props();

  // Capture the page default once, so a missing URL value never reuses the
  // previous history entry's selection. Audio retains its legacy fallback.
  const initialTab = untrack(() => defaultTab ?? activeTab);
  const selectedTab = $derived.by(() => {
    const requested = new URLSearchParams(navigation.currentSearch).get(queryParam);
    return (
      tabs.find(tab => tab.id === requested)?.id ??
      tabs.find(tab => tab.id === initialTab)?.id ??
      tabs[0]?.id ??
      ''
    );
  });

  // Parents use the binding for tab-specific work (e.g. mounting the location map).
  $effect(() => {
    activeTab = selectedTab;
  });

  // Only explicit selection writes history; restoring a URL must not push entries.
  function selectTab(tabId: string) {
    const url = new URL(window.location.href);
    if (url.searchParams.get(queryParam) === tabId) return;
    url.searchParams.set(queryParam, tabId);
    navigation.navigate(url.pathname + url.search + url.hash);
    onTabChange?.(tabId);
  }

  // Safe array access using Array.prototype.at() to avoid object injection warnings
  function getTabAt(index: number): TabDefinition | undefined {
    if (index >= 0 && index < tabs.length) {
      return tabs.at(index);
    }
    return undefined;
  }

  // Keyboard navigation handler
  function handleKeyDown(event: KeyboardEvent, currentIndex: number) {
    const tabCount = tabs.length;
    let newIndex = currentIndex;

    switch (event.key) {
      case 'ArrowRight':
        event.preventDefault();
        newIndex = (currentIndex + 1) % tabCount;
        break;
      case 'ArrowLeft':
        event.preventDefault();
        newIndex = (currentIndex - 1 + tabCount) % tabCount;
        break;
      case 'Home':
        event.preventDefault();
        newIndex = 0;
        break;
      case 'End':
        event.preventDefault();
        newIndex = tabCount - 1;
        break;
      case 'Enter':
      case ' ': {
        event.preventDefault();
        const currentTab = getTabAt(currentIndex);
        if (currentTab) {
          selectTab(currentTab.id);
        }
        return;
      }
      default:
        return;
    }

    // Focus and select the new tab
    const newTab = getTabAt(newIndex);
    if (newTab) {
      selectTab(newTab.id);
      // Focus the tab button
      const tabButton = document.getElementById(`settings-tab-${newTab.id}`);
      tabButton?.focus();
    }
  }
</script>

<div class={cn('settings-tabs', className)}>
  <!-- Tab Navigation -->
  <div
    class="tabs border-b border-[var(--color-base-300)] mb-6"
    role="tablist"
    aria-label={t('settings.tabs.navigation')}
  >
    {#each tabs as tab, index (tab.id)}
      {@const isActive = selectedTab === tab.id}
      <button
        id="settings-tab-{tab.id}"
        type="button"
        role="tab"
        aria-label={tab.label}
        class={cn(
          'tab gap-2 transition-all duration-200 font-medium -mb-px',
          'hover:text-[color:var(--color-primary)]',
          isActive
            ? 'tab-active text-[var(--color-primary)] border-b-2 border-[var(--color-primary)]'
            : 'text-[var(--color-base-content)]/75 hover:text-[var(--color-base-content)]'
        )}
        aria-selected={isActive}
        aria-controls="settings-tabpanel-{tab.id}"
        tabindex={isActive ? 0 : -1}
        onclick={() => selectTab(tab.id)}
        onkeydown={e => handleKeyDown(e, index)}
      >
        <!-- Tab icon -->
        {#if tab.icon}
          {@const TabIcon = tab.icon}
          <TabIcon class="size-4" aria-hidden="true" />
        {/if}

        <!-- Tab label -->
        <span class="hidden sm:inline">{tab.label}</span>

        <!-- Change indicator -->
        {#if tab.hasChanges}
          <span
            class="badge badge-primary badge-xs"
            role="status"
            aria-label={t('settings.tabs.hasChanges')}
          >
          </span>
        {/if}
      </button>
    {/each}
  </div>

  <!-- Tab Panels -->
  {#each tabs as tab (tab.id)}
    {@const isActive = selectedTab === tab.id}
    <div
      id="settings-tabpanel-{tab.id}"
      role="tabpanel"
      aria-labelledby="settings-tab-{tab.id}"
      class="tab-panel"
      tabindex={isActive ? 0 : -1}
      hidden={!isActive}
    >
      {#if isActive}
        {@render tab.content()}
      {/if}
    </div>
  {/each}

  <!-- Integrated Save/Reset Actions -->
  {#if showActions}
    <SettingsPageActions />
  {/if}
</div>

<style>
  .settings-tabs {
    /* Configurable tab font size */
    --tab-font-size: 0.9rem;

    /* Tab styling for underline variant */
    & .tab {
      min-height: 2.5rem;
      padding-inline: 1rem;
      padding-block: 0.75rem;
      border-radius: 0;
      background: transparent;
      font-size: var(--tab-font-size);
    }

    /* Tab panel animation */
    & .tab-panel {
      animation: fadeIn 0.15s ease-out;
    }
  }

  /* Keyframes must be at top level — lightningcss (Vite 8 default CSS minifier)
     does not support @keyframes nested inside selector rule blocks */
  @keyframes fadeIn {
    from {
      opacity: 0;
      transform: translateY(4px);
    }

    to {
      opacity: 1;
      transform: translateY(0);
    }
  }

  /* Responsive: show only icons on very small screens */
  @media (max-width: 480px) {
    .settings-tabs .tab {
      padding-inline: 0.75rem;
    }
  }
</style>

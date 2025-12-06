<!--
  Settings Tabs Component

  Purpose: A reusable tabbed navigation component for settings pages that provides
  consistent styling, keyboard navigation, change indicators, and accessibility.

  Features:
  - DaisyUI 5 tabs styling with modern appearance
  - Icon + label display for each tab
  - Per-tab change indicator badges
  - Full keyboard navigation (Arrow keys, Home, End)
  - ARIA compliance for screen readers
  - Optional "default" star indicator for input source tabs
  - Lazy content rendering (only active tab content is mounted)

  Props:
  - tabs: Array of tab definitions
  - activeTab: Currently active tab ID (bindable)
  - onTabChange: Callback when tab changes
  - class: Additional CSS classes

  @component
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Snippet, Component } from 'svelte';
  import { Star } from '@lucide/svelte';
  import type { IconProps } from '@lucide/svelte';
  import { t } from '$lib/i18n';

  export interface TabDefinition {
    id: string;
    label: string;
    icon?: Component<IconProps>;
    hasChanges?: boolean;
    isDefault?: boolean;
    showDefaultStar?: boolean;
    content: Snippet;
  }

  interface Props {
    tabs: TabDefinition[];
    activeTab: string;
    onTabChange?: (_tabId: string) => void;
    class?: string;
  }

  let { tabs, activeTab = $bindable(), onTabChange, class: className }: Props = $props();

  // Handle tab selection
  function selectTab(tabId: string) {
    activeTab = tabId;
    onTabChange?.(tabId);
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
      case ' ':
        event.preventDefault();
        selectTab(tabs[currentIndex].id);
        return;
      default:
        return;
    }

    // Focus and select the new tab
    const newTab = tabs[newIndex];
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
    class="tabs tabs-box bg-base-200/50 mb-6 p-1 rounded-xl"
    role="tablist"
    aria-label={t('settings.tabs.navigation')}
  >
    {#each tabs as tab, index (tab.id)}
      {@const isActive = activeTab === tab.id}
      <button
        id="settings-tab-{tab.id}"
        type="button"
        role="tab"
        class={cn(
          'tab gap-2 transition-all duration-200 font-medium',
          'hover:bg-base-300/50',
          isActive
            ? 'tab-active bg-base-100 shadow-sm rounded-lg text-base-content'
            : 'text-base-content/90 hover:text-base-content'
        )}
        aria-selected={isActive}
        aria-controls="settings-tabpanel-{tab.id}"
        tabindex={isActive ? 0 : -1}
        onclick={() => selectTab(tab.id)}
        onkeydown={e => handleKeyDown(e, index)}
      >
        <!-- Default star indicator -->
        {#if tab.showDefaultStar}
          <Star
            class={cn(
              'size-3.5 transition-colors',
              tab.isDefault ? 'fill-warning text-warning' : 'text-base-content/30'
            )}
            aria-hidden="true"
          />
        {/if}

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
    {@const isActive = activeTab === tab.id}
    <div
      id="settings-tabpanel-{tab.id}"
      role="tabpanel"
      aria-labelledby="settings-tab-{tab.id}"
      class={cn('tab-panel', !isActive && 'hidden')}
      tabindex={isActive ? 0 : -1}
      hidden={!isActive}
    >
      {#if isActive}
        {@render tab.content()}
      {/if}
    </div>
  {/each}
</div>

<style>
  .settings-tabs {
    /* Ensure smooth transitions */
    & .tab {
      min-height: 2.5rem;
      padding-inline: 1rem;
    }

    & .tab-active {
      /* Subtle elevation for active tab */
      box-shadow:
        0 1px 2px rgba(0, 0, 0, 0.05),
        0 1px 3px rgba(0, 0, 0, 0.1);
    }

    /* Tab panel animation */
    & .tab-panel {
      animation: fadeIn 0.15s ease-out;
    }

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
  }

  /* Responsive: show only icons on very small screens */
  @media (max-width: 480px) {
    .settings-tabs .tab {
      padding-inline: 0.75rem;
    }
  }
</style>

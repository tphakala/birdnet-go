<!--
DesktopSidebar.svelte - Main navigation sidebar for desktop layout

Purpose:
- Primary navigation menu for the desktop interface
- Handles authentication state display (login/logout)
- Manages route navigation with special behaviors for specific routes
- Collapsible to icons-only mode with localStorage persistence

Features:
- Hierarchical navigation with collapsible sections
- Performance-optimized with cached route calculations
- Dashboard navigation resets date selection to current date
- Authentication integration with login/logout functionality
- Responsive drawer behavior for smaller screens
- Version display with GitHub repository link
- Collapsible sidebar with smooth transitions (desktop only)
- Flyout submenus for nested navigation when collapsed
- Tooltips for menu items when collapsed

Special Behaviors:
- Dashboard Link: When clicking the dashboard navigation link (home or "Dashboard" menu item),
  automatically resets the date persistence to show current date. This ensures users always
  see today's data when explicitly navigating to the dashboard.
- Collapse Toggle: Only visible on desktop (≥1024px). State persists in localStorage.

Props:
- securityEnabled?: boolean - Whether security/auth is enabled
- accessAllowed?: boolean - Whether user has access to protected routes
- version?: string - Version string to display
- currentRoute?: string - Current active route for highlighting
- onNavigate?: (url: string) => void - Custom navigation handler
- className?: string - Additional CSS classes
- authConfig?: object - Authentication configuration

Performance Optimizations:
- Route checking cached with $derived to avoid repeated calculations
- Navigation URLs pre-computed to eliminate string processing
- CSS containment for improved rendering performance
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { auth as authStore } from '$lib/stores/auth';
  import { sidebar } from '$lib/stores/sidebar';
  import { navigation } from '$lib/stores/navigation.svelte';
  import type { AuthConfig } from '../../../app.d';
  import {
    LayoutDashboard,
    BarChart3,
    Search,
    Info,
    Cpu,
    Settings,
    LogOut,
    LogIn,
    ChevronDown,
    ChevronsLeft,
    ChevronsRight,
  } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { resetDateToToday } from '$lib/utils/datePersistence';
  import LoginModal from '../components/modals/LoginModal.svelte';
  import LogoBadge from '$lib/components/LogoBadge.svelte';

  interface Props {
    securityEnabled?: boolean;
    accessAllowed?: boolean;
    version?: string;
    currentRoute?: string;
    onNavigate?: (_url: string) => void;
    className?: string;
    authConfig?: AuthConfig;
  }

  let {
    securityEnabled = false,
    accessAllowed = true,
    version = 'Development Build',
    currentRoute = '/ui/dashboard',
    onNavigate,
    className = '',
    authConfig = {
      basicEnabled: true,
      enabledProviders: [],
    },
  }: Props = $props();

  // State for login modal and collapsible sections
  let showLoginModal = $state(false);
  let analyticsExpanded = $state(false);
  let settingsExpanded = $state(false);
  let systemExpanded = $state(false);

  // Flyout state for collapsed mode
  let analyticsFlyoutOpen = $state(false);
  let settingsFlyoutOpen = $state(false);
  let systemFlyoutOpen = $state(false);

  // Flyout position (for fixed positioning to escape overflow container)
  let analyticsFlyoutPosition = $state({ top: 0, left: 0 });
  let settingsFlyoutPosition = $state({ top: 0, left: 0 });
  let systemFlyoutPosition = $state({ top: 0, left: 0 });

  // Tooltip state for fixed positioning (escapes overflow containers)
  let tooltipText = $state('');
  let tooltipPosition = $state({ top: 0, left: 0 });
  let tooltipVisible = $state(false);

  // Show tooltip with calculated position
  function showTooltip(event: MouseEvent, text: string) {
    const target = event.currentTarget as HTMLElement;
    const rect = target.getBoundingClientRect();
    tooltipPosition = {
      top: rect.top + rect.height / 2,
      left: rect.right + 8, // 8px gap (ml-2)
    };
    tooltipText = text;
    tooltipVisible = true;
  }

  // Hide tooltip
  function hideTooltip() {
    tooltipVisible = false;
  }

  // Button refs for position calculation
  let analyticsButtonRef = $state<HTMLButtonElement | null>(null);
  let settingsButtonRef = $state<HTMLButtonElement | null>(null);
  let systemButtonRef = $state<HTMLButtonElement | null>(null);

  // Toggle flyout with position calculation
  function toggleAnalyticsFlyout() {
    hideTooltip(); // Hide tooltip when opening flyout
    if (!analyticsFlyoutOpen && analyticsButtonRef) {
      const rect = analyticsButtonRef.getBoundingClientRect();
      analyticsFlyoutPosition = {
        top: rect.top,
        left: rect.right + 8, // 8px gap (ml-2)
      };
    }
    analyticsFlyoutOpen = !analyticsFlyoutOpen;
    settingsFlyoutOpen = false;
    systemFlyoutOpen = false;
  }

  function toggleSettingsFlyout() {
    hideTooltip(); // Hide tooltip when opening flyout
    if (!settingsFlyoutOpen && settingsButtonRef) {
      const rect = settingsButtonRef.getBoundingClientRect();
      settingsFlyoutPosition = {
        top: rect.top,
        left: rect.right + 8, // 8px gap (ml-2)
      };
    }
    settingsFlyoutOpen = !settingsFlyoutOpen;
    analyticsFlyoutOpen = false;
    systemFlyoutOpen = false;
  }

  function toggleSystemFlyout() {
    hideTooltip(); // Hide tooltip when opening flyout
    if (!systemFlyoutOpen && systemButtonRef) {
      const rect = systemButtonRef.getBoundingClientRect();
      systemFlyoutPosition = {
        top: rect.top,
        left: rect.right + 8, // 8px gap (ml-2)
      };
    }
    systemFlyoutOpen = !systemFlyoutOpen;
    analyticsFlyoutOpen = false;
    settingsFlyoutOpen = false;
  }

  // Get collapsed state from store (using $ prefix for auto-subscription)
  let isCollapsed = $derived($sidebar);

  // Use the currentRoute prop for reactive highlighting
  // This receives the full URL path (e.g., /ui/settings/main) from RootLayout
  let actualRoute = $derived(currentRoute);

  // PERFORMANCE OPTIMIZATION: Cache route calculations with $derived.by
  let routeCache = $derived.by(() => ({
    dashboard: actualRoute === '/ui/dashboard' || actualRoute === '/ui/',
    analytics: actualRoute.startsWith('/ui/analytics'),
    analyticsExact: actualRoute === '/ui/analytics',
    analyticsAdvanced: actualRoute === '/ui/analytics/advanced',
    analyticsSpecies: actualRoute === '/ui/analytics/species',
    search: actualRoute.startsWith('/ui/search'),
    about: actualRoute.startsWith('/ui/about'),
    system: actualRoute.startsWith('/ui/system'),
    systemOverview: actualRoute === '/ui/system',
    systemDatabase: actualRoute === '/ui/system/database',
    systemTerminal: actualRoute === '/ui/system/terminal',
    settings: actualRoute.startsWith('/ui/settings'),
    settingsMain: actualRoute === '/ui/settings/main',
    settingsAudio: actualRoute === '/ui/settings/audio',
    settingsSpecies: actualRoute === '/ui/settings/species',
    settingsFilters: actualRoute.startsWith('/ui/settings/detectionfilters'),
    settingsNotifications: actualRoute === '/ui/settings/notifications',
    settingsIntegrations: actualRoute === '/ui/settings/integrations',
    settingsSecurity: actualRoute === '/ui/settings/security',
    settingsSupport: actualRoute === '/ui/settings/support',
  }));

  // Auto-expand sections when route matches (only when not collapsed)
  $effect(() => {
    if (!isCollapsed) {
      if (routeCache.analytics) analyticsExpanded = true;
      if (routeCache.settings) settingsExpanded = true;
      if (routeCache.system) systemExpanded = true;
    }
  });

  // Close flyouts when clicking outside
  function handleClickOutside(event: MouseEvent) {
    const target = event.target as HTMLElement;
    if (!target.closest('.flyout-container')) {
      analyticsFlyoutOpen = false;
      settingsFlyoutOpen = false;
      systemFlyoutOpen = false;
    }
  }

  // PERFORMANCE OPTIMIZATION: Cache navigation URL transformations
  let navigationUrls = $derived({
    dashboard: onNavigate ? '/' : '/ui/dashboard',
    analytics: onNavigate ? '/analytics' : '/ui/analytics',
    analyticsAdvanced: '/ui/analytics/advanced',
    analyticsSpecies: onNavigate ? '/analytics/species' : '/ui/analytics/species',
    search: onNavigate ? '/search' : '/ui/search',
    about: onNavigate ? '/about' : '/ui/about',
    systemOverview: onNavigate ? '/system' : '/ui/system',
    systemDatabase: onNavigate ? '/system/database' : '/ui/system/database',
    systemTerminal: onNavigate ? '/system/terminal' : '/ui/system/terminal',
    settingsMain: onNavigate ? '/settings/main' : '/ui/settings/main',
    settingsAudio: onNavigate ? '/settings/audio' : '/ui/settings/audio',
    settingsSpecies: onNavigate ? '/settings/species' : '/ui/settings/species',
    settingsFilters: onNavigate ? '/settings/detectionfilters' : '/ui/settings/detectionfilters',
    settingsNotifications: onNavigate ? '/settings/notifications' : '/ui/settings/notifications',
    settingsIntegrations: onNavigate ? '/settings/integrations' : '/ui/settings/integrations',
    settingsSecurity: onNavigate ? '/settings/security' : '/ui/settings/security',
    settingsSupport: onNavigate ? '/settings/support' : '/ui/settings/support',
  });

  function navigate(url: string) {
    if (url === navigationUrls.dashboard) {
      resetDateToToday();
    }
    // Close flyouts on navigation
    analyticsFlyoutOpen = false;
    settingsFlyoutOpen = false;
    systemFlyoutOpen = false;
    if (onNavigate) {
      onNavigate(url);
    } else {
      // Fallback to navigation store for proxy-aware navigation
      navigation.navigate(url);
    }
  }

  async function handleLogout() {
    await authStore.logout();
  }

  function handleLogin() {
    showLoginModal = true;
  }

  function toggleSidebar() {
    sidebar.toggle();
  }

  // Close expanded sections when sidebar collapses
  $effect(() => {
    if ($sidebar) {
      // Sidebar is now collapsed - close expanded sections
      analyticsExpanded = false;
      settingsExpanded = false;
      systemExpanded = false;
    }
  });

  // Shared styles for menu items - inspired by modern sidebar designs
  const menuItemBase =
    'flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm font-medium transition-all duration-150 w-full text-left';
  const menuItemDefault = 'text-base-content/80 hover:text-base-content hover:menu-hover';
  const menuItemActive = 'menu-item-active';

  // Collapsed menu item styles
  let menuItemCollapsed = $derived(isCollapsed ? 'justify-center px-0' : '');
</script>

<svelte:window onclick={handleClickOutside} />

<aside
  class={cn(
    'drawer-side z-10 transition-all duration-200 ease-in-out overflow-visible',
    isCollapsed ? 'lg:w-16' : 'lg:w-64',
    className
  )}
  aria-label={t('navigation.mainNavigation')}
>
  <label for="my-drawer" class="drawer-overlay" aria-label={t('navigation.closeSidebar')}></label>

  <nav
    class={cn(
      'relative z-10 flex flex-col h-dvh bg-base-100 border-r border-base-200/50 transition-all duration-200 ease-in-out',
      isCollapsed ? 'w-16' : 'w-64'
    )}
  >
    <!-- Logo Header -->
    <div
      class={cn(
        'flex-none py-5 border-b border-base-200/50 relative',
        isCollapsed ? 'px-2' : 'px-4'
      )}
    >
      <div class={cn('flex items-center', isCollapsed ? 'justify-center' : 'justify-between')}>
        <button
          onclick={() => navigate(navigationUrls.dashboard)}
          class={cn('flex items-center gap-3 group', isCollapsed && 'justify-center')}
          aria-label="BirdNET-Go Home"
        >
          <LogoBadge size="md" variant="ocean" />
          {#if !isCollapsed}
            <span class="text-xl font-bold tracking-tight text-base-content">BirdNET-Go</span>
          {/if}
        </button>
        <!-- Collapse toggle - desktop only -->
        {#if !isCollapsed}
          <button
            onclick={toggleSidebar}
            class="hidden lg:flex items-center justify-center p-1.5 rounded-md text-base-content/60 hover:text-base-content hover:bg-base-content/10 transition-colors duration-150"
            aria-label={t('navigation.collapseSidebar')}
            title={t('navigation.collapseSidebar')}
          >
            <ChevronsLeft class="size-4" />
          </button>
        {/if}
      </div>
      <!-- Expand toggle when collapsed - positioned below logo -->
      {#if isCollapsed}
        <button
          onclick={toggleSidebar}
          class="hidden lg:flex items-center justify-center w-full mt-3 p-1.5 rounded-md text-base-content/60 hover:text-base-content hover:bg-base-content/10 transition-colors duration-150"
          aria-label={t('navigation.expandSidebar')}
          title={t('navigation.expandSidebar')}
        >
          <ChevronsRight class="size-4" />
        </button>
      {/if}
    </div>

    <!-- Navigation Menu -->
    <div class={cn('flex-1 overflow-y-auto py-4', isCollapsed ? 'px-2' : 'px-3')}>
      <div class="flex flex-col gap-1" role="navigation">
        <!-- Dashboard -->
        <div class="relative">
          <button
            onclick={() => navigate(navigationUrls.dashboard)}
            onmouseenter={e => isCollapsed && showTooltip(e, t('navigation.dashboard'))}
            onmouseleave={hideTooltip}
            class={cn(
              menuItemBase,
              menuItemCollapsed,
              routeCache.dashboard ? menuItemActive : menuItemDefault
            )}
            role="menuitem"
            aria-current={routeCache.dashboard ? 'page' : undefined}
          >
            <LayoutDashboard class="size-5 shrink-0" />
            {#if !isCollapsed}
              <span>{t('navigation.dashboard')}</span>
            {/if}
          </button>
        </div>

        <!-- Analytics (Collapsible) -->
        <div class="flex flex-col relative flyout-container">
          {#if isCollapsed}
            <!-- Collapsed: Icon with flyout -->
            <div class="relative">
              <button
                bind:this={analyticsButtonRef}
                onclick={toggleAnalyticsFlyout}
                onmouseenter={e =>
                  !analyticsFlyoutOpen && showTooltip(e, t('navigation.analytics'))}
                onmouseleave={hideTooltip}
                class={cn(
                  menuItemBase,
                  menuItemCollapsed,
                  routeCache.analytics ? 'text-primary' : 'text-base-content/80',
                  'hover:text-base-content hover:menu-hover'
                )}
                aria-expanded={analyticsFlyoutOpen}
                aria-label={t('navigation.analyticsSubmenu')}
              >
                <BarChart3 class="size-5 shrink-0" />
              </button>
            </div>
            <!-- Flyout submenu (fixed positioning to escape overflow container) -->
            {#if analyticsFlyoutOpen}
              <div
                class="fixed bg-base-100 border border-base-200 rounded-lg shadow-xl min-w-48 z-[100]"
                style:top="{analyticsFlyoutPosition.top}px"
                style:left="{analyticsFlyoutPosition.left}px"
              >
                <div
                  class="px-3 py-2 border-b border-base-200 font-medium text-sm text-base-content"
                >
                  {t('navigation.analytics')}
                </div>
                <div class="p-1">
                  <button
                    onclick={() => navigate(navigationUrls.analytics)}
                    class={cn(
                      'flex items-center w-full px-3 py-2 rounded-md text-sm transition-colors duration-150',
                      routeCache.analyticsExact
                        ? 'menu-subitem-active'
                        : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                    )}
                  >
                    {t('analytics.title')}
                  </button>
                  <button
                    onclick={() => navigate(navigationUrls.analyticsSpecies)}
                    class={cn(
                      'flex items-center w-full px-3 py-2 rounded-md text-sm transition-colors duration-150',
                      routeCache.analyticsSpecies
                        ? 'menu-subitem-active'
                        : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                    )}
                  >
                    {t('analytics.species.title')}
                  </button>
                  <button
                    onclick={() => navigate(navigationUrls.analyticsAdvanced)}
                    class={cn(
                      'flex items-center w-full px-3 py-2 rounded-md text-sm transition-colors duration-150',
                      routeCache.analyticsAdvanced
                        ? 'menu-subitem-active'
                        : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                    )}
                  >
                    {t('analytics.advanced.title')}
                  </button>
                </div>
              </div>
            {/if}
          {:else}
            <!-- Expanded: Regular collapsible -->
            <button
              onclick={() => (analyticsExpanded = !analyticsExpanded)}
              class={cn(
                menuItemBase,
                routeCache.analytics ? 'text-primary' : 'text-base-content/80',
                'hover:text-base-content hover:menu-hover'
              )}
              aria-expanded={analyticsExpanded}
            >
              <BarChart3 class="size-5 shrink-0" />
              <span class="flex-1">{t('navigation.analytics')}</span>
              <ChevronDown
                class={cn('size-4 shrink-0 transition-transform duration-200', {
                  'rotate-180': analyticsExpanded,
                })}
              />
            </button>

            {#if analyticsExpanded}
              <div
                class="ml-4 pl-4 border-l-2 border-primary mt-1 flex flex-col gap-0.5"
                style:border-color="color-mix(in oklch, var(--color-primary) 30%, transparent)"
              >
                <button
                  onclick={() => navigate(navigationUrls.analytics)}
                  class={cn(
                    'flex items-center px-3 py-2 rounded-md text-sm transition-colors duration-150',
                    routeCache.analyticsExact
                      ? 'menu-subitem-active'
                      : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                  )}
                >
                  {t('analytics.title')}
                </button>
                <button
                  onclick={() => navigate(navigationUrls.analyticsSpecies)}
                  class={cn(
                    'flex items-center px-3 py-2 rounded-md text-sm transition-colors duration-150',
                    routeCache.analyticsSpecies
                      ? 'menu-subitem-active'
                      : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                  )}
                >
                  {t('analytics.species.title')}
                </button>
                <button
                  onclick={() => navigate(navigationUrls.analyticsAdvanced)}
                  class={cn(
                    'flex items-center px-3 py-2 rounded-md text-sm transition-colors duration-150',
                    routeCache.analyticsAdvanced
                      ? 'menu-subitem-active'
                      : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                  )}
                >
                  {t('analytics.advanced.title')}
                </button>
              </div>
            {/if}
          {/if}
        </div>

        <!-- Search -->
        <div class="relative">
          <button
            onclick={() => navigate(navigationUrls.search)}
            onmouseenter={e => isCollapsed && showTooltip(e, t('navigation.search'))}
            onmouseleave={hideTooltip}
            class={cn(
              menuItemBase,
              menuItemCollapsed,
              routeCache.search ? menuItemActive : menuItemDefault
            )}
            role="menuitem"
          >
            <Search class="size-5 shrink-0" />
            {#if !isCollapsed}
              <span>{t('navigation.search')}</span>
            {/if}
          </button>
        </div>

        <!-- About -->
        <div class="relative">
          <button
            onclick={() => navigate(navigationUrls.about)}
            onmouseenter={e => isCollapsed && showTooltip(e, t('navigation.about'))}
            onmouseleave={hideTooltip}
            class={cn(
              menuItemBase,
              menuItemCollapsed,
              routeCache.about ? menuItemActive : menuItemDefault
            )}
            role="menuitem"
          >
            <Info class="size-5 shrink-0" />
            {#if !isCollapsed}
              <span>{t('navigation.about')}</span>
            {/if}
          </button>
        </div>

        {#if !securityEnabled || accessAllowed}
          <!-- Divider -->
          <div class="my-2 border-t border-base-200/50"></div>

          <!-- System (Collapsible) -->
          <div class="flex flex-col relative flyout-container">
            {#if isCollapsed}
              <!-- Collapsed: Icon with flyout -->
              <div class="relative">
                <button
                  bind:this={systemButtonRef}
                  onclick={toggleSystemFlyout}
                  onmouseenter={e => !systemFlyoutOpen && showTooltip(e, t('navigation.system'))}
                  onmouseleave={hideTooltip}
                  class={cn(
                    menuItemBase,
                    menuItemCollapsed,
                    routeCache.system ? 'text-primary' : 'text-base-content/80',
                    'hover:text-base-content hover:menu-hover'
                  )}
                  aria-expanded={systemFlyoutOpen}
                  aria-label={t('navigation.systemSubmenu')}
                >
                  <Cpu class="size-5 shrink-0" />
                </button>
              </div>
              <!-- Flyout submenu (fixed positioning to escape overflow container) -->
              {#if systemFlyoutOpen}
                <div
                  class="fixed bg-base-100 border border-base-200 rounded-lg shadow-xl min-w-48 z-[100]"
                  style:top="{systemFlyoutPosition.top}px"
                  style:left="{systemFlyoutPosition.left}px"
                >
                  <div
                    class="px-3 py-2 border-b border-base-200 font-medium text-sm text-base-content"
                  >
                    {t('navigation.system')}
                  </div>
                  <div class="p-1">
                    <button
                      onclick={() => navigate(navigationUrls.systemOverview)}
                      class={cn(
                        'flex items-center w-full px-3 py-2 rounded-md text-sm transition-colors duration-150',
                        routeCache.systemOverview
                          ? 'menu-subitem-active'
                          : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                      )}
                    >
                      {t('system.sections.overview')}
                    </button>
                    <button
                      onclick={() => navigate(navigationUrls.systemDatabase)}
                      class={cn(
                        'flex items-center w-full px-3 py-2 rounded-md text-sm transition-colors duration-150',
                        routeCache.systemDatabase
                          ? 'menu-subitem-active'
                          : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                      )}
                    >
                      {t('system.sections.database')}
                    </button>
                    <button
                      onclick={() => navigate(navigationUrls.systemTerminal)}
                      class={cn(
                        'flex items-center w-full px-3 py-2 rounded-md text-sm transition-colors duration-150',
                        routeCache.systemTerminal
                          ? 'menu-subitem-active'
                          : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                      )}
                    >
                      {t('system.sections.terminal')}
                    </button>
                  </div>
                </div>
              {/if}
            {:else}
              <!-- Expanded: Regular collapsible -->
              <button
                onclick={() => (systemExpanded = !systemExpanded)}
                class={cn(
                  menuItemBase,
                  routeCache.system ? 'text-primary' : 'text-base-content/80',
                  'hover:text-base-content hover:menu-hover'
                )}
                aria-expanded={systemExpanded}
              >
                <Cpu class="size-5 shrink-0" />
                <span class="flex-1">{t('navigation.system')}</span>
                <ChevronDown
                  class={cn('size-4 shrink-0 transition-transform duration-200', {
                    'rotate-180': systemExpanded,
                  })}
                />
              </button>

              {#if systemExpanded}
                <div
                  class="ml-4 pl-4 border-l-2 border-primary mt-1 flex flex-col gap-0.5"
                  style:border-color="color-mix(in oklch, var(--color-primary) 30%, transparent)"
                >
                  <button
                    onclick={() => navigate(navigationUrls.systemOverview)}
                    class={cn(
                      'flex items-center px-3 py-2 rounded-md text-sm transition-colors duration-150',
                      routeCache.systemOverview
                        ? 'menu-subitem-active'
                        : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                    )}
                  >
                    {t('system.sections.overview')}
                  </button>
                  <button
                    onclick={() => navigate(navigationUrls.systemDatabase)}
                    class={cn(
                      'flex items-center px-3 py-2 rounded-md text-sm transition-colors duration-150',
                      routeCache.systemDatabase
                        ? 'menu-subitem-active'
                        : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                    )}
                  >
                    {t('system.sections.database')}
                  </button>
                  <button
                    onclick={() => navigate(navigationUrls.systemTerminal)}
                    class={cn(
                      'flex items-center px-3 py-2 rounded-md text-sm transition-colors duration-150',
                      routeCache.systemTerminal
                        ? 'menu-subitem-active'
                        : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                    )}
                  >
                    {t('system.sections.terminal')}
                  </button>
                </div>
              {/if}
            {/if}
          </div>

          <!-- Settings (Collapsible) -->
          <div class="flex flex-col relative flyout-container">
            {#if isCollapsed}
              <!-- Collapsed: Icon with flyout -->
              <div class="relative">
                <button
                  bind:this={settingsButtonRef}
                  onclick={toggleSettingsFlyout}
                  onmouseenter={e =>
                    !settingsFlyoutOpen && showTooltip(e, t('navigation.settings'))}
                  onmouseleave={hideTooltip}
                  class={cn(
                    menuItemBase,
                    menuItemCollapsed,
                    routeCache.settings ? 'text-primary' : 'text-base-content/80',
                    'hover:text-base-content hover:menu-hover'
                  )}
                  aria-expanded={settingsFlyoutOpen}
                  aria-label={t('navigation.settingsSubmenu')}
                >
                  <Settings class="size-5 shrink-0" />
                </button>
              </div>
              <!-- Flyout submenu (fixed positioning to escape overflow container) -->
              {#if settingsFlyoutOpen}
                <div
                  class="fixed bg-base-100 border border-base-200 rounded-lg shadow-xl min-w-48 z-[100]"
                  style:top="{settingsFlyoutPosition.top}px"
                  style:left="{settingsFlyoutPosition.left}px"
                >
                  <div
                    class="px-3 py-2 border-b border-base-200 font-medium text-sm text-base-content"
                  >
                    {t('navigation.settings')}
                  </div>
                  <div class="p-1 max-h-80 overflow-y-auto">
                    <button
                      onclick={() => navigate(navigationUrls.settingsMain)}
                      class={cn(
                        'flex items-center w-full px-3 py-2 rounded-md text-sm transition-colors duration-150',
                        routeCache.settingsMain
                          ? 'menu-subitem-active'
                          : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                      )}
                    >
                      {t('settings.sections.node')}
                    </button>
                    <button
                      onclick={() => navigate(navigationUrls.settingsAudio)}
                      class={cn(
                        'flex items-center w-full px-3 py-2 rounded-md text-sm transition-colors duration-150',
                        routeCache.settingsAudio
                          ? 'menu-subitem-active'
                          : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                      )}
                    >
                      {t('settings.sections.audio')}
                    </button>
                    <button
                      onclick={() => navigate(navigationUrls.settingsSpecies)}
                      class={cn(
                        'flex items-center w-full px-3 py-2 rounded-md text-sm transition-colors duration-150',
                        routeCache.settingsSpecies
                          ? 'menu-subitem-active'
                          : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                      )}
                    >
                      {t('settings.sections.species')}
                    </button>
                    <button
                      onclick={() => navigate(navigationUrls.settingsFilters)}
                      class={cn(
                        'flex items-center w-full px-3 py-2 rounded-md text-sm transition-colors duration-150',
                        routeCache.settingsFilters
                          ? 'menu-subitem-active'
                          : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                      )}
                    >
                      {t('settings.sections.filters')}
                    </button>
                    <button
                      onclick={() => navigate(navigationUrls.settingsNotifications)}
                      class={cn(
                        'flex items-center w-full px-3 py-2 rounded-md text-sm transition-colors duration-150',
                        routeCache.settingsNotifications
                          ? 'menu-subitem-active'
                          : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                      )}
                    >
                      {t('settings.sections.notifications')}
                    </button>
                    <button
                      onclick={() => navigate(navigationUrls.settingsIntegrations)}
                      class={cn(
                        'flex items-center w-full px-3 py-2 rounded-md text-sm transition-colors duration-150',
                        routeCache.settingsIntegrations
                          ? 'menu-subitem-active'
                          : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                      )}
                    >
                      {t('settings.sections.integration')}
                    </button>
                    <button
                      onclick={() => navigate(navigationUrls.settingsSecurity)}
                      class={cn(
                        'flex items-center w-full px-3 py-2 rounded-md text-sm transition-colors duration-150',
                        routeCache.settingsSecurity
                          ? 'menu-subitem-active'
                          : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                      )}
                    >
                      {t('settings.sections.security')}
                    </button>
                    <button
                      onclick={() => navigate(navigationUrls.settingsSupport)}
                      class={cn(
                        'flex items-center w-full px-3 py-2 rounded-md text-sm transition-colors duration-150',
                        routeCache.settingsSupport
                          ? 'menu-subitem-active'
                          : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                      )}
                    >
                      {t('settings.sections.support')}
                    </button>
                  </div>
                </div>
              {/if}
            {:else}
              <!-- Expanded: Regular collapsible -->
              <button
                onclick={() => (settingsExpanded = !settingsExpanded)}
                class={cn(
                  menuItemBase,
                  routeCache.settings ? 'text-primary' : 'text-base-content/80',
                  'hover:text-base-content hover:menu-hover'
                )}
                aria-expanded={settingsExpanded}
              >
                <Settings class="size-5 shrink-0" />
                <span class="flex-1">{t('navigation.settings')}</span>
                <ChevronDown
                  class={cn('size-4 shrink-0 transition-transform duration-200', {
                    'rotate-180': settingsExpanded,
                  })}
                />
              </button>

              {#if settingsExpanded}
                <div
                  class="ml-4 pl-4 border-l-2 mt-1 flex flex-col gap-0.5"
                  style:border-color="color-mix(in oklch, var(--color-primary) 30%, transparent)"
                >
                  <button
                    onclick={() => navigate(navigationUrls.settingsMain)}
                    class={cn(
                      'flex items-center px-3 py-2 rounded-md text-sm transition-colors duration-150',
                      routeCache.settingsMain
                        ? 'menu-subitem-active'
                        : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                    )}
                  >
                    {t('settings.sections.node')}
                  </button>
                  <button
                    onclick={() => navigate(navigationUrls.settingsAudio)}
                    class={cn(
                      'flex items-center px-3 py-2 rounded-md text-sm transition-colors duration-150',
                      routeCache.settingsAudio
                        ? 'menu-subitem-active'
                        : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                    )}
                  >
                    {t('settings.sections.audio')}
                  </button>
                  <button
                    onclick={() => navigate(navigationUrls.settingsSpecies)}
                    class={cn(
                      'flex items-center px-3 py-2 rounded-md text-sm transition-colors duration-150',
                      routeCache.settingsSpecies
                        ? 'menu-subitem-active'
                        : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                    )}
                  >
                    {t('settings.sections.species')}
                  </button>
                  <button
                    onclick={() => navigate(navigationUrls.settingsFilters)}
                    class={cn(
                      'flex items-center px-3 py-2 rounded-md text-sm transition-colors duration-150',
                      routeCache.settingsFilters
                        ? 'menu-subitem-active'
                        : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                    )}
                  >
                    {t('settings.sections.filters')}
                  </button>
                  <button
                    onclick={() => navigate(navigationUrls.settingsNotifications)}
                    class={cn(
                      'flex items-center px-3 py-2 rounded-md text-sm transition-colors duration-150',
                      routeCache.settingsNotifications
                        ? 'menu-subitem-active'
                        : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                    )}
                  >
                    {t('settings.sections.notifications')}
                  </button>
                  <button
                    onclick={() => navigate(navigationUrls.settingsIntegrations)}
                    class={cn(
                      'flex items-center px-3 py-2 rounded-md text-sm transition-colors duration-150',
                      routeCache.settingsIntegrations
                        ? 'menu-subitem-active'
                        : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                    )}
                  >
                    {t('settings.sections.integration')}
                  </button>
                  <button
                    onclick={() => navigate(navigationUrls.settingsSecurity)}
                    class={cn(
                      'flex items-center px-3 py-2 rounded-md text-sm transition-colors duration-150',
                      routeCache.settingsSecurity
                        ? 'menu-subitem-active'
                        : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                    )}
                  >
                    {t('settings.sections.security')}
                  </button>
                  <button
                    onclick={() => navigate(navigationUrls.settingsSupport)}
                    class={cn(
                      'flex items-center px-3 py-2 rounded-md text-sm transition-colors duration-150',
                      routeCache.settingsSupport
                        ? 'menu-subitem-active'
                        : 'text-base-content/80 hover:text-base-content hover:menu-hover'
                    )}
                  >
                    {t('settings.sections.support')}
                  </button>
                </div>
              {/if}
            {/if}
          </div>
        {/if}
      </div>
    </div>

    <!-- Footer -->
    <div class={cn('flex-none py-4 border-t border-base-200/50', isCollapsed ? 'px-2' : 'px-3')}>
      {#if securityEnabled}
        {#if accessAllowed}
          <div class="relative">
            <button
              onclick={handleLogout}
              onmouseenter={e => isCollapsed && showTooltip(e, t('auth.logout'))}
              onmouseleave={hideTooltip}
              class={cn(
                'flex items-center gap-2 w-full px-3 py-2 rounded-lg text-sm font-medium text-base-content/90 hover:text-base-content hover:bg-base-content/5 transition-colors duration-150',
                isCollapsed && 'justify-center'
              )}
              aria-label={t('auth.logout')}
            >
              <LogOut class="size-4" />
              {#if !isCollapsed}
                <span>{t('auth.logout')}</span>
              {/if}
            </button>
          </div>
        {:else}
          <div class="relative">
            <button
              onclick={handleLogin}
              onmouseenter={e => isCollapsed && showTooltip(e, t('auth.login'))}
              onmouseleave={hideTooltip}
              class={cn(
                'flex items-center gap-2 w-full px-3 py-2 rounded-lg text-sm font-medium text-base-content/90 hover:text-base-content hover:bg-base-content/5 transition-colors duration-150',
                isCollapsed && 'justify-center'
              )}
              aria-label={t('auth.openLoginModal')}
            >
              <LogIn class="size-4" />
              {#if !isCollapsed}
                <span>{t('auth.login')}</span>
              {/if}
            </button>
          </div>
        {/if}
      {/if}

      <!-- Version -->
      {#if !isCollapsed}
        <div class="mt-3 text-center">
          <a
            href="https://github.com/tphakala/birdnet-go"
            target="_blank"
            rel="noopener noreferrer"
            class="text-xs text-base-content/60 hover:text-base-content/80 transition-colors duration-150"
            aria-label="View BirdNET-Go repository on GitHub (opens in new window)"
          >
            {version}
          </a>
        </div>
      {/if}
    </div>
  </nav>

  <!-- Fixed-position tooltip for collapsed sidebar (escapes overflow containers) -->
  {#if tooltipVisible && isCollapsed}
    <div
      class="fixed px-2 py-1 bg-base-300 text-base-content text-sm rounded shadow-lg pointer-events-none whitespace-nowrap z-[100] -translate-y-1/2"
      style:top="{tooltipPosition.top}px"
      style:left="{tooltipPosition.left}px"
    >
      {tooltipText}
    </div>
  {/if}
</aside>

<!-- Login Modal -->
<LoginModal
  isOpen={showLoginModal}
  onClose={() => (showLoginModal = false)}
  redirectUrl={window.location.pathname}
  {authConfig}
/>

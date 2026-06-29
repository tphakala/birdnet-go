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
    Radio,
    BarChart3,
    Search,
    Info,
    Cpu,
    Settings,
    LogOut,
    LogIn,
    ChevronsLeft,
    ChevronsRight,
    Bird,
    Monitor,
    Database,
    Terminal,
    SlidersHorizontal,
    Volume2,
    Filter,
    Bell,
    Puzzle,
    Shield,
    LifeBuoy,
    Paintbrush,
    Brain,
    CircleHelp,
    Bug,
    MessageCircleQuestion,
    ExternalLink,
    Activity,
    ArrowDownToLine,
    TrendingUp,
    Leaf,
    BadgeCheck,
  } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import CollapsibleNavSection from './CollapsibleNavSection.svelte';
  import type { NavItem } from './CollapsibleNavSection.svelte';
  import { appState, hasLiveAudioAccess } from '$lib/stores/appState.svelte';
  import { resetDateToToday } from '$lib/utils/datePersistence';
  import { getCurrentPathWithQuery } from '$lib/utils/urlHelpers';
  import LoginModal from '../components/modals/LoginModal.svelte';
  import LogoBadge from '$lib/components/LogoBadge.svelte';
  import { scheme } from '$lib/stores/scheme';
  import { logoStyle } from '$lib/stores/logoStyle';
  import { SCHEME_GRADIENT_MAP, type LogoVariant } from '$lib/stores/logoVariant';

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

  // Logo variant: solid uses flat color, gradient uses per-scheme handcrafted gradient
  let logoVariant: LogoVariant = $derived(
    // eslint-disable-next-line security/detect-object-injection -- $scheme is a controlled color scheme identifier
    $logoStyle === 'solid' ? 'solid' : (SCHEME_GRADIENT_MAP[$scheme] ?? 'scheme')
  );

  // State for login modal and collapsible sections
  let showLoginModal = $state(false);
  // Snapshot of the URL (path + query) the user was on when they opened the login
  // modal, so a login from a filtered view returns them to that exact view (#3306).
  let loginRedirectUrl = $state('/ui/');
  let analyticsExpanded = $state(false);
  let settingsExpanded = $state(false);
  let systemExpanded = $state(false);
  let helpExpanded = $state(false);

  // Single flyout state (mutual exclusion built-in)
  let activeFlyout = $state<string | null>(null);

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

  function toggleFlyout(sectionId: string) {
    activeFlyout = activeFlyout === sectionId ? null : sectionId;
  }

  // Get collapsed state from store (using $ prefix for auto-subscription)
  let isCollapsed = $derived($sidebar);

  // Use the currentRoute prop for reactive highlighting
  // This receives the full URL path (e.g., /ui/settings/main) from RootLayout
  let actualRoute = $derived(currentRoute);

  // PERFORMANCE OPTIMIZATION: Cache route calculations with $derived.by
  let routeCache = $derived.by(() => ({
    dashboard: actualRoute === '/ui/dashboard' || actualRoute === '/ui/',
    liveStream: actualRoute.startsWith('/ui/live-stream'),
    analytics: actualRoute.startsWith('/ui/analytics'),
    analyticsSummary: actualRoute === '/ui/analytics/summary',
    analyticsActivity: actualRoute === '/ui/analytics/activity',
    analyticsTrends: actualRoute === '/ui/analytics/trends',
    analyticsBiodiversity: actualRoute === '/ui/analytics/biodiversity',
    analyticsReview: actualRoute === '/ui/analytics/review',
    analyticsSpecies: actualRoute === '/ui/analytics/species',
    search: actualRoute.startsWith('/ui/search'),
    about: actualRoute.startsWith('/ui/about'),
    system: actualRoute.startsWith('/ui/system'),
    systemOverview: actualRoute === '/ui/system',
    systemDatabase: actualRoute === '/ui/system/database',
    systemInference: actualRoute === '/ui/system/inference',
    systemTerminal: actualRoute === '/ui/system/terminal',
    systemHealth: actualRoute === '/ui/system/health',
    systemImportExport: actualRoute === '/ui/system/import-export',
    help: actualRoute.startsWith('/ui/help'),
    helpExact: actualRoute === '/ui/help',
    helpReportBug: actualRoute === '/ui/help/report-bug',
    settings: actualRoute.startsWith('/ui/settings'),
    settingsAnalysis: actualRoute === '/ui/settings/analysis',
    settingsMain: actualRoute === '/ui/settings/main',
    settingsAudio: actualRoute === '/ui/settings/audio',
    settingsSpecies: actualRoute === '/ui/settings/species',
    settingsFilters: actualRoute.startsWith('/ui/settings/detectionfilters'),
    settingsNotifications: actualRoute === '/ui/settings/notifications',
    settingsIntegrations: actualRoute === '/ui/settings/integrations',
    settingsSecurity: actualRoute === '/ui/settings/security',
    settingsSupport: actualRoute === '/ui/settings/support',
    settingsUserInterface: actualRoute === '/ui/settings/userinterface',
  }));

  // Auto-expand sections when route matches (only when not collapsed)
  $effect(() => {
    if (!isCollapsed) {
      if (routeCache.analytics) analyticsExpanded = true;
      if (routeCache.settings) settingsExpanded = true;
      if (routeCache.system) systemExpanded = true;
      if (routeCache.help) helpExpanded = true;
    }
  });

  // Close flyouts when clicking outside
  function handleClickOutside(event: MouseEvent) {
    const target = event.target as HTMLElement;
    if (!target.closest('.flyout-container')) {
      activeFlyout = null;
    }
  }

  // PERFORMANCE OPTIMIZATION: Cache navigation URL transformations
  let navigationUrls = $derived({
    dashboard: onNavigate ? '/' : '/ui/dashboard',
    liveStream: onNavigate ? '/live-stream' : '/ui/live-stream',
    analyticsSummary: onNavigate ? '/analytics/summary' : '/ui/analytics/summary',
    analyticsActivity: onNavigate ? '/analytics/activity' : '/ui/analytics/activity',
    analyticsTrends: onNavigate ? '/analytics/trends' : '/ui/analytics/trends',
    analyticsBiodiversity: onNavigate ? '/analytics/biodiversity' : '/ui/analytics/biodiversity',
    analyticsReview: onNavigate ? '/analytics/review' : '/ui/analytics/review',
    analyticsSpecies: onNavigate ? '/analytics/species' : '/ui/analytics/species',
    search: onNavigate ? '/search' : '/ui/search',
    about: onNavigate ? '/about' : '/ui/about',
    help: onNavigate ? '/help' : '/ui/help',
    helpReportBug: onNavigate ? '/help/report-bug' : '/ui/help/report-bug',
    systemOverview: onNavigate ? '/system' : '/ui/system',
    systemDatabase: onNavigate ? '/system/database' : '/ui/system/database',
    systemInference: onNavigate ? '/system/inference' : '/ui/system/inference',
    systemTerminal: onNavigate ? '/system/terminal' : '/ui/system/terminal',
    systemHealth: onNavigate ? '/system/health' : '/ui/system/health',
    systemImportExport: onNavigate ? '/system/import-export' : '/ui/system/import-export',
    settingsAnalysis: onNavigate ? '/settings/analysis' : '/ui/settings/analysis',
    settingsMain: onNavigate ? '/settings/main' : '/ui/settings/main',
    settingsAudio: onNavigate ? '/settings/audio' : '/ui/settings/audio',
    settingsSpecies: onNavigate ? '/settings/species' : '/ui/settings/species',
    settingsFilters: onNavigate ? '/settings/detectionfilters' : '/ui/settings/detectionfilters',
    settingsNotifications: onNavigate ? '/settings/notifications' : '/ui/settings/notifications',
    settingsIntegrations: onNavigate ? '/settings/integrations' : '/ui/settings/integrations',
    settingsSecurity: onNavigate ? '/settings/security' : '/ui/settings/security',
    settingsSupport: onNavigate ? '/settings/support' : '/ui/settings/support',
    settingsUserInterface: onNavigate ? '/settings/userinterface' : '/ui/settings/userinterface',
  });

  // Nav item definitions for collapsible sections
  let analyticsItems: NavItem[] = $derived([
    {
      icon: BarChart3,
      label: t('analytics.hub.tabs.summary'),
      url: navigationUrls.analyticsSummary,
      routeKey: 'analyticsSummary',
    },
    {
      icon: Bird,
      label: t('analytics.species.title'),
      url: navigationUrls.analyticsSpecies,
      routeKey: 'analyticsSpecies',
    },
    {
      icon: Activity,
      label: t('analytics.hub.tabs.patterns'),
      url: navigationUrls.analyticsActivity,
      routeKey: 'analyticsActivity',
    },
    {
      icon: TrendingUp,
      label: t('analytics.hub.tabs.trends'),
      url: navigationUrls.analyticsTrends,
      routeKey: 'analyticsTrends',
    },
    {
      icon: Leaf,
      label: t('analytics.hub.tabs.biodiversity'),
      url: navigationUrls.analyticsBiodiversity,
      routeKey: 'analyticsBiodiversity',
    },
    {
      icon: BadgeCheck,
      label: t('analytics.hub.tabs.quality'),
      url: navigationUrls.analyticsReview,
      routeKey: 'analyticsReview',
    },
  ]);

  let systemItems: NavItem[] = $derived([
    {
      icon: Monitor,
      label: t('system.sections.overview'),
      url: navigationUrls.systemOverview,
      routeKey: 'systemOverview',
    },
    {
      icon: Database,
      label: t('system.sections.database'),
      url: navigationUrls.systemDatabase,
      routeKey: 'systemDatabase',
    },
    {
      icon: Brain,
      label: t('system.sections.inference'),
      url: navigationUrls.systemInference,
      routeKey: 'systemInference',
    },
    {
      icon: Terminal,
      label: t('system.sections.terminal'),
      url: navigationUrls.systemTerminal,
      routeKey: 'systemTerminal',
    },
    {
      icon: Activity,
      label: t('navigation.health'),
      url: navigationUrls.systemHealth,
      routeKey: 'systemHealth',
    },
    {
      icon: ArrowDownToLine,
      label: t('system.sections.importExport'),
      url: navigationUrls.systemImportExport,
      routeKey: 'systemImportExport',
    },
  ]);

  let helpItems: NavItem[] = $derived([
    {
      icon: LifeBuoy,
      label: t('navigation.helpAndSupport'),
      url: navigationUrls.help,
      routeKey: 'helpExact',
    },
    {
      icon: Bug,
      label: t('navigation.reportBug'),
      url: navigationUrls.helpReportBug,
      routeKey: 'helpReportBug',
    },
    {
      type: 'link',
      icon: MessageCircleQuestion,
      label: t('navigation.askQuestion'),
      href: appState.projectLinks.discussionsUrl,
      ariaLabel: t('navigation.askQuestionAriaLabel'),
      trailingIcon: ExternalLink,
    },
  ]);

  let settingsItems: NavItem[] = $derived([
    {
      icon: SlidersHorizontal,
      label: t('settings.sections.node'),
      url: navigationUrls.settingsMain,
      routeKey: 'settingsMain',
    },
    {
      icon: Paintbrush,
      label: t('settings.sections.userinterface'),
      url: navigationUrls.settingsUserInterface,
      routeKey: 'settingsUserInterface',
    },
    {
      icon: Volume2,
      label: t('settings.sections.audio'),
      url: navigationUrls.settingsAudio,
      routeKey: 'settingsAudio',
    },
    {
      icon: Brain,
      label: t('settings.sections.analysis'),
      url: navigationUrls.settingsAnalysis,
      routeKey: 'settingsAnalysis',
    },
    {
      icon: Bird,
      label: t('settings.sections.species'),
      url: navigationUrls.settingsSpecies,
      routeKey: 'settingsSpecies',
    },
    {
      icon: Filter,
      label: t('settings.sections.filters'),
      url: navigationUrls.settingsFilters,
      routeKey: 'settingsFilters',
    },
    {
      icon: Bell,
      label: t('settings.sections.notifications'),
      url: navigationUrls.settingsNotifications,
      routeKey: 'settingsNotifications',
    },
    {
      icon: Puzzle,
      label: t('settings.sections.integration'),
      url: navigationUrls.settingsIntegrations,
      routeKey: 'settingsIntegrations',
    },
    {
      icon: Shield,
      label: t('settings.sections.security'),
      url: navigationUrls.settingsSecurity,
      routeKey: 'settingsSecurity',
    },
    {
      icon: LifeBuoy,
      label: t('settings.sections.support'),
      url: navigationUrls.settingsSupport,
      routeKey: 'settingsSupport',
    },
  ]);

  // Close the mobile drawer by unchecking the toggle. Dispatch a synthetic
  // change event so Svelte's bind:checked binding stays in sync.
  function closeMobileDrawer() {
    const drawer = document.getElementById('my-drawer');
    if (drawer instanceof HTMLInputElement && drawer.checked) {
      drawer.checked = false;
      drawer.dispatchEvent(new Event('change', { bubbles: true }));
    }
  }

  function navigate(url: string) {
    if (url === navigationUrls.dashboard) {
      resetDateToToday();
    }
    // Close flyouts on navigation
    activeFlyout = null;
    closeMobileDrawer();
    if (onNavigate) {
      onNavigate(url);
    } else {
      // Fallback to navigation store for proxy-aware navigation
      navigation.navigate(url);
    }
  }

  async function handleLogout() {
    try {
      await authStore.logout();
    } catch {
      // authStore.logout() already logs before rethrowing; swallow here
      // to prevent an unhandled rejection from the non-awaited onclick.
    }
  }

  function handleLogin() {
    // Capture the full current location (path + query) at click time so the
    // post-login redirect returns the user to the exact filtered view (#3306).
    loginRedirectUrl = getCurrentPathWithQuery();
    // Close the mobile drawer before opening the modal to ensure a clean transition.
    closeMobileDrawer();
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
      helpExpanded = false;
    }
  });

  // Shared styles for menu items - inspired by modern sidebar designs
  const menuItemBase =
    'flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm font-medium transition-all duration-150 w-full text-left';
  const menuItemDefault =
    'text-[var(--color-base-content)]/80 hover:text-[var(--color-base-content)] hover:menu-hover';
  const menuItemActive = 'menu-item-active';

  // Collapsed menu item styles
  let menuItemCollapsed = $derived(isCollapsed ? 'justify-center px-0' : '');
</script>

<svelte:window onclick={handleClickOutside} />

<aside
  class={cn(
    // z-[200] matches Z_INDEX.SIDEBAR_DRAWER; lg:z-10 restores desktop stacking
    'drawer-side z-[200] lg:z-10 transition-all duration-200 ease-in-out overflow-visible',
    isCollapsed ? 'lg:w-16' : 'lg:w-64',
    className
  )}
  aria-label={t('navigation.mainNavigation')}
>
  <label for="my-drawer" class="drawer-overlay" aria-label={t('navigation.closeSidebar')}></label>

  <nav
    class={cn(
      'relative z-10 flex flex-col h-dvh bg-[var(--color-base-100)] border-r border-[var(--color-base-200)]/50 transition-all duration-200 ease-in-out',
      isCollapsed ? 'w-16' : 'w-64'
    )}
  >
    <!-- Logo Header -->
    <div
      class={cn(
        'flex-none py-5 border-b border-[var(--color-base-200)]/50 relative',
        isCollapsed ? 'px-2' : 'px-4'
      )}
    >
      <div class={cn('flex items-center', isCollapsed ? 'justify-center' : 'justify-between')}>
        <button
          onclick={() => navigate(navigationUrls.dashboard)}
          class={cn('flex items-center gap-3 group', isCollapsed && 'justify-center')}
          aria-label={t('navigation.dashboard')}
        >
          <LogoBadge size="md" variant={logoVariant} />
          {#if !isCollapsed}
            <span class="text-xl font-bold tracking-tight text-[var(--color-base-content)]"
              >BirdNET-Go</span
            >
          {/if}
        </button>
        <!-- Collapse toggle - desktop only -->
        {#if !isCollapsed}
          <button
            onclick={toggleSidebar}
            class="hidden lg:flex items-center justify-center p-1.5 rounded-md text-[var(--color-base-content)]/60 hover:text-[var(--color-base-content)] hover:bg-[var(--color-base-content)]/10 transition-colors duration-150"
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
          class="hidden lg:flex items-center justify-center w-full mt-3 p-1.5 rounded-md text-[var(--color-base-content)]/60 hover:text-[var(--color-base-content)] hover:bg-[var(--color-base-content)]/10 transition-colors duration-150"
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
            aria-label={t('navigation.dashboard')}
            class={cn(
              menuItemBase,
              menuItemCollapsed,
              routeCache.dashboard ? menuItemActive : menuItemDefault
            )}
            aria-current={routeCache.dashboard ? 'page' : undefined}
          >
            <LayoutDashboard class="size-5 shrink-0" />
            {#if !isCollapsed}
              <span>{t('navigation.dashboard')}</span>
            {/if}
          </button>
        </div>

        <!-- Live Stream -->
        {#if hasLiveAudioAccess()}
          <div class="relative">
            <button
              onclick={() => navigate(navigationUrls.liveStream)}
              onmouseenter={e => isCollapsed && showTooltip(e, t('spectrogram.page.title'))}
              onmouseleave={hideTooltip}
              aria-label={t('spectrogram.page.title')}
              class={cn(
                menuItemBase,
                menuItemCollapsed,
                routeCache.liveStream ? menuItemActive : menuItemDefault
              )}
              aria-current={routeCache.liveStream ? 'page' : undefined}
              role="menuitem"
            >
              <Radio class="size-5 shrink-0" />
              {#if !isCollapsed}
                <span>{t('spectrogram.page.title')}</span>
              {/if}
            </button>
          </div>
        {/if}

        <!-- Analytics (Collapsible) -->
        <CollapsibleNavSection
          icon={BarChart3}
          label={t('navigation.analytics')}
          ariaLabel={t('navigation.analyticsSubmenu')}
          items={analyticsItems}
          {isCollapsed}
          expanded={analyticsExpanded}
          routeActive={routeCache.analytics}
          {routeCache}
          onToggleExpanded={() => (analyticsExpanded = !analyticsExpanded)}
          onNavigate={navigate}
          {showTooltip}
          {hideTooltip}
          {activeFlyout}
          sectionId="analytics"
          onToggleFlyout={toggleFlyout}
        />

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
          <div class="my-2 border-t border-[var(--color-base-200)]/50"></div>

          <!-- System (Collapsible) -->
          <CollapsibleNavSection
            icon={Cpu}
            label={t('navigation.system')}
            ariaLabel={t('navigation.systemSubmenu')}
            items={systemItems}
            {isCollapsed}
            expanded={systemExpanded}
            routeActive={routeCache.system}
            {routeCache}
            onToggleExpanded={() => (systemExpanded = !systemExpanded)}
            onNavigate={navigate}
            {showTooltip}
            {hideTooltip}
            {activeFlyout}
            sectionId="system"
            onToggleFlyout={toggleFlyout}
          />

          <!-- Help (Collapsible) -->
          <CollapsibleNavSection
            icon={CircleHelp}
            label={t('navigation.help')}
            ariaLabel={t('navigation.helpSubmenu')}
            items={helpItems}
            {isCollapsed}
            expanded={helpExpanded}
            routeActive={routeCache.help}
            {routeCache}
            onToggleExpanded={() => (helpExpanded = !helpExpanded)}
            onNavigate={navigate}
            {showTooltip}
            {hideTooltip}
            {activeFlyout}
            sectionId="help"
            onToggleFlyout={toggleFlyout}
          />

          <!-- Settings (Collapsible) -->
          <CollapsibleNavSection
            icon={Settings}
            label={t('navigation.settings')}
            ariaLabel={t('navigation.settingsSubmenu')}
            items={settingsItems}
            {isCollapsed}
            expanded={settingsExpanded}
            routeActive={routeCache.settings}
            {routeCache}
            onToggleExpanded={() => (settingsExpanded = !settingsExpanded)}
            onNavigate={navigate}
            {showTooltip}
            {hideTooltip}
            {activeFlyout}
            sectionId="settings"
            onToggleFlyout={toggleFlyout}
          />
        {/if}
      </div>
    </div>

    <!-- Footer -->
    <div
      class={cn(
        'flex-none py-4 border-t border-[var(--color-base-200)]/50',
        isCollapsed ? 'px-2' : 'px-3'
      )}
    >
      {#if securityEnabled}
        {#if accessAllowed}
          <div class="relative">
            <button
              onclick={handleLogout}
              onmouseenter={e => isCollapsed && showTooltip(e, t('auth.logout'))}
              onmouseleave={hideTooltip}
              class={cn(
                'flex items-center gap-2 w-full px-3 py-2 rounded-lg text-sm font-medium text-[var(--color-base-content)]/90 hover:text-[var(--color-base-content)] hover:bg-[var(--color-base-content)]/5 transition-colors duration-150',
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
                'flex items-center gap-2 w-full px-3 py-2 rounded-lg text-sm font-medium text-[var(--color-base-content)]/90 hover:text-[var(--color-base-content)] hover:bg-[var(--color-base-content)]/5 transition-colors duration-150',
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
            href={appState.projectLinks.repoUrl}
            target="_blank"
            rel="noopener noreferrer"
            class="text-xs text-[var(--color-base-content)]/60 hover:text-[var(--color-base-content)]/80 transition-colors duration-150"
            aria-label={t('navigation.viewOnGithubAriaLabel')}
          >
            {version}
          </a>
        </div>
      {/if}
    </div>
  </nav>

  <!-- Fixed-position tooltip for collapsed sidebar (escapes overflow containers).
       z-[210] keeps it above the drawer (z-[200] = Z_INDEX.SIDEBAR_DRAWER): the
       collapsed state persists in localStorage and can carry over to a mobile
       viewport, where the drawer is raised to z-[200] and would otherwise hide it. -->
  {#if tooltipVisible && isCollapsed}
    <div
      class="sidebar-tooltip fixed px-2 py-1 bg-[var(--color-base-300)] text-[var(--color-base-content)] text-sm rounded shadow-lg pointer-events-none whitespace-nowrap z-[210] -translate-y-1/2"
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
  redirectUrl={loginRedirectUrl}
  {authConfig}
/>

<style>
  /* Left-pointing arrow on sidebar tooltips */
  .sidebar-tooltip::before {
    content: '';
    position: absolute;
    top: 50%;
    left: -5px;
    transform: translateY(-50%);
    width: 0;
    height: 0;
    border-top: 5px solid transparent;
    border-bottom: 5px solid transparent;
    border-right: 5px solid var(--color-base-300);
  }
</style>

<!--
DesktopSidebar.svelte - Main navigation sidebar for desktop layout

Purpose:
- Primary navigation menu for the desktop interface
- Handles authentication state display (login/logout)
- Manages route navigation with special behaviors for specific routes

Features:
- Hierarchical navigation with collapsible sections
- Performance-optimized with cached route calculations
- Dashboard navigation resets date selection to current date
- Authentication integration with login/logout functionality
- Responsive drawer behavior for smaller screens
- Version display with GitHub repository link

Special Behaviors:
- Dashboard Link: When clicking the dashboard navigation link (home or "Dashboard" menu item),
  automatically resets the date persistence to show current date. This ensures users always
  see today's data when explicitly navigating to the dashboard.

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
  } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { resetDateToToday } from '$lib/utils/datePersistence';
  import LoginModal from '../components/modals/LoginModal.svelte';

  interface Props {
    securityEnabled?: boolean;
    accessAllowed?: boolean;
    version?: string;
    currentRoute?: string;
    onNavigate?: (_url: string) => void;
    className?: string;
    authConfig?: {
      basicEnabled: boolean;
      googleEnabled: boolean;
      githubEnabled: boolean;
    };
  }

  let {
    securityEnabled = false,
    accessAllowed = true,
    version = 'Development Build',
    currentRoute = '/ui/dashboard',
    onNavigate,
    className = '',
    authConfig = { basicEnabled: true, googleEnabled: false, githubEnabled: false },
  }: Props = $props();

  // State for login modal and collapsible sections
  let showLoginModal = $state(false);
  let analyticsExpanded = $state(false);
  let settingsExpanded = $state(false);

  // Get actual route from window.location for accurate highlighting
  // Falls back to currentRoute prop if window is not available
  let actualRoute = $derived.by(() => {
    if (typeof window !== 'undefined') {
      return window.location.pathname;
    }
    return currentRoute;
  });

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
    settings: actualRoute.startsWith('/ui/settings'),
    settingsMain: actualRoute === '/ui/settings/main',
    settingsAudio: actualRoute === '/ui/settings/audio',
    settingsFilters: actualRoute.startsWith('/ui/settings/detectionfilters'),
    settingsIntegrations: actualRoute === '/ui/settings/integrations',
    settingsSecurity: actualRoute === '/ui/settings/security',
    settingsSpecies: actualRoute === '/ui/settings/species',
    settingsNotifications: actualRoute === '/ui/settings/notifications',
    settingsSupport: actualRoute === '/ui/settings/support',
  }));

  // Auto-expand sections when route matches
  $effect(() => {
    if (routeCache.analytics) analyticsExpanded = true;
    if (routeCache.settings) settingsExpanded = true;
  });

  // PERFORMANCE OPTIMIZATION: Cache navigation URL transformations
  let navigationUrls = $derived({
    dashboard: onNavigate ? '/' : '/ui/dashboard',
    analytics: onNavigate ? '/analytics' : '/ui/analytics',
    analyticsAdvanced: '/ui/analytics/advanced',
    analyticsSpecies: onNavigate ? '/analytics/species' : '/ui/analytics/species',
    search: onNavigate ? '/search' : '/ui/search',
    about: onNavigate ? '/about' : '/ui/about',
    system: onNavigate ? '/system' : '/ui/system',
    settingsMain: onNavigate ? '/settings/main' : '/ui/settings/main',
    settingsAudio: onNavigate ? '/settings/audio' : '/ui/settings/audio',
    settingsFilters: onNavigate ? '/settings/detectionfilters' : '/ui/settings/detectionfilters',
    settingsIntegrations: onNavigate ? '/settings/integrations' : '/ui/settings/integrations',
    settingsSecurity: onNavigate ? '/settings/security' : '/ui/settings/security',
    settingsSpecies: onNavigate ? '/settings/species' : '/ui/settings/species',
    settingsNotifications: onNavigate ? '/settings/notifications' : '/ui/settings/notifications',
    settingsSupport: onNavigate ? '/settings/support' : '/ui/settings/support',
  });

  function navigate(url: string) {
    if (url === navigationUrls.dashboard) {
      resetDateToToday();
    }
    if (onNavigate) {
      onNavigate(url);
    } else {
      window.location.href = url;
    }
  }

  async function handleLogout() {
    await authStore.logout();
  }

  function handleLogin() {
    showLoginModal = true;
  }

  // Shared styles for menu items - inspired by modern sidebar designs
  const menuItemBase =
    'flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm font-medium transition-all duration-150 w-full text-left';
  const menuItemDefault = 'text-base-content/80 hover:text-base-content hover:menu-hover';
  const menuItemActive = 'menu-item-active';
</script>

<aside class={cn('drawer-side z-10', className)} aria-label={t('navigation.mainNavigation')}>
  <label for="my-drawer" class="drawer-overlay" aria-label={t('navigation.closeSidebar')}></label>

  <nav class="flex flex-col h-dvh w-64 bg-base-100 border-r border-base-200/50">
    <!-- Logo Header -->
    <div class="flex-none px-4 py-5 border-b border-base-200/50">
      <button
        onclick={() => navigate(navigationUrls.dashboard)}
        class="flex items-center gap-3 group"
        aria-label="BirdNET-Go Home"
      >
        <img
          src="/assets/images/logo.png"
          alt="BirdNET-Go Logo"
          class="h-9 w-9 rounded-lg shadow-sm"
        />
        <span class="text-xl font-bold tracking-tight text-base-content">BirdNET-Go</span>
      </button>
    </div>

    <!-- Navigation Menu -->
    <div class="flex-1 overflow-y-auto px-3 py-4">
      <div class="flex flex-col gap-1" role="navigation">
        <!-- Dashboard -->
        <button
          onclick={() => navigate(navigationUrls.dashboard)}
          class={cn(menuItemBase, routeCache.dashboard ? menuItemActive : menuItemDefault)}
          role="menuitem"
          aria-current={routeCache.dashboard ? 'page' : undefined}
        >
          <LayoutDashboard class="size-5 shrink-0" />
          <span>{t('navigation.dashboard')}</span>
        </button>

        <!-- Analytics (Collapsible) -->
        <div class="flex flex-col">
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
            </div>
          {/if}
        </div>

        <!-- Search -->
        <button
          onclick={() => navigate(navigationUrls.search)}
          class={cn(menuItemBase, routeCache.search ? menuItemActive : menuItemDefault)}
          role="menuitem"
        >
          <Search class="size-5 shrink-0" />
          <span>{t('navigation.search')}</span>
        </button>

        <!-- About -->
        <button
          onclick={() => navigate(navigationUrls.about)}
          class={cn(menuItemBase, routeCache.about ? menuItemActive : menuItemDefault)}
          role="menuitem"
        >
          <Info class="size-5 shrink-0" />
          <span>{t('navigation.about')}</span>
        </button>

        {#if !securityEnabled || accessAllowed}
          <!-- Divider -->
          <div class="my-2 border-t border-base-200/50"></div>

          <!-- System -->
          <button
            onclick={() => navigate(navigationUrls.system)}
            class={cn(menuItemBase, routeCache.system ? menuItemActive : menuItemDefault)}
            role="menuitem"
            aria-current={routeCache.system ? 'page' : undefined}
          >
            <Cpu class="size-5 shrink-0" />
            <span>{t('navigation.system')}</span>
          </button>

          <!-- Settings (Collapsible) -->
          <div class="flex flex-col">
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
          </div>
        {/if}
      </div>
    </div>

    <!-- Footer -->
    <div class="flex-none px-3 py-4 border-t border-base-200/50">
      {#if securityEnabled}
        {#if accessAllowed}
          <button
            onclick={handleLogout}
            class="flex items-center justify-center gap-2 w-full px-3 py-2 rounded-lg text-sm font-medium text-base-content/90 hover:text-base-content hover:bg-base-content/5 transition-colors duration-150"
            aria-label={t('auth.logout')}
          >
            <LogOut class="size-4" />
            <span>{t('auth.logout')}</span>
          </button>
        {:else}
          <button
            onclick={handleLogin}
            class="flex items-center justify-center gap-2 w-full px-3 py-2 rounded-lg text-sm font-medium bg-primary text-primary-content hover:bg-primary/90 transition-colors duration-150"
            aria-label={t('auth.openLoginModal')}
          >
            <LogIn class="size-4" />
            <span>{t('auth.login')}</span>
          </button>
        {/if}
      {/if}

      <!-- Version -->
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
    </div>
  </nav>
</aside>

<!-- Login Modal -->
<LoginModal
  isOpen={showLoginModal}
  onClose={() => (showLoginModal = false)}
  redirectUrl={window.location.pathname}
  {authConfig}
/>

<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { auth as authStore } from '$lib/stores/auth';
  import { systemIcons } from '$lib/utils/icons'; // Centralized icons - see icons.ts
  import { t } from '$lib/i18n';

  interface Props {
    securityEnabled?: boolean;
    accessAllowed?: boolean;
    version?: string;
    currentRoute?: string;
    onNavigate?: (_url: string) => void;
    className?: string;
  }

  let {
    securityEnabled = false,
    accessAllowed = true,
    version = 'Development Build',
    currentRoute = '/ui/dashboard',
    onNavigate,
    className = '',
  }: Props = $props();

  // PERFORMANCE OPTIMIZATION: Cache route calculations with $derived
  // Avoids repeated string processing and condition checks in templates
  let routeCache = $derived(() => {
    const routes = {
      dashboard: currentRoute === '/ui/dashboard' || currentRoute === '/ui/',
      analytics: currentRoute.startsWith('/ui/analytics'),
      analyticsExact: currentRoute === '/ui/analytics',
      analyticsSpecies: currentRoute === '/ui/analytics/species',
      search: currentRoute.startsWith('/ui/search'),
      about: currentRoute.startsWith('/ui/about'),
      system: currentRoute.startsWith('/ui/system'),
      settings: currentRoute.startsWith('/ui/settings'),
      settingsMain: currentRoute === '/ui/settings/main',
      settingsAudio: currentRoute === '/ui/settings/audio',
      settingsFilters: currentRoute.startsWith('/ui/settings/detectionfilters'),
      settingsIntegrations: currentRoute === '/ui/settings/integrations',
      settingsSecurity: currentRoute === '/ui/settings/security',
      settingsSpecies: currentRoute === '/ui/settings/species',
      settingsSupport: currentRoute === '/ui/settings/support',
    };
    return routes;
  });

  // PERFORMANCE OPTIMIZATION: Legacy helper functions removed - now using cached routeCache

  // PERFORMANCE OPTIMIZATION: Use $derived for navigation section states
  // Automatically updates when currentRoute changes, eliminating manual $effect
  let analyticsOpen = $derived(routeCache().analytics);
  let settingsOpen = $derived(routeCache().settings);

  // PERFORMANCE OPTIMIZATION: Cache navigation URL transformations with $derived
  // Pre-compute all navigation URLs to avoid repeated string processing
  let navigationUrls = $derived({
    dashboard: onNavigate ? '/' : '/ui/dashboard',
    analytics: onNavigate ? '/analytics' : '/ui/analytics',
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
    settingsSupport: onNavigate ? '/settings/support' : '/ui/settings/support',
  });

  // PERFORMANCE OPTIMIZATION: Simplified navigation function using cached URLs
  function navigate(url: string) {
    if (onNavigate) {
      onNavigate(url);
    } else {
      // All URLs are pre-computed in navigationUrls cache
      // Direct assignment without string processing since we always pass proper URLs
      window.location.href = url;
    }
  }

  // PERFORMANCE OPTIMIZATION: All navigation now uses cached URLs from navigationUrls
  // Eliminates repeated string processing and URL transformations in templates

  // Handle logout
  async function handleLogout() {
    await authStore.logout();
  }

  // Handle login
  function handleLogin() {
    // TODO: Open login modal - implement login modal
  }
</script>

<aside class={cn('drawer-side z-10', className)} aria-label={t('navigation.mainNavigation')}>
  <label for="my-drawer" class="drawer-overlay" aria-label={t('navigation.closeSidebar')}></label>

  <nav
    class="flex flex-col h-[100dvh] w-64 bg-base-100 absolute inset-y-0 sm:static sm:h-full overflow-y-auto p-4"
  >
    <!-- Header -->
    <div class="flex-none p-4">
      <button
        onclick={() => navigate(navigationUrls.dashboard)}
        class="flex items-center gap-2 font-black text-2xl"
        aria-label="BirdNET-Go Home"
      >
        BirdNET-Go
        <img
          src="/assets/images/logo.png"
          alt="BirdNET-Go Logo"
          class="absolute h-10 w-10 right-5 mr-2"
        />
      </button>
    </div>

    <!-- Scrollable menu section -->
    <div class="flex-1 overflow-y-auto px-4">
      <ul class="menu menu-md" role="menubar">
        <li role="none">
          <button
            onclick={() => navigate(navigationUrls.dashboard)}
            class={cn('flex items-center gap-2', { active: routeCache().dashboard })}
            role="menuitem"
          >
            {@html systemIcons.home}
            <span>{t('navigation.dashboard')}</span>
          </button>
        </li>

        <li role="none">
          <details bind:open={analyticsOpen}>
            <summary class="flex items-center gap-2" role="menuitem" aria-haspopup="true">
              {@html systemIcons.analytics}
              <span>{t('navigation.analytics')}</span>
            </summary>
            <ul role="menu" aria-label={t('navigation.analyticsSubmenu')}>
              <li role="none">
                <button
                  onclick={() => navigate(navigationUrls.analytics)}
                  class={cn({ active: routeCache().analyticsExact })}
                  role="menuitem"
                >
                  {t('analytics.title')}
                </button>
              </li>
              <li role="none">
                <button
                  onclick={() => navigate(navigationUrls.analyticsSpecies)}
                  class={cn({ active: routeCache().analyticsSpecies })}
                  role="menuitem"
                >
                  {t('analytics.species.title')}
                </button>
              </li>
            </ul>
          </details>
        </li>

        <li role="none">
          <button
            onclick={() => navigate(navigationUrls.search)}
            class={cn('flex items-center gap-2', { active: routeCache().search })}
            role="menuitem"
          >
            {@html systemIcons.search}
            <span>{t('navigation.search')}</span>
          </button>
        </li>

        <li role="none">
          <button
            onclick={() => navigate(navigationUrls.about)}
            class={cn('flex items-center gap-2', { active: routeCache().about })}
            role="menuitem"
          >
            {@html systemIcons.about}
            <span>{t('navigation.about')}</span>
          </button>
        </li>

        {#if !securityEnabled || accessAllowed}
          <li role="none">
            <button
              onclick={() => navigate(navigationUrls.system)}
              class={cn('flex items-center gap-2', { active: routeCache().system })}
              role="menuitem"
              aria-label="System dashboard"
              aria-current={routeCache().system ? 'page' : undefined}
            >
              {@html systemIcons.system}
              <span>{t('navigation.system')}</span>
            </button>
          </li>

          <li role="none">
            <details bind:open={settingsOpen}>
              <summary class="flex items-center gap-2" role="menuitem" aria-haspopup="true">
                {@html systemIcons.settingsGear}
                <span>{t('navigation.settings')}</span>
              </summary>
              <ul role="menu" aria-label={t('navigation.settingsSubmenu')}>
                <li role="none">
                  <button
                    onclick={() => navigate(navigationUrls.settingsMain)}
                    class={cn({ active: routeCache().settingsMain })}
                    role="menuitem"
                  >
                    {t('settings.sections.node')}
                  </button>
                </li>
                <li role="none">
                  <button
                    onclick={() => navigate(navigationUrls.settingsAudio)}
                    class={cn({ active: routeCache().settingsAudio })}
                    role="menuitem"
                  >
                    {t('settings.sections.audio')}
                  </button>
                </li>
                <li role="none">
                  <button
                    onclick={() => navigate(navigationUrls.settingsFilters)}
                    class={cn({ active: routeCache().settingsFilters })}
                    role="menuitem"
                  >
                    {t('settings.sections.filters')}
                  </button>
                </li>
                <li role="none">
                  <button
                    onclick={() => navigate(navigationUrls.settingsIntegrations)}
                    class={cn({ active: routeCache().settingsIntegrations })}
                    role="menuitem"
                  >
                    {t('settings.sections.integration')}
                  </button>
                </li>
                <li role="none">
                  <button
                    onclick={() => navigate(navigationUrls.settingsSecurity)}
                    class={cn({ active: routeCache().settingsSecurity })}
                    role="menuitem"
                  >
                    {t('settings.sections.security')}
                  </button>
                </li>
                <li role="none">
                  <button
                    onclick={() => navigate(navigationUrls.settingsSpecies)}
                    class={cn({ active: routeCache().settingsSpecies })}
                    role="menuitem"
                  >
                    {t('settings.sections.species')}
                  </button>
                </li>
                <li role="none">
                  <button
                    onclick={() => navigate(navigationUrls.settingsSupport)}
                    class={cn({ active: routeCache().settingsSupport })}
                    role="menuitem"
                  >
                    {t('settings.sections.support')}
                  </button>
                </li>
              </ul>
            </details>
          </li>
        {/if}
      </ul>
    </div>

    <!-- Footer section -->
    <div class="flex-none border-base-200">
      <div class="p-4 flex flex-col gap-4">
        {#if securityEnabled}
          {#if accessAllowed}
            <!-- Logout section -->
            <div class="flex flex-col gap-2">
              <button
                onclick={handleLogout}
                class="btn btn-sm justify-center w-full"
                aria-label={t('auth.logout')}
              >
                {@html systemIcons.logout}
                <span>{t('auth.logout')}</span>
              </button>
            </div>
          {:else}
            <!-- Login section -->
            <button
              onclick={handleLogin}
              class="btn btn-sm justify-center w-full"
              aria-label={t('auth.openLoginModal')}
            >
              {@html systemIcons.login}
              <span>{t('auth.login')}</span>
            </button>
          {/if}
        {/if}

        <!-- Version number -->
        <div class="text-center text-xs text-base-content/60 text-gray-500" role="contentinfo">
          <span class="inline-flex items-center gap-1">
            {version}
          </span>
        </div>
      </div>
    </div>
  </nav>
</aside>

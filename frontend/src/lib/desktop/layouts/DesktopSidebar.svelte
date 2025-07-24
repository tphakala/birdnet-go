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

  // Route active state helpers
  function isRouteActive(route: string): boolean {
    const uiRoute = route.startsWith('/ui/') ? route : `/ui${route}`;
    return currentRoute.startsWith(uiRoute);
  }

  function isExactRouteActive(route: string): boolean {
    const uiRoute = route.startsWith('/ui/') ? route : `/ui${route === '/' ? '/dashboard' : route}`;
    return currentRoute === uiRoute;
  }

  // Navigation sections open state
  let analyticsOpen = $state(isRouteActive('/analytics'));
  let settingsOpen = $state(isRouteActive('/settings'));

  // Update open states when route changes
  $effect(() => {
    analyticsOpen = isRouteActive('/analytics');
    settingsOpen = isRouteActive('/settings');
  });

  // Handle navigation
  function navigate(url: string) {
    if (onNavigate) {
      onNavigate(url);
    } else {
      // Default navigation - convert to /ui/ routes
      const uiUrl = url.startsWith('/ui/') ? url : `/ui${url === '/' ? '/dashboard' : url}`;
      window.location.href = uiUrl;
    }
  }

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
        onclick={() => navigate('/')}
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
            onclick={() => navigate('/')}
            class={cn('flex items-center gap-2', { active: isExactRouteActive('/') })}
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
                  onclick={() => navigate('/analytics')}
                  class={cn({ active: isExactRouteActive('/analytics') })}
                  role="menuitem"
                >
                  {t('analytics.title')}
                </button>
              </li>
              <li role="none">
                <button
                  onclick={() => navigate('/analytics/species')}
                  class={cn({ active: isExactRouteActive('/analytics/species') })}
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
            onclick={() => navigate('/search')}
            class={cn('flex items-center gap-2', { active: isRouteActive('/search') })}
            role="menuitem"
          >
            {@html systemIcons.search}
            <span>{t('navigation.search')}</span>
          </button>
        </li>

        <li role="none">
          <button
            onclick={() => navigate('/about')}
            class={cn('flex items-center gap-2', { active: isRouteActive('/about') })}
            role="menuitem"
          >
            {@html systemIcons.about}
            <span>{t('navigation.about')}</span>
          </button>
        </li>

        {#if !securityEnabled || accessAllowed}
          <li role="none">
            <button
              onclick={() => navigate('/system')}
              class={cn('flex items-center gap-2', { active: isRouteActive('/system') })}
              role="menuitem"
              aria-label="System dashboard"
              aria-current={isRouteActive('/system') ? 'page' : undefined}
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
                    onclick={() => navigate('/settings/main')}
                    class={cn({ active: isExactRouteActive('/settings/main') })}
                    role="menuitem"
                  >
                    {t('settings.sections.node')}
                  </button>
                </li>
                <li role="none">
                  <button
                    onclick={() => navigate('/settings/audio')}
                    class={cn({ active: isExactRouteActive('/settings/audio') })}
                    role="menuitem"
                  >
                    {t('settings.sections.audio')}
                  </button>
                </li>
                <li role="none">
                  <button
                    onclick={() => navigate('/settings/detectionfilters')}
                    class={cn({ active: isRouteActive('/settings/detectionfilters') })}
                    role="menuitem"
                  >
                    {t('settings.sections.filters')}
                  </button>
                </li>
                <li role="none">
                  <button
                    onclick={() => navigate('/settings/integrations')}
                    class={cn({ active: isExactRouteActive('/settings/integrations') })}
                    role="menuitem"
                  >
                    {t('settings.sections.integration')}
                  </button>
                </li>
                <li role="none">
                  <button
                    onclick={() => navigate('/settings/security')}
                    class={cn({ active: isExactRouteActive('/settings/security') })}
                    role="menuitem"
                  >
                    {t('settings.sections.security')}
                  </button>
                </li>
                <li role="none">
                  <button
                    onclick={() => navigate('/settings/species')}
                    class={cn({ active: isExactRouteActive('/settings/species') })}
                    role="menuitem"
                  >
                    {t('settings.sections.species')}
                  </button>
                </li>
                <li role="none">
                  <button
                    onclick={() => navigate('/settings/support')}
                    class={cn({ active: isExactRouteActive('/settings/support') })}
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

<script lang="ts">
  import { onMount } from 'svelte';
  import RootLayout from './lib/desktop/layouts/RootLayout.svelte';
  import DashboardPage from './lib/desktop/features/dashboard/pages/DashboardPage.svelte'; // Keep dashboard for initial load
  import type { Component } from 'svelte';
  import { getLogger } from './lib/utils/logger';
  import { createSafeMap } from './lib/utils/security';
  import { sseNotifications } from './lib/stores/sseNotifications'; // Initialize SSE toast handler
  import { t } from './lib/i18n';
  import { appState, initApp, MAX_RETRIES } from './lib/stores/appState.svelte';
  import { navigation } from './lib/stores/navigation.svelte';
  import { settingsActions } from './lib/stores/settings.js';

  const logger = getLogger('app');

  /**
   * Client-side navigation function.
   * Updates URL via History API. Route handling is triggered reactively
   * by the $effect watching navigation.currentPath.
   */
  function navigate(url: string): void {
    navigation.navigate(url);
  }

  // Dynamic imports for heavy pages - properly typed component references
  let Analytics = $state<Component | null>(null);
  let AdvancedAnalytics = $state<Component | null>(null);
  let Species = $state<Component | null>(null);
  let Search = $state<Component | null>(null);
  let About = $state<Component | null>(null);
  let System = $state<Component | null>(null);
  let Settings = $state<Component | null>(null);
  let Notifications = $state<Component | null>(null);
  let Detections = $state<Component | null>(null);
  let DetectionDetail = $state<Component | null>(null);
  let ErrorPage = $state<Component | null>(null);
  let ServerErrorPage = $state<Component | null>(null);
  let GenericErrorPage = $state<any>(null);

  let currentRoute = $state<string>('');
  let currentPage = $state<string>('');
  let pageTitleKey = $state<string>('navigation.dashboard');
  let loadingComponent = $state<boolean>(false);

  // Track the last path we routed to, to avoid duplicate routing
  let lastRoutedPath = $state<string | null>(null);

  // Derived translated title - automatically updates when language changes
  let pageTitle = $derived(t(pageTitleKey));
  let dynamicErrorCode = $state<string | null>(null);
  let detectionId = $state<string | null>(null);

  // Configuration derived from centralized appState
  let securityEnabled = $derived(appState.security.enabled);
  let accessAllowed = $derived(appState.security.accessAllowed);
  let version = $derived(appState.version);
  let authConfig = $derived(appState.security.authConfig);

  // App initialization state
  let appInitialized = $derived(appState.initialized);
  let appLoading = $derived(appState.loading);
  let appError = $derived(appState.error);

  // Route configuration for better maintainability
  interface RouteConfig {
    route: string;
    page: string;
    titleKey: string;
    component: string;
  }

  const routeConfigs: RouteConfig[] = [
    { route: 'dashboard', page: 'dashboard', titleKey: 'navigation.dashboard', component: '' },
    {
      route: 'notifications',
      page: 'notifications',
      titleKey: 'navigation.notifications',
      component: 'notifications',
    },
    {
      route: 'species',
      page: 'analytics/species',
      titleKey: 'pageTitle.speciesAnalytics',
      component: 'species',
    },
    {
      route: 'analytics',
      page: 'analytics',
      titleKey: 'navigation.analytics',
      component: 'analytics',
    },
    {
      route: 'advanced-analytics',
      page: 'analytics/advanced',
      titleKey: 'pageTitle.advancedAnalytics',
      component: 'advanced-analytics',
    },
    { route: 'search', page: 'search', titleKey: 'navigation.search', component: 'search' },
    {
      route: 'detections',
      page: 'detections',
      titleKey: 'navigation.detections',
      component: 'detections',
    },
    {
      route: 'detection-detail',
      page: 'detection-detail',
      titleKey: 'pageTitle.detectionDetails',
      component: 'detection-detail',
    },
    { route: 'about', page: 'about', titleKey: 'navigation.about', component: 'about' },
    { route: 'system', page: 'system', titleKey: 'navigation.system', component: 'system' },
    { route: 'settings', page: 'settings', titleKey: 'navigation.settings', component: 'settings' },
  ];

  // Settings subpage title keys
  const settingsSubpages: Record<string, string> = {
    '/main': 'settings.sections.node',
    '/userinterface': 'settings.sections.userinterface',
    '/audio': 'settings.sections.audio',
    '/detectionfilters': 'settings.sections.filters',
    '/integrations': 'settings.sections.integration',
    '/security': 'settings.sections.security',
    '/species': 'settings.sections.species',
    '/notifications': 'settings.sections.notifications',
    '/alertrules': 'settings.sections.alertrules',
    '/support': 'settings.sections.support',
  };

  // System subpage title keys
  const systemSubpages: Record<string, string> = {
    '/database': 'system.sections.database',
    '/terminal': 'system.sections.terminal',
  };

  // Dynamic import helper
  async function loadComponent(route: string): Promise<void> {
    if (loadingComponent) return;
    loadingComponent = true;

    try {
      switch (route) {
        case 'analytics':
          if (!Analytics) {
            const module = await import('./lib/desktop/features/analytics/pages/Analytics.svelte');
            Analytics = module.default;
          }
          break;
        case 'advanced-analytics':
          if (!AdvancedAnalytics) {
            const module =
              await import('./lib/desktop/features/analytics/pages/AdvancedAnalytics.svelte');
            AdvancedAnalytics = module.default;
          }
          break;
        case 'species':
          if (!Species) {
            const module = await import('./lib/desktop/features/analytics/pages/Species.svelte');
            Species = module.default;
          }
          break;
        case 'search':
          if (!Search) {
            const module = await import('./lib/desktop/views/Search.svelte');
            Search = module.default;
          }
          break;
        case 'about':
          if (!About) {
            const module = await import('./lib/desktop/views/About.svelte');
            About = module.default;
          }
          break;
        case 'system':
          if (!System) {
            const module = await import('./lib/desktop/views/System.svelte');
            System = module.default;
          }
          break;
        case 'settings':
          if (!Settings) {
            const module = await import('./lib/desktop/views/Settings.svelte');
            Settings = module.default;
          }
          break;
        case 'notifications':
          if (!Notifications) {
            const module = await import('./lib/desktop/views/Notifications.svelte');
            Notifications = module.default;
          }
          break;
        case 'detections':
          if (!Detections) {
            const module = await import('./lib/desktop/views/Detections.svelte');
            Detections = module.default;
          }
          break;
        case 'detection-detail':
          if (!DetectionDetail) {
            const module = await import('./lib/desktop/views/DetectionDetail.svelte');
            DetectionDetail = module.default;
          }
          break;
        case 'error-404':
          if (!ErrorPage) {
            const module = await import('./lib/desktop/views/ErrorPage.svelte');
            ErrorPage = module.default;
          }
          break;
        case 'error-500':
          if (!ServerErrorPage) {
            const module = await import('./lib/desktop/views/ServerErrorPage.svelte');
            ServerErrorPage = module.default;
          }
          break;
        case 'error-generic':
          if (!GenericErrorPage) {
            const module = await import('./lib/desktop/views/GenericErrorPage.svelte');
            GenericErrorPage = module.default;
          }
          break;
      }
    } catch (error) {
      logger.error(`Failed to load component for route "${route}"`, error, {
        component: 'App',
        action: 'loadComponent',
        route,
      });
      // Fall back to generic error page on component load failure
      currentRoute = 'error-generic';
      currentPage = 'error-generic';
      pageTitleKey = 'pageTitle.componentError';
      dynamicErrorCode = '500';
      // Try to load the generic error component if it hasn't been loaded yet
      if (!GenericErrorPage) {
        try {
          const module = await import('./lib/desktop/views/GenericErrorPage.svelte');
          GenericErrorPage = module.default;
        } catch (fallbackError) {
          logger.error('Failed to load fallback error component', fallbackError, {
            component: 'App',
            action: 'loadFallbackError',
          });
        }
      }
    } finally {
      loadingComponent = false;
    }
  }

  // Helper function to safely find route configs
  function findRouteConfig(route: string): RouteConfig | undefined {
    return routeConfigs.find(r => r.route === route);
  }

  // Build UI paths immune to nginx sub_filter text rewriting.
  // sub_filter rewrites quoted strings like '/ui/...' but can't match array joins.
  function uiPath(...segments: string[]): string {
    return ['', 'ui', ...segments].join('/');
  }

  // Regex patterns for prefix matching (immune to sub_filter)
  const UI_DETECTIONS_PREFIX_RE = /^\/ui\/detections\//;
  const UI_SYSTEM_PREFIX_RE = /^\/ui\/system\//;
  const UI_SETTINGS_PREFIX_RE = /^\/ui\/settings\//;

  // Route path to config mapping - using Map for safe lookups.
  // Keys use computed uiPath() to avoid sub_filter corruption.
  const pathToRouteMap = createSafeMap<RouteConfig | undefined>({
    '/': findRouteConfig('dashboard'),
    [uiPath() + '/']: findRouteConfig('dashboard'),
    [uiPath()]: findRouteConfig('dashboard'),
    [uiPath('dashboard')]: findRouteConfig('dashboard'),
    [uiPath('notifications')]: findRouteConfig('notifications'),
    [uiPath('analytics', 'species')]: findRouteConfig('species'),
    [uiPath('analytics', 'advanced')]: findRouteConfig('advanced-analytics'),
    [uiPath('analytics')]: findRouteConfig('analytics'),
    [uiPath('search')]: findRouteConfig('search'),
    [uiPath('detections')]: findRouteConfig('detections'),
    [uiPath('about')]: findRouteConfig('about'),
    [uiPath('system')]: findRouteConfig('system'),
    [uiPath('settings')]: findRouteConfig('settings'),
  });

  /**
   * Handles routing for sections with subpages (e.g., system, settings).
   * Finds the route config, sets the current route/page/title, and loads the component.
   * Falls back to a 404 error page if the route config is not found.
   */
  function handleSubpageRouting(
    path: string,
    routeName: string,
    subpagesMap: Record<string, string>,
    errorTitleKey: string
  ): void {
    const config = findRouteConfig(routeName);
    if (config) {
      currentRoute = config.route;
      currentPage = config.page;
      pageTitleKey = config.titleKey;

      for (const [subpath, titleKey] of Object.entries(subpagesMap)) {
        if (path.endsWith(subpath)) {
          pageTitleKey = titleKey;
          break;
        }
      }

      if (config.component) {
        loadComponent(config.component);
      }
    } else {
      currentRoute = 'error-404';
      currentPage = 'error-404';
      pageTitleKey = errorTitleKey;
      loadComponent('error-404');
    }
  }

  function handleRouting(path: string): void {
    // Special handling for detection detail pages
    if (UI_DETECTIONS_PREFIX_RE.test(path) && path.split('/').length > 3) {
      const pathParts = path.split('/');
      const id = pathParts[3];
      if (id && !isNaN(Number(id))) {
        detectionId = id;
        currentRoute = 'detection-detail';
        currentPage = 'detection-detail';
        pageTitleKey = 'pageTitle.detectionDetails';
        loadComponent('detection-detail');
        return;
      }
    }

    // Handle system and settings subpages
    if (UI_SYSTEM_PREFIX_RE.test(path)) {
      handleSubpageRouting(path, 'system', systemSubpages, 'pageTitle.systemNotAvailable');
      return;
    }

    if (UI_SETTINGS_PREFIX_RE.test(path)) {
      handleSubpageRouting(path, 'settings', settingsSubpages, 'pageTitle.settingsNotAvailable');
      return;
    }

    // Normal route lookup - using Map.get() for safe access
    const routeConfig = pathToRouteMap.get(path);
    if (routeConfig) {
      currentRoute = routeConfig.route;
      currentPage = routeConfig.page;
      pageTitleKey = routeConfig.titleKey;

      if (routeConfig.component) {
        loadComponent(routeConfig.component);
      }
      return;
    }

    // Handle error pages or unknown routes
    const urlParams = new URLSearchParams(window.location.search);
    const errorCode = urlParams.get('error');

    if (errorCode === '404') {
      currentRoute = 'error-404';
      currentPage = 'error-404';
      pageTitleKey = 'pageTitle.pageNotFound';
      loadComponent('error-404');
    } else if (errorCode === '500') {
      currentRoute = 'error-500';
      currentPage = 'error-500';
      pageTitleKey = 'pageTitle.serverError';
      loadComponent('error-500');
    } else if (errorCode) {
      currentRoute = 'error-generic';
      currentPage = 'error-generic';
      // For dynamic error titles from URL, we use a generic error key
      pageTitleKey = 'common.error';
      dynamicErrorCode = errorCode || '500';
      loadComponent('error-generic');
    } else {
      // Unknown route, default to 404
      currentRoute = 'error-404';
      currentPage = 'error-404';
      pageTitleKey = 'pageTitle.pageNotFound';
      loadComponent('error-404');
    }
  }

  onMount(async () => {
    // Initialize application configuration from API with retry logic
    const success = await initApp();

    if (!success) {
      // Fatal initialization error - appState.error will contain the message
      logger.error('App initialization failed after all retries', {
        error: appState.error,
      });
      // The template will show the error page based on appError state
      return;
    }

    // Ensure SSE notifications manager is connected (it auto-connects on import)
    // This prevents tree-shaking and ensures toast messages work properly
    if (sseNotifications) {
      logger.debug('SSE notifications manager initialized');
    }

    // Load settings at app startup so they are available on any page the user navigates to
    // first (e.g. System → Terminal) without requiring a visit to the Settings page.
    settingsActions.loadSettings().catch(err => {
      logger.error('Failed to load settings on app init', err);
    });

    // Initial routing is handled by the reactive $effect below when appInitialized becomes true
  });

  // Reactive routing: automatically handle route changes when navigation.currentPath updates.
  // This handles both initial routing (when appInitialized becomes true) and subsequent
  // navigation from any component calling navigation.navigate().
  $effect(() => {
    const currentPath = navigation.currentPath;

    // Skip if app isn't initialized yet
    if (!appInitialized) return;

    // Skip if we already routed to this path (prevents duplicate routing)
    if (currentPath === lastRoutedPath) return;

    lastRoutedPath = currentPath;
    handleRouting(currentPath);
  });

  // Use $effect for browser back/forward navigation with automatic cleanup
  $effect(() => {
    const handlePopState = () => {
      navigation.handlePopState();
      // The reactive routing effect above will handle the actual routing
      // when navigation.currentPath updates
    };

    window.addEventListener('popstate', handlePopState);

    return () => {
      window.removeEventListener('popstate', handlePopState);
    };
  });
</script>

{#snippet renderRoute(component: Component | null)}
  {#if component}
    {@const Component = component}
    <Component />
  {/if}
{/snippet}

<!-- Show loading screen during initialization -->
{#if appLoading || (!appInitialized && !appError)}
  <div class="flex h-screen w-full items-center justify-center bg-base-200">
    <div class="flex flex-col items-center gap-4">
      <span class="loading loading-spinner loading-lg text-primary"></span>
      <p class="text-base-content/70">{t('common.loading')}</p>
      {#if appState.retryCount > 0}
        <p class="text-sm text-warning">
          {t('common.retrying')} ({appState.retryCount}/{MAX_RETRIES})...
        </p>
      {/if}
    </div>
  </div>
  <!-- Show fatal error page if initialization failed -->
{:else if appError}
  <div class="flex min-h-screen flex-col items-center justify-center bg-base-200 p-4">
    <div class="card max-w-lg bg-base-100 shadow-xl">
      <div class="card-body items-center text-center">
        <div class="mb-4 text-6xl text-error">500</div>
        <h2 class="card-title text-error">{t('error.server.title')}</h2>
        <p class="text-base-content/70">{t('error.server.description')}</p>
        <div class="mt-4 rounded-lg bg-base-200 p-4 text-left">
          <p class="font-mono text-sm text-error">{appError}</p>
        </div>
        <div class="card-actions mt-6">
          <button class="btn btn-primary" onclick={() => window.location.reload()}>
            {t('common.retry')}
          </button>
        </div>
      </div>
    </div>
  </div>
{:else}
  <RootLayout
    title={pageTitle}
    {currentPage}
    currentPath={navigation.currentPath}
    {securityEnabled}
    {accessAllowed}
    {version}
    {authConfig}
    onNavigate={navigate}
  >
    {#if currentRoute === 'dashboard'}
      <DashboardPage />
    {:else if currentRoute === 'notifications'}
      {@render renderRoute(Notifications)}
    {:else if currentRoute === 'analytics'}
      {@render renderRoute(Analytics)}
    {:else if currentRoute === 'advanced-analytics'}
      {@render renderRoute(AdvancedAnalytics)}
    {:else if currentRoute === 'species'}
      {@render renderRoute(Species)}
    {:else if currentRoute === 'search'}
      {@render renderRoute(Search)}
    {:else if currentRoute === 'about'}
      {@render renderRoute(About)}
    {:else if currentRoute === 'system'}
      {@render renderRoute(System)}
    {:else if currentRoute === 'settings'}
      {@render renderRoute(Settings)}
    {:else if currentRoute === 'detections'}
      {@render renderRoute(Detections)}
    {:else if currentRoute === 'detection-detail'}
      {#if DetectionDetail}
        {@const Component = DetectionDetail}
        <Component {detectionId} />
      {/if}
    {:else if currentRoute === 'error-404'}
      {@render renderRoute(ErrorPage)}
    {:else if currentRoute === 'error-500'}
      {@render renderRoute(ServerErrorPage)}
    {:else if currentRoute === 'error-generic'}
      {#if GenericErrorPage}
        {@const ErrorComponent = GenericErrorPage}
        <ErrorComponent
          code={dynamicErrorCode || '500'}
          title={t('error.generic.componentLoadError')}
          message={t('error.generic.failedToLoadComponent')}
        />
      {/if}
    {/if}
  </RootLayout>
{/if}

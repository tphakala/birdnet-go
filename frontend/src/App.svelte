<script lang="ts">
  import { onMount } from 'svelte';
  import RootLayout from './lib/desktop/layouts/RootLayout.svelte';
  import DashboardPage from './lib/desktop/features/dashboard/pages/DashboardPage.svelte'; // Keep dashboard for initial load
  import type { Component } from 'svelte';
  import type { BirdnetConfig } from './app.d.ts';

  // Dynamic imports for heavy pages - properly typed component references
  let Analytics = $state<Component | null>(null);
  let Species = $state<Component | null>(null);
  let Search = $state<Component | null>(null);
  let About = $state<Component | null>(null);
  let System = $state<Component | null>(null);
  let Settings = $state<Component | null>(null);
  let Notifications = $state<Component | null>(null);
  let Detections = $state<Component | null>(null);
  let ErrorPage = $state<Component | null>(null);
  let ServerErrorPage = $state<Component | null>(null);
  let GenericErrorPage = $state<Component | null>(null);

  let currentRoute = $state<string>('');
  let currentPage = $state<string>('');
  let pageTitle = $state<string>('Dashboard');
  let loadingComponent = $state<boolean>(false);

  // Get configuration from server
  let config = $state<BirdnetConfig | null>(null);
  let securityEnabled = $state<boolean>(false);
  let accessAllowed = $state<boolean>(true);
  let version = $state<string>('Development Build');

  // Route configuration for better maintainability
  interface RouteConfig {
    route: string;
    page: string;
    title: string;
    component: string;
  }

  const routeConfigs: RouteConfig[] = [
    { route: 'dashboard', page: 'dashboard', title: 'Dashboard', component: '' },
    {
      route: 'notifications',
      page: 'notifications',
      title: 'Notifications',
      component: 'notifications',
    },
    {
      route: 'species',
      page: 'analytics/species',
      title: 'Species Analytics',
      component: 'species',
    },
    { route: 'analytics', page: 'analytics', title: 'Analytics', component: 'analytics' },
    { route: 'search', page: 'search', title: 'Search', component: 'search' },
    { route: 'detections', page: 'detections', title: 'Detections', component: 'detections' },
    { route: 'about', page: 'about', title: 'About', component: 'about' },
    { route: 'system', page: 'system', title: 'System', component: 'system' },
    { route: 'settings', page: 'settings', title: 'Settings', component: 'settings' },
  ];

  const settingsSubpages = {
    '/main': 'Main Settings',
    '/audio': 'Audio Settings',
    '/detectionfilters': 'Detection Filters',
    '/integrations': 'Integrations',
    '/security': 'Security Settings',
    '/species': 'Species Settings',
    '/support': 'Support',
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
      console.error(`Failed to load component for route "${route}":`, error);
      // Fall back to generic error page on component load failure
      currentRoute = 'error-generic';
      currentPage = 'error-generic';
      pageTitle = 'Component Load Error';
      // Try to load the generic error component if it hasn't been loaded yet
      if (!GenericErrorPage) {
        try {
          const module = await import('./lib/desktop/views/GenericErrorPage.svelte');
          GenericErrorPage = module.default;
        } catch (fallbackError) {
          console.error('Failed to load fallback error component:', fallbackError);
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

  // Route path to config mapping
  const pathToRouteMap: Record<string, RouteConfig | undefined> = {
    '/ui/': findRouteConfig('dashboard'),
    '/ui': findRouteConfig('dashboard'),
    '/ui/dashboard': findRouteConfig('dashboard'),
    '/ui/notifications': findRouteConfig('notifications'),
    '/ui/analytics/species': findRouteConfig('species'),
    '/ui/analytics': findRouteConfig('analytics'),
    '/ui/search': findRouteConfig('search'),
    '/ui/detections': findRouteConfig('detections'),
    '/ui/about': findRouteConfig('about'),
    '/ui/system': findRouteConfig('system'),
    '/ui/settings': findRouteConfig('settings'),
  };

  function handleRouting(path: string): void {
    // Special handling for settings subpages
    if (path.startsWith('/ui/settings/')) {
      const settingsConfig = findRouteConfig('settings');
      if (settingsConfig) {
        currentRoute = settingsConfig.route;
        currentPage = settingsConfig.page;
        pageTitle = settingsConfig.title;

        // Update title based on specific settings page
        for (const [subpath, title] of Object.entries(settingsSubpages)) {
          if (path.includes(subpath)) {
            pageTitle = title;
            break;
          }
        }

        if (settingsConfig.component) {
          loadComponent(settingsConfig.component);
        }
      } else {
        // Settings config not found, redirect to error page
        currentRoute = 'error-404';
        currentPage = 'error-404';
        pageTitle = 'Settings Not Available';
        loadComponent('error-404');
      }
      return;
    }

    // Normal route lookup
    const routeConfig = pathToRouteMap[path];
    if (routeConfig) {
      currentRoute = routeConfig.route;
      currentPage = routeConfig.page;
      pageTitle = routeConfig.title;

      if (routeConfig.component) {
        loadComponent(routeConfig.component);
      }
      return;
    }

    // Handle error pages or unknown routes
    const urlParams = new URLSearchParams(window.location.search);
    const errorCode = urlParams.get('error');
    const errorTitle = urlParams.get('title');

    if (errorCode === '404') {
      currentRoute = 'error-404';
      currentPage = 'error-404';
      pageTitle = 'Page Not Found';
      loadComponent('error-404');
    } else if (errorCode === '500') {
      currentRoute = 'error-500';
      currentPage = 'error-500';
      pageTitle = 'Internal Server Error';
      loadComponent('error-500');
    } else if (errorCode) {
      currentRoute = 'error-generic';
      currentPage = 'error-generic';
      pageTitle = errorTitle || 'Error';
      loadComponent('error-generic');
    } else {
      // Unknown route, default to 404
      currentRoute = 'error-404';
      currentPage = 'error-404';
      pageTitle = 'Page Not Found';
      loadComponent('error-404');
    }
  }

  onMount(() => {
    // Get server configuration
    config = window.BIRDNET_CONFIG || null;
    securityEnabled = config?.security?.enabled || false;
    accessAllowed = config?.security?.accessAllowed !== false; // Default to true unless explicitly false
    version = config?.version || 'Development Build';

    // Determine current route from URL path
    const path = window.location.pathname;
    handleRouting(path);
  });
</script>

{#snippet loadingSpinner()}
  <div class="flex items-center justify-center h-64">
    <span class="loading loading-spinner loading-lg"></span>
  </div>
{/snippet}

{#snippet renderRoute(component: Component | null)}
  {#if loadingComponent}
    {@render loadingSpinner()}
  {:else if component}
    {@const Component = component}
    <Component />
  {/if}
{/snippet}

<RootLayout title={pageTitle} {currentPage} {securityEnabled} {accessAllowed} {version}>
  {#if currentRoute === 'dashboard'}
    <DashboardPage />
  {:else if currentRoute === 'notifications'}
    {@render renderRoute(Notifications)}
  {:else if currentRoute === 'analytics'}
    {@render renderRoute(Analytics)}
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
  {:else if currentRoute === 'error-404'}
    {@render renderRoute(ErrorPage)}
  {:else if currentRoute === 'error-500'}
    {@render renderRoute(ServerErrorPage)}
  {:else if currentRoute === 'error-generic'}
    {@render renderRoute(GenericErrorPage)}
  {/if}
</RootLayout>

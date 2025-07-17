<script>
  import { onMount } from 'svelte';
  import RootLayout from './lib/desktop/layouts/RootLayout.svelte';
  import DashboardPage from './lib/desktop/features/dashboard/pages/DashboardPage.svelte'; // Keep dashboard for initial load

  // Dynamic imports for heavy pages
  let Analytics = $state(null);
  let Species = $state(null);
  let Search = $state(null);
  let About = $state(null);
  let System = $state(null);
  let Settings = $state(null);
  let Notifications = $state(null);
  let Detections = $state(null);
  let ErrorPage = $state(null);
  let ServerErrorPage = $state(null);
  let GenericErrorPage = $state(null);

  let currentRoute = $state('');
  let currentPage = $state('');
  let pageTitle = $state('Dashboard');
  let loadingComponent = $state(false);

  // Get configuration from server
  let config = $state(null);
  let securityEnabled = $state(false);
  let accessAllowed = $state(true);
  let version = $state('Development Build');

  // Dynamic import helper
  async function loadComponent(route) {
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
    } finally {
      loadingComponent = false;
    }
  }

  onMount(() => {
    // Get server configuration
    config = window.BIRDNET_CONFIG || {};
    securityEnabled = config.security?.enabled || false;
    accessAllowed = config.security?.accessAllowed || true;
    version = config.version || 'Development Build';

    // Determine current route from URL path
    const path = window.location.pathname;
    if (path === '/ui/' || path === '/ui' || path === '/ui/dashboard') {
      currentRoute = 'dashboard';
      currentPage = 'dashboard';
      pageTitle = 'Dashboard';
    } else if (path === '/ui/notifications') {
      currentRoute = 'notifications';
      currentPage = 'notifications';
      pageTitle = 'Notifications';
      loadComponent('notifications');
    } else if (path === '/ui/analytics/species') {
      currentRoute = 'species';
      currentPage = 'analytics/species';
      pageTitle = 'Species Analytics';
      loadComponent('species');
    } else if (path === '/ui/analytics') {
      currentRoute = 'analytics';
      currentPage = 'analytics';
      pageTitle = 'Analytics';
      loadComponent('analytics');
    } else if (path === '/ui/search') {
      currentRoute = 'search';
      currentPage = 'search';
      pageTitle = 'Search';
      loadComponent('search');
    } else if (path === '/ui/detections') {
      currentRoute = 'detections';
      currentPage = 'detections';
      pageTitle = 'Detections';
      loadComponent('detections');
    } else if (path === '/ui/about') {
      currentRoute = 'about';
      currentPage = 'about';
      pageTitle = 'About';
      loadComponent('about');
    } else if (path === '/ui/system') {
      currentRoute = 'system';
      currentPage = 'system';
      pageTitle = 'System';
      loadComponent('system');
    } else if (path === '/ui/settings' || path.startsWith('/ui/settings/')) {
      currentRoute = 'settings';
      currentPage = 'settings';
      pageTitle = 'Settings';
      loadComponent('settings');

      // Update title based on specific settings page
      if (path.includes('/main')) pageTitle = 'Main Settings';
      else if (path.includes('/audio')) pageTitle = 'Audio Settings';
      else if (path.includes('/detectionfilters')) pageTitle = 'Detection Filters';
      else if (path.includes('/integrations')) pageTitle = 'Integrations';
      else if (path.includes('/security')) pageTitle = 'Security Settings';
      else if (path.includes('/species')) pageTitle = 'Species Settings';
      else if (path.includes('/support')) pageTitle = 'Support';
    } else {
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
  });
</script>

<RootLayout title={pageTitle} {currentPage} {securityEnabled} {accessAllowed} {version}>
  {#if currentRoute === 'dashboard'}
    <DashboardPage />
  {:else if currentRoute === 'notifications'}
    {#if loadingComponent}
      <div class="flex items-center justify-center h-64">
        <span class="loading loading-spinner loading-lg"></span>
      </div>
    {:else if Notifications}
      <Notifications />
    {/if}
  {:else if currentRoute === 'analytics'}
    {#if loadingComponent}
      <div class="flex items-center justify-center h-64">
        <span class="loading loading-spinner loading-lg"></span>
      </div>
    {:else if Analytics}
      <Analytics />
    {/if}
  {:else if currentRoute === 'species'}
    {#if loadingComponent}
      <div class="flex items-center justify-center h-64">
        <span class="loading loading-spinner loading-lg"></span>
      </div>
    {:else if Species}
      <Species />
    {/if}
  {:else if currentRoute === 'search'}
    {#if loadingComponent}
      <div class="flex items-center justify-center h-64">
        <span class="loading loading-spinner loading-lg"></span>
      </div>
    {:else if Search}
      <Search />
    {/if}
  {:else if currentRoute === 'about'}
    {#if loadingComponent}
      <div class="flex items-center justify-center h-64">
        <span class="loading loading-spinner loading-lg"></span>
      </div>
    {:else if About}
      <About />
    {/if}
  {:else if currentRoute === 'system'}
    {#if loadingComponent}
      <div class="flex items-center justify-center h-64">
        <span class="loading loading-spinner loading-lg"></span>
      </div>
    {:else if System}
      <System />
    {/if}
  {:else if currentRoute === 'settings'}
    {#if loadingComponent}
      <div class="flex items-center justify-center h-64">
        <span class="loading loading-spinner loading-lg"></span>
      </div>
    {:else if Settings}
      <Settings />
    {/if}
  {:else if currentRoute === 'detections'}
    {#if loadingComponent}
      <div class="flex items-center justify-center h-64">
        <span class="loading loading-spinner loading-lg"></span>
      </div>
    {:else if Detections}
      <Detections />
    {/if}
  {:else if currentRoute === 'error-404'}
    {#if loadingComponent}
      <div class="flex items-center justify-center h-64">
        <span class="loading loading-spinner loading-lg"></span>
      </div>
    {:else if ErrorPage}
      <ErrorPage />
    {/if}
  {:else if currentRoute === 'error-500'}
    {#if loadingComponent}
      <div class="flex items-center justify-center h-64">
        <span class="loading loading-spinner loading-lg"></span>
      </div>
    {:else if ServerErrorPage}
      <ServerErrorPage />
    {/if}
  {:else if currentRoute === 'error-generic'}
    {#if loadingComponent}
      <div class="flex items-center justify-center h-64">
        <span class="loading loading-spinner loading-lg"></span>
      </div>
    {:else if GenericErrorPage}
      <GenericErrorPage />
    {/if}
  {/if}
</RootLayout>

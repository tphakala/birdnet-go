<script lang="ts">
  import { onMount } from 'svelte';
  import type { Snippet } from 'svelte';
  import type { AuthConfig } from '../../../app.d';
  import { cn } from '$lib/utils/cn';
  import Header from './DesktopHeader.svelte';
  import Sidebar from './DesktopSidebar.svelte';
  import { auth as authStore } from '$lib/stores/auth';
  import { sidebar } from '$lib/stores/sidebar';
  import ToastContainer from '$lib/desktop/components/ui/ToastContainer.svelte';

  interface Props {
    title?: string;
    currentPage?: string;
    currentPath?: string;
    securityEnabled?: boolean;
    accessAllowed?: boolean;
    version?: string;
    children?: Snippet;
    className?: string;
    authConfig?: AuthConfig;
    onNavigate?: (_url: string) => void;
  }

  let {
    title = 'Dashboard',
    currentPage = 'dashboard',
    currentPath,
    securityEnabled = false,
    accessAllowed = true,
    version = 'Development Build',
    children,
    className = '',
    authConfig = {
      basicEnabled: true,
      enabledProviders: [],
    },
    onNavigate,
  }: Props = $props();

  // Drawer state
  let drawerOpen = $state(false);

  // Get sidebar collapsed state for dynamic grid layout ($ prefix auto-subscribes to store)
  let isCollapsed = $derived($sidebar);

  // Initialize stores on mount
  onMount(() => {
    // Initialize auth state (CSRF is now handled by appState in App.svelte)
    authStore.init(securityEnabled, accessAllowed);

    // SSE notifications are auto-initialized when imported

    // Set theme from localStorage
    const savedTheme = globalThis.localStorage.getItem('theme');
    if (savedTheme) {
      document.documentElement.setAttribute('data-theme', savedTheme);
      document.documentElement.setAttribute('data-theme-controller', savedTheme);
    }

    // Handle HTTPS redirect if configured
    if (
      window.location.protocol !== 'https:' &&
      window.location.hostname !== 'localhost' &&
      window.location.hostname !== '127.0.0.1'
    ) {
      // Check if HTTPS redirect is enabled via a data attribute or config
      const redirectEnabled = document.documentElement.dataset.httpsRedirect === 'true';
      if (redirectEnabled) {
        window.location.href =
          'https:' + window.location.href.substring(window.location.protocol.length);
      }
    }
  });

  // Handle sidebar toggle
  function handleSidebarToggle() {
    drawerOpen = !drawerOpen;
  }

  // Handle search - passed through to SearchBox component
  function handleSearch(_query: string) {
    // SearchBox component handles the search implementation internally
    // including navigation to /ui/detections with query parameters
  }

  // Handle navigation - use passed callback or fall back to full reload
  function handleNavigate(url: string) {
    const uiUrl = url.startsWith('/ui/') ? url : `/ui${url === '/' ? '/dashboard' : url}`;
    if (onNavigate) {
      onNavigate(uiUrl);
    } else {
      // Fallback for when used without SPA routing
      window.location.href = uiUrl;
    }
  }
</script>

<div
  class={cn(
    'drawer lg:drawer-open min-h-screen bg-base-200 transition-all duration-200',
    isCollapsed ? 'sidebar-collapsed' : 'sidebar-expanded',
    className
  )}
>
  <input id="my-drawer" type="checkbox" class="drawer-toggle" bind:checked={drawerOpen} />

  <div class="drawer-content">
    <!-- Header -->
    <div class="mx-auto max-w-7xl">
      <div class="grid grid-cols-12 grid-rows-[min-content] p-3 pt-0 lg:px-8 lg:pb-0">
        <Header
          {title}
          {currentPage}
          {securityEnabled}
          {accessAllowed}
          showSidebarToggle={true}
          showSearch={currentPage === 'dashboard' || currentPage === 'detections'}
          onSidebarToggle={handleSidebarToggle}
          onSearch={handleSearch}
          onNavigate={handleNavigate}
        />
      </div>
    </div>

    <!-- Main content -->
    <main class="min-h-screen">
      <div class="mx-auto max-w-7xl">
        <div
          id="mainContent"
          class="grid grid-cols-12 grid-rows-[min-content] gap-y-8 p-3 pt-0 lg:p-8 lg:pt-0"
        >
          {#if children}
            {@render children()}
          {/if}
        </div>
      </div>

      <!-- Placeholder for dynamic notifications -->
      <div id="status-message"></div>
    </main>
  </div>

  <!-- Sidebar -->
  <Sidebar
    {securityEnabled}
    {accessAllowed}
    {version}
    currentRoute={currentPath ?? `/ui/${currentPage}`}
    onNavigate={handleNavigate}
    {authConfig}
  />

  <!-- Login Modal placeholder -->
  <dialog id="loginModal" class="modal modal-bottom sm:modal-middle">
    <!-- Login form will be loaded here dynamically -->
  </dialog>
</div>

<!-- Global Toast Container -->
<ToastContainer />

<style>
  /* =================================================================
     Drawer Layout Styles
     All drawer-related styles are centralized here for maintainability
     ================================================================= */

  /* Base drawer content styles */
  :global(.drawer-content) {
    position: relative;
    overflow-x: hidden;
    min-width: 0;
  }

  /* Prevent horizontal scroll while allowing max-width utilities */
  :global(.drawer-content > *:not(.mx-auto)) {
    max-width: 100%;
  }

  /* Ensure grid containers respect parent width */
  :global(.drawer-content .grid) {
    width: 100%;
  }

  /* Desktop drawer layout (≥1024px) */
  @media (min-width: 1024px) {
    :global(.drawer.lg\:drawer-open) {
      grid-template-columns: 256px 1fr;
      transition: grid-template-columns 0.2s ease-in-out;
    }

    :global(.drawer.lg\:drawer-open.sidebar-collapsed) {
      grid-template-columns: 64px 1fr;
    }

    :global(.drawer.lg\:drawer-open .drawer-side) {
      display: block;
      position: sticky;
      width: 256px;
      transition: width 0.2s ease-in-out;
      overflow: visible;
    }

    :global(.drawer.lg\:drawer-open.sidebar-collapsed .drawer-side) {
      width: 64px;
      overflow: visible;
    }

    :global(.drawer.lg\:drawer-open .drawer-content) {
      min-width: 0;
      overflow-x: hidden;
    }
  }

  /* =================================================================
     Content Container Styles
     Container with max-width of 80rem (1280px)
     ================================================================= */

  /* Max-width container */
  :global(.drawer-content .max-w-7xl) {
    max-width: 80rem; /* 1280px */
    width: 100%;
    margin-left: auto;
    margin-right: auto;
  }

  /* =================================================================
     Tooltip Overflow Prevention
     Ensures tooltips don't cause horizontal scrollbars
     ================================================================= */

  /* Prevent tooltips from extending beyond viewport */
  :global(.drawer-content .invisible.group-hover\:visible) {
    max-width: calc(100vw - 2rem);
  }

  /* Fix transform-based centered tooltips */
  :global(.drawer-content .absolute.left-1\/2.transform.-translate-x-1\/2) {
    transform-origin: center;
  }
</style>

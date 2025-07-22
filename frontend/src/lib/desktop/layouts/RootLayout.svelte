<script lang="ts">
  import { onMount } from 'svelte';
  import type { Snippet } from 'svelte';
  import { cn } from '$lib/utils/cn';
  import Header from './DesktopHeader.svelte';
  import Sidebar from './DesktopSidebar.svelte';
  import { auth as authStore } from '$lib/stores/auth';
  import { csrf as csrfStore } from '$lib/stores/csrf';
  import ToastContainer from '$lib/desktop/components/ui/ToastContainer.svelte';

  interface Props {
    title?: string;
    currentPage?: string;
    securityEnabled?: boolean;
    accessAllowed?: boolean;
    version?: string;
    children?: Snippet;
    className?: string;
  }

  let {
    title = 'Dashboard',
    currentPage = 'dashboard',
    securityEnabled = false,
    accessAllowed = true,
    version = 'Development Build',
    children,
    className = '',
  }: Props = $props();

  // Drawer state
  let drawerOpen = $state(false);

  // Initialize stores on mount
  onMount(() => {
    // Initialize CSRF token
    csrfStore.init();

    // Initialize auth state
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

  // Handle search
  function handleSearch(_query: string) {
    // TODO: Implement search navigation for query: ${query}
    // When API is ready, navigate to search results
  }

  // Handle navigation
  function handleNavigate(url: string) {
    // Convert old routes to new /ui/ routes
    const uiUrl = url.startsWith('/ui/') ? url : `/ui${url === '/' ? '/dashboard' : url}`;
    window.location.href = uiUrl;
  }
</script>

<div class={cn('drawer lg:drawer-open min-h-screen bg-base-200', className)}>
  <input id="my-drawer" type="checkbox" class="drawer-toggle" bind:checked={drawerOpen} />

  <div class="drawer-content">
    <!-- Header -->
    <div class="grid grid-cols-12 grid-rows-[min-content] p-3 pt-0 lg:px-8 lg:pb-0">
      <Header
        {title}
        {currentPage}
        {securityEnabled}
        {accessAllowed}
        showSidebarToggle={true}
        onSidebarToggle={handleSidebarToggle}
        onSearch={handleSearch}
        onNavigate={handleNavigate}
      />
    </div>

    <!-- Main content -->
    <main>
      <div
        id="mainContent"
        class="grid grid-cols-12 grid-rows-[min-content] gap-y-8 p-3 pt-0 lg:p-8 lg:pt-0"
      >
        {#if children}
          {@render children()}
        {/if}
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
    currentRoute={`/ui/${currentPage}`}
    onNavigate={handleNavigate}
  />

  <!-- Login Modal placeholder -->
  <dialog id="loginModal" class="modal modal-bottom sm:modal-middle">
    <!-- Login form will be loaded here dynamically -->
  </dialog>

  <!-- Global Loading Indicator (hidden by default in Svelte UI) -->
  <!-- This was for HTMX loading states, not needed in Svelte -->
</div>

<!-- Global Toast Container -->
<ToastContainer />

<style>
  /* Ensure consistent drawer behavior */
  :global(.drawer-content) {
    position: relative;
    overflow-x: hidden;
    min-width: 0;
  }

  /* Fix drawer layout for desktop */
  @media (min-width: 1024px) {
    :global(.drawer.lg\:drawer-open) {
      grid-template-columns: 256px 1fr;
    }

    :global(.drawer.lg\:drawer-open .drawer-side) {
      display: block;
      position: sticky;
      width: 256px;
    }
  }
</style>

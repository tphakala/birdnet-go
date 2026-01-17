<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import SearchBox from '$lib/desktop/components/ui/SearchBox.svelte';
  import AudioLevelIndicator from '$lib/desktop/components/ui/AudioLevelIndicator.svelte';
  import NotificationBell from '$lib/desktop/components/ui/NotificationBell.svelte';
  import ThemeToggle from '$lib/desktop/components/ui/ThemeToggle.svelte';
  import { navigation } from '$lib/stores/navigation.svelte';
  import { Menu } from '@lucide/svelte';
  import { t } from '$lib/i18n';

  interface Props {
    title?: string;
    currentPage?: string;
    showSidebarToggle?: boolean;
    showSearch?: boolean;
    showAudioLevel?: boolean;
    showNotifications?: boolean;
    showThemeToggle?: boolean;
    debugMode?: boolean;
    securityEnabled?: boolean;
    accessAllowed?: boolean;
    onSidebarToggle?: () => void;
    onSearch?: (_query: string) => void;
    onNavigate?: (_url: string) => void;
    className?: string;
  }

  let {
    title = 'Dashboard',
    currentPage = 'dashboard',
    showSidebarToggle = true,
    showSearch = true,
    showAudioLevel = true,
    showNotifications = true,
    showThemeToggle = true,
    debugMode = false,
    securityEnabled = false,
    accessAllowed = true,
    onSidebarToggle,
    onSearch,
    onNavigate,
    className = '',
  }: Props = $props();

  // Handle sidebar toggle
  function handleSidebarToggle() {
    if (onSidebarToggle) {
      onSidebarToggle();
    } else {
      // Default behavior - toggle drawer checkbox if it exists
      const drawer = document.getElementById('my-drawer') as HTMLInputElement;
      if (drawer) {
        drawer.checked = !drawer.checked;
      }
    }
  }

  // Handle search queries (quick search functionality)
  function handleSearch(query: string) {
    if (onSearch) {
      onSearch(query);
    }
  }

  // Handle navigation (including search results navigation)
  function handleNavigate(url: string) {
    if (onNavigate) {
      onNavigate(url);
    } else {
      // Fallback to navigation store for proxy-aware navigation
      navigation.navigate(url);
    }
  }
</script>

<header
  class={cn(
    'col-span-12 flex items-center justify-between gap-2 p-1 sm:gap-4 sm:p-2 lg:p-4',
    className
  )}
>
  <!-- Left section: Sidebar toggle and title -->
  <div class="flex items-center gap-2 sm:gap-4">
    {#if showSidebarToggle}
      <button
        onclick={handleSidebarToggle}
        class="btn btn-ghost btn-sm p-0 sm:p-1 lg:hidden"
        aria-label={t('navigation.toggleSidebar')}
      >
        <Menu class="size-6" />
      </button>
    {/if}

    <h1 class="text-base sm:text-xl lg:text-2xl font-bold">
      {title}
    </h1>
  </div>

  <!-- Center section: Search box -->
  {#if showSearch}
    <SearchBox {currentPage} onSearch={handleSearch} onNavigate={handleNavigate} />
  {:else}
    <!-- Spacer to maintain layout when search is hidden -->
    <!-- Match SearchBox height: input-sm (32px) on mobile, input-md (40px) on sm+ screens -->
    <div class="grow flex items-center">
      <div class="w-full md:w-3/4 lg:w-4/5 xl:w-5/6 max-w-4xl mx-auto h-12"></div>
    </div>
  {/if}

  <!-- Right section: Action items -->
  <div class="flex items-center gap-2">
    {#if showAudioLevel}
      <AudioLevelIndicator {securityEnabled} {accessAllowed} />
    {/if}

    {#if showNotifications}
      <NotificationBell
        {debugMode}
        onNavigateToNotifications={() => handleNavigate('/ui/notifications')}
      />
    {/if}

    {#if showThemeToggle}
      <ThemeToggle className="hidden md:block" showTooltip={true} />
    {/if}
  </div>
</header>

<style>
  /* Ensure header maintains minimum height */
  header {
    min-height: 3rem;
  }
</style>

<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { systemIcons } from '$lib/utils/icons';
  import { resetDateToToday } from '$lib/utils/datePersistence';

  interface Props {
    currentRoute: string;
    onNavigate?: (_url: string) => void;
    className?: string;
  }

  let { currentRoute, onNavigate, className = '' }: Props = $props();

  interface TabConfig {
    id: string;
    route: string;
    icon: string;
    label: string;
  }

  const tabs: TabConfig[] = [
    {
      id: 'dashboard',
      route: '/ui/dashboard',
      icon: systemIcons.home,
      label: 'Dashboard',
    },
    {
      id: 'detections',
      route: '/ui/detections',
      icon: systemIcons.list,
      label: 'Detections',
    },
    {
      id: 'analytics',
      route: '/ui/analytics',
      icon: systemIcons.analytics,
      label: 'Analytics',
    },
    {
      id: 'settings',
      route: '/ui/settings',
      icon: systemIcons.settingsGear,
      label: 'Settings',
    },
  ];

  function isActive(tabRoute: string): boolean {
    if (tabRoute === '/ui/dashboard') {
      return currentRoute === '/ui/dashboard' || currentRoute === '/ui/' || currentRoute === '/ui';
    }
    return currentRoute.startsWith(tabRoute);
  }

  function handleTabClick(tab: TabConfig) {
    // Reset date to today when navigating to dashboard
    if (tab.id === 'dashboard') {
      resetDateToToday();
    }

    if (onNavigate) {
      onNavigate(tab.route);
    } else {
      window.location.href = tab.route;
    }
  }
</script>

<nav
  class={cn(
    'fixed bottom-0 left-0 right-0 z-30 flex h-16 items-center justify-around bg-base-100 border-t border-base-200 safe-area-inset-bottom',
    className
  )}
  aria-label="Main navigation"
>
  {#each tabs as tab (tab.id)}
    {@const active = isActive(tab.route)}
    <button
      onclick={() => handleTabClick(tab)}
      class={cn(
        'flex flex-col items-center justify-center gap-1 h-full px-4 min-w-[64px]',
        'transition-colors duration-150',
        active ? 'text-primary' : 'text-base-content/60'
      )}
      aria-current={active ? 'page' : undefined}
      aria-label={tab.label}
    >
      <span class={cn('w-6 h-6', active && 'scale-110')}>
        {@html tab.icon}
      </span>
      <span class="text-xs font-medium">{tab.label}</span>
    </button>
  {/each}
</nav>

<style>
  .safe-area-inset-bottom {
    padding-bottom: env(safe-area-inset-bottom, 0);
  }
</style>

<script lang="ts">
  import { onMount } from 'svelte';
  import type { Snippet } from 'svelte';
  import { cn } from '$lib/utils/cn';
  import BottomNav from './BottomNav.svelte';
  import MobileHeader from './MobileHeader.svelte';
  import { auth as authStore } from '$lib/stores/auth';
  import { csrf as csrfStore } from '$lib/stores/csrf';
  import ToastContainer from '$lib/desktop/components/ui/ToastContainer.svelte';

  interface Props {
    title?: string;
    currentRoute?: string;
    showBack?: boolean;
    showHeader?: boolean;
    showNav?: boolean;
    securityEnabled?: boolean;
    accessAllowed?: boolean;
    onBack?: () => void;
    onNavigate?: (_url: string) => void;
    children?: Snippet;
    className?: string;
  }

  let {
    title = 'BirdNET-Go',
    currentRoute = '/ui/dashboard',
    showBack = false,
    showHeader = true,
    showNav = true,
    securityEnabled = false,
    accessAllowed = true,
    onBack,
    onNavigate,
    children,
    className = '',
  }: Props = $props();

  onMount(() => {
    // Initialize CSRF token
    csrfStore.init();

    // Initialize auth state
    authStore.init(securityEnabled, accessAllowed);

    // Set theme from localStorage
    const savedTheme = globalThis.localStorage.getItem('theme');
    if (savedTheme) {
      document.documentElement.setAttribute('data-theme', savedTheme);
    }
  });
</script>

<div class={cn('flex flex-col min-h-screen bg-base-200', className)}>
  {#if showHeader}
    <MobileHeader {title} {showBack} {onBack} />
  {/if}

  <main
    class={cn(
      'flex-1 overflow-y-auto',
      showNav && 'pb-20' // Space for bottom nav + safe area
    )}
  >
    {#if children}
      {@render children()}
    {/if}
  </main>

  {#if showNav}
    <BottomNav {currentRoute} {onNavigate} />
  {/if}
</div>

<ToastContainer />

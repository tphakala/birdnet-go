<script lang="ts">
  import { onMount } from 'svelte';
  import { theme } from '$lib/stores/theme';
  import { scheme } from '$lib/stores/scheme';
  import { dashboardEditMode } from '$lib/stores/dashboardEditMode';
  import { navigation } from '$lib/stores/navigation.svelte';
  import { cn } from '$lib/utils/cn';
  import { t } from '$lib/i18n';
  import { Settings, Sun, Moon, Pencil, Github } from '@lucide/svelte';

  interface Props {
    securityEnabled?: boolean;
    accessAllowed?: boolean;
    className?: string;
  }

  let { securityEnabled = false, accessAllowed = true, className = '' }: Props = $props();

  let isOpen = $state(false);
  let buttonRef = $state<HTMLButtonElement | null>(null);
  let dropdownRef = $state<HTMLDivElement | null>(null);

  // Admin check: show edit dashboard if security disabled or user has access
  let isAdmin = $derived(!securityEnabled || accessAllowed);

  // Initialize theme and scheme (migrated from ThemeToggle)
  onMount(() => {
    const cleanup = theme.initialize();
    scheme.initialize();

    return () => {
      if (cleanup) cleanup();
    };
  });

  function toggleMenu() {
    isOpen = !isOpen;
  }

  function handleThemeToggle() {
    theme.toggle();
    // Menu stays open after theme toggle for visual feedback
  }

  function handleEditDashboard() {
    isOpen = false;
    dashboardEditMode.set(true);
    navigation.navigate('/ui/dashboard');
  }

  // Click outside to close
  function handleClickOutside(event: MouseEvent) {
    if (!dropdownRef || !buttonRef) return;
    const target = event.target as Node;
    if (!dropdownRef.contains(target) && !buttonRef.contains(target)) {
      isOpen = false;
    }
  }

  // Escape key to close
  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape' && isOpen) {
      isOpen = false;
      buttonRef?.focus();
    }
  }

  // Register click-outside and keydown listeners
  $effect(() => {
    if (typeof globalThis.window !== 'undefined') {
      globalThis.document.addEventListener('click', handleClickOutside);
      globalThis.document.addEventListener('keydown', handleKeydown);

      return () => {
        globalThis.document.removeEventListener('click', handleClickOutside);
        globalThis.document.removeEventListener('keydown', handleKeydown);
      };
    }
  });
</script>

<div class={cn('relative', className)}>
  <button
    bind:this={buttonRef}
    onclick={toggleMenu}
    class="btn btn-ghost btn-sm p-1"
    aria-label={t('navigation.settingsMenu')}
    aria-expanded={isOpen}
  >
    <Settings class="size-6" />
  </button>

  {#if isOpen}
    <div
      bind:this={dropdownRef}
      class="absolute right-0 top-full mt-2 min-w-48 rounded-lg border border-[var(--color-base-content)]/10 bg-[var(--color-base-100)] shadow-lg"
      style:z-index="1010"
      role="menu"
    >
      <div class="p-1">
        <!-- Theme toggle -->
        <button
          onclick={handleThemeToggle}
          class="flex w-full items-center gap-3 rounded-md px-3 py-2 text-sm text-[var(--color-base-content)] transition-colors duration-150 hover:bg-[var(--color-base-content)]/10"
          role="menuitem"
        >
          {#if $theme === 'dark'}
            <Sun class="size-5 shrink-0 text-[var(--color-base-content)]/70" />
          {:else}
            <Moon class="size-5 shrink-0 text-[var(--color-base-content)]/70" />
          {/if}
          <span>{t('navigation.theme')}</span>
        </button>

        <!-- Edit Dashboard (admin only) -->
        {#if isAdmin}
          <button
            onclick={handleEditDashboard}
            class="flex w-full items-center gap-3 rounded-md px-3 py-2 text-sm text-[var(--color-base-content)] transition-colors duration-150 hover:bg-[var(--color-base-content)]/10"
            role="menuitem"
          >
            <Pencil class="size-5 shrink-0 text-[var(--color-base-content)]/70" />
            <span>{t('dashboard.editMode.editDashboard')}</span>
          </button>
        {/if}

        <!-- Divider -->
        <div class="my-1 border-t border-[var(--color-base-content)]/10" role="separator"></div>

        <!-- GitHub link -->
        <a
          href="https://github.com/tphakala/birdnet-go"
          target="_blank"
          rel="noopener noreferrer"
          class="flex w-full items-center gap-3 rounded-md px-3 py-2 text-sm text-[var(--color-base-content)] transition-colors duration-150 hover:bg-[var(--color-base-content)]/10"
          role="menuitem"
        >
          <Github class="size-5 shrink-0 text-[var(--color-base-content)]/70" />
          <span>{t('navigation.github')}</span>
        </a>
      </div>
    </div>
  {/if}
</div>

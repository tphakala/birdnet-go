<script lang="ts">
  import { onMount } from 'svelte';
  import { theme } from '$lib/stores/theme';
  import { scheme } from '$lib/stores/scheme';
  import { dashboardEditMode } from '$lib/stores/dashboardEditMode';
  import { settingsStore } from '$lib/stores/settings';
  import type { Dashboard, DashboardLayout } from '$lib/stores/settings';
  import { navigation } from '$lib/stores/navigation.svelte';
  import { cn } from '$lib/utils/cn';
  import { t } from '$lib/i18n';
  import { api } from '$lib/utils/api';
  import { getLogger } from '$lib/utils/logger';
  import { resetDateToToday } from '$lib/utils/datePersistence';
  import {
    Settings,
    Sun,
    Moon,
    Pencil,
    RotateCcw,
    Bug,
    MessageCircleQuestion,
    ExternalLink,
    Keyboard,
  } from '@lucide/svelte';
  import GithubIcon from '$lib/desktop/components/ui/GithubIcon.svelte';
  import { dropdown } from '$lib/utils/transitions';
  import { appState } from '$lib/stores/appState.svelte';
  import ConfirmModal from '$lib/desktop/components/modals/ConfirmModal.svelte';
  import Kbd from '$lib/desktop/components/ui/Kbd.svelte';
  import { HELP_SHORTCUT_ID, shortcuts } from '$lib/stores/shortcuts.svelte';
  import { DASHBOARD_PATH } from '$lib/utils/loginRedirect';

  const logger = getLogger('dashboard');

  interface Props {
    securityEnabled?: boolean;
    accessAllowed?: boolean;
    className?: string;
  }

  let { securityEnabled = false, accessAllowed = true, className = '' }: Props = $props();

  // Items touch their neighbours and the panel pads them by 4 px, so the ring is drawn inside the item
  const MENU_ITEM_CLASS =
    'flex w-full items-center gap-3 rounded-md px-3 py-2 text-sm font-normal text-[var(--color-base-content)] transition-colors duration-150 hover:bg-[var(--color-base-content)]/10 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-[var(--color-primary)]';

  let isOpen = $state(false);
  let showResetConfirm = $state(false);
  let buttonRef = $state<HTMLButtonElement | null>(null);
  let dropdownRef = $state<HTMLDivElement | null>(null);

  // Admin check: user is authenticated or security is disabled. Dashboard
  // customization is admin-only: guests view the owner's published dashboard
  // read-only and never see the edit/reset controls (see issue #4112).
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

  // The item names its action: "Switch to dark theme", not just "Theme"
  let themeToggleLabel = $derived(
    t($theme === 'dark' ? 'navigation.switchToLightTheme' : 'navigation.switchToDarkTheme')
  );

  function handleThemeToggle() {
    theme.toggle();
    // Menu stays open after theme toggle for visual feedback
  }

  function handleEditDashboard() {
    isOpen = false;
    // As for the reset: the leave prompt returns focus to the trigger, not to the unmounted item
    buttonRef?.focus();
    resetDateToToday();
    // Armed only once the move applies: after Stay, the next dashboard visit must not open in
    // edit mode. The dashboard reads the flag reactively, so setting it just after the route
    // change is equivalent to setting it first.
    void navigation.navigate(DASHBOARD_PATH).then(applied => {
      if (applied) dashboardEditMode.set(true);
    });
  }

  const DEFAULT_LAYOUT: DashboardLayout = {
    elements: [
      {
        id: 'daily-summary-0',
        type: 'daily-summary',
        enabled: true,
        summary: { summaryLimit: 30 },
      },
      { id: 'currently-hearing-0', type: 'currently-hearing', enabled: true },
      { id: 'detections-grid-0', type: 'detections-grid', enabled: true },
    ],
  };

  function handleResetDashboard() {
    isOpen = false;
    // The focused menu item unmounts with the menu; focus the trigger first so the
    // confirmation records it as the place to return to
    buttonRef?.focus();
    showResetConfirm = true;
  }

  // The key that opens the cheat sheet; null while shortcuts are off
  let helpHint = $derived(shortcuts.hint(HELP_SHORTCUT_ID));

  function handleKeyboardShortcuts() {
    isOpen = false;
    // As for the reset: the sheet returns focus to the trigger, not to the unmounted item
    buttonRef?.focus();
    shortcuts.openHelp();
  }

  async function confirmResetDashboard() {
    try {
      await api.patch('/api/v2/settings/dashboard', { layout: DEFAULT_LAYOUT });
    } catch (error) {
      logger.error('Failed to reset dashboard layout:', error);
      showResetConfirm = false;
      return;
    }

    const defaultDashboard: Dashboard = {
      thumbnails: { summary: true, recent: true, imageProvider: '', fallbackPolicy: '' },
      summaryLimit: 30,
    };

    settingsStore.update(state => ({
      ...state,
      formData: {
        ...state.formData,
        realtime: {
          ...state.formData.realtime,
          dashboard: {
            ...defaultDashboard,
            ...state.formData.realtime?.dashboard,
            layout: DEFAULT_LAYOUT,
          },
        },
      },
      originalData: {
        ...state.originalData,
        realtime: {
          ...state.originalData.realtime,
          dashboard: {
            ...defaultDashboard,
            ...state.originalData.realtime?.dashboard,
            layout: DEFAULT_LAYOUT,
          },
        },
      },
    }));

    showResetConfirm = false;
    navigation.navigate(DASHBOARD_PATH);
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

  // Register click-outside and keydown listeners only when menu is open
  $effect(() => {
    if (isOpen && typeof globalThis.window !== 'undefined') {
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
    class="focus-ring inline-flex items-center justify-center rounded-lg p-1 text-[var(--color-base-content)] transition-colors duration-150 hover:bg-[var(--color-base-content)]/10"
    aria-label={t('navigation.settingsMenu')}
    aria-haspopup="true"
    aria-expanded={isOpen}
    aria-controls="header-settings-menu"
  >
    <Settings class="size-6" />
  </button>

  {#if isOpen}
    <div
      bind:this={dropdownRef}
      id="header-settings-menu"
      in:dropdown
      out:dropdown={{ duration: 100 }}
      class="absolute right-0 top-full mt-2 w-max min-w-48 max-w-[calc(100vw-2rem)] rounded-lg border border-[var(--color-base-content)]/10 bg-[var(--color-base-100)] shadow-lg"
      style:z-index="1010"
      aria-label={t('navigation.settingsMenu')}
    >
      <div class="p-1">
        <!-- Theme toggle -->
        <button onclick={handleThemeToggle} class={MENU_ITEM_CLASS}>
          {#if $theme === 'dark'}
            <Sun class="size-4 shrink-0 text-[var(--color-base-content)]/70" />
          {:else}
            <Moon class="size-4 shrink-0 text-[var(--color-base-content)]/70" />
          {/if}
          <span>{themeToggleLabel}</span>
        </button>

        <!-- Edit Dashboard (authenticated users only; guests view read-only) -->
        {#if isAdmin}
          <button onclick={handleEditDashboard} class={MENU_ITEM_CLASS}>
            <Pencil class="size-4 shrink-0 text-[var(--color-base-content)]/70" />
            <span>{t('dashboard.editMode.editDashboard')}</span>
          </button>

          <!-- Reset Dashboard -->
          <button onclick={handleResetDashboard} class={MENU_ITEM_CLASS}>
            <RotateCcw class="size-4 shrink-0 text-[var(--color-base-content)]/70" />
            <span>{t('dashboard.editMode.resetDashboard')}</span>
          </button>
        {/if}

        <!-- Divider -->
        <div class="my-1 border-t border-[var(--color-base-content)]/10" aria-hidden="true"></div>

        <!-- Keyboard shortcuts cheat sheet -->
        <button
          onclick={handleKeyboardShortcuts}
          class={MENU_ITEM_CLASS}
          aria-haspopup="dialog"
          aria-keyshortcuts={helpHint?.aria}
        >
          <Keyboard class="size-4 shrink-0 text-[var(--color-base-content)]/70" />
          <span class="flex-1 text-start">{t('shortcuts.menuItem')}</span>
          {#if helpHint}
            <Kbd keys={helpHint.keys} size="sm" className="text-[var(--color-base-content)]/70" />
          {/if}
        </button>

        <!-- Report Bug -->
        <button
          onclick={() => {
            isOpen = false;
            // As for Edit dashboard: the leave prompt returns focus to the trigger
            buttonRef?.focus();
            navigation.navigate('/ui/help/report-bug');
          }}
          class={MENU_ITEM_CLASS}
        >
          <Bug class="size-4 shrink-0 text-[var(--color-base-content)]/70" />
          <span>{t('navigation.reportBug')}</span>
        </button>

        <!-- Ask a Question -->
        <a
          href={appState.projectLinks.discussionsUrl}
          target="_blank"
          rel="noopener noreferrer"
          class={MENU_ITEM_CLASS}
          aria-label={t('navigation.askQuestionAriaLabel')}
        >
          <MessageCircleQuestion class="size-4 shrink-0 text-[var(--color-base-content)]/70" />
          <span class="flex-1">{t('navigation.askQuestion')}</span>
          <ExternalLink class="size-3 text-[var(--color-base-content)]/40" />
        </a>

        <!-- GitHub link -->
        <a
          href={appState.projectLinks.repoUrl}
          target="_blank"
          rel="noopener noreferrer"
          class={MENU_ITEM_CLASS}
        >
          <GithubIcon class="size-4 shrink-0 text-[var(--color-base-content)]/70" />
          <span>{t('navigation.github')}</span>
        </a>
      </div>
    </div>
  {/if}
</div>

<ConfirmModal
  isOpen={showResetConfirm}
  title={t('dashboard.editMode.resetDashboard')}
  message={t('dashboard.editMode.resetConfirm')}
  confirmLabel={t('dashboard.editMode.resetDashboard')}
  confirmVariant="warning"
  onClose={() => {
    showResetConfirm = false;
  }}
  onConfirm={confirmResetDashboard}
/>

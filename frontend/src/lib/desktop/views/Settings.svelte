<script lang="ts">
  import { onMount } from 'svelte';
  import { settingsStore, settingsActions } from '$lib/stores/settings';
  import { safeGet } from '$lib/utils/security';
  import { t } from '$lib/i18n';
  import { navigation } from '$lib/stores/navigation.svelte';

  // SPINNER CONTROL: Set to false to disable loading spinners (reduces flickering)
  // Change back to true to re-enable spinners for testing
  const ENABLE_LOADING_SPINNERS = false;
  import MainSettingsSection from '$lib/desktop/features/settings/pages/MainSettingsPage.svelte';
  import AudioSettingsSection from '$lib/desktop/features/settings/pages/AudioSettingsPage.svelte';
  import FilterSettingsSection from '$lib/desktop/features/settings/pages/FilterSettingsPage.svelte';
  import IntegrationSettingsSection from '$lib/desktop/features/settings/pages/IntegrationSettingsPage.svelte';
  import SecuritySettingsSection from '$lib/desktop/features/settings/pages/SecuritySettingsPage.svelte';
  import SupportSettingsSection from '$lib/desktop/features/settings/pages/SupportSettingsPage.svelte';
  import SpeciesSettingsSection from '$lib/desktop/features/settings/pages/SpeciesSettingsPage.svelte';
  import NotificationsSettingsSection from '$lib/desktop/features/settings/pages/NotificationsSettingsPage.svelte';
  import AlertRulesSettingsSection from '$lib/desktop/features/settings/pages/AlertRulesSettingsPage.svelte';
  import ErrorAlert from '$lib/desktop/components/ui/ErrorAlert.svelte';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';

  // Map URL paths to section names
  const sectionMap: Record<string, string> = {
    main: 'node',
    audio: 'audio',
    detectionfilters: 'filters',
    integrations: 'integration',
    security: 'security',
    species: 'species',
    notifications: 'notifications',
    alertrules: 'alertrules',
    support: 'support',
  };

  // Get current section from a path
  function getSectionFromPath(path: string): string {
    const parts = path.split('/');
    const lastPart = parts[parts.length - 1];
    return safeGet(sectionMap, lastPart, 'node');
  }

  // Derive current section from navigation store's reactive path
  // This automatically updates when SPA navigation changes the path
  let currentSection = $derived(getSectionFromPath(navigation.currentPath));

  // Get store values
  let store = $derived($settingsStore);

  // Load settings data on mount
  onMount(() => {
    settingsActions.loadSettings();
  });
</script>

<svelte:head>
  <title>{t('pageTitle.settings')}</title>
</svelte:head>

<main class="col-span-12 container mx-auto">
  <!-- Global Error Display -->
  {#if store.error}
    <div class="mb-6">
      <ErrorAlert message={store.error} onDismiss={() => settingsActions.clearError()} />
    </div>
  {/if}

  <!-- Loading State -->
  {#if ENABLE_LOADING_SPINNERS && store.isLoading}
    <div class="flex justify-center items-center py-12">
      <LoadingSpinner size="lg" />
      <span class="ml-3 text-lg">{t('common.ui.loadingSettings')}</span>
    </div>
  {:else}
    <!-- Settings Content -->
    <div class="space-y-6">
      {#if currentSection === 'node'}
        <MainSettingsSection />
      {:else if currentSection === 'audio'}
        <AudioSettingsSection />
      {:else if currentSection === 'filters'}
        <FilterSettingsSection />
      {:else if currentSection === 'integration'}
        <IntegrationSettingsSection />
      {:else if currentSection === 'security'}
        <SecuritySettingsSection />
      {:else if currentSection === 'species'}
        <SpeciesSettingsSection />
      {:else if currentSection === 'notifications'}
        <NotificationsSettingsSection />
      {:else if currentSection === 'alertrules'}
        <AlertRulesSettingsSection />
      {:else if currentSection === 'support'}
        <SupportSettingsSection />
      {:else}
        <div class="card bg-base-100 shadow-xs p-6">
          <div class="text-center py-12 text-base-content opacity-70">
            <h2 class="text-xl font-semibold mb-2">{t('common.ui.settingsNotFound')}</h2>
            <p>{t('common.ui.sectionNotFound', { section: currentSection })}</p>
          </div>
        </div>
      {/if}
    </div>
  {/if}
</main>

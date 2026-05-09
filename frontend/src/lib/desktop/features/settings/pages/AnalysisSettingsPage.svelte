<!--
  Analysis Settings Page Component

  Purpose: Configure BirdNET-Go analysis settings including detection thresholds
  and manage the model gallery (install/uninstall additional classifier models).

  Features:
  - Confidence threshold slider for bird detection
  - Bat detection threshold slider (visible when a bat model is installed)
  - Locale selector for species labels
  - Model gallery with Installed and Available tabs
  - License acceptance dialog for model installation
  - Remove confirmation dialog for model uninstallation
  - Real-time download progress via SSE

  Props: None - This is a page component that uses global settings stores

  @component
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import type { CatalogEntry, DownloadProgress } from '$lib/types/models';
  import {
    fetchCatalog,
    installModel,
    uninstallModel,
    subscribeInstallProgress,
  } from '$lib/utils/modelsApi';
  import SettingsTabs from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import type { TabDefinition } from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import NumberField from '$lib/desktop/components/forms/NumberField.svelte';
  import { settingsStore, settingsActions, birdnetSettings } from '$lib/stores/settings';
  import { formatBytes } from '$lib/utils/formatters';
  import { t } from '$lib/i18n';
  import {
    Download,
    Trash2,
    Shield,
    ShieldAlert,
    Package,
    BrainCircuit,
    AlertTriangle,
    Loader2,
    RefreshCw,
    Radar,
  } from '@lucide/svelte';

  import logoBirdnet from '$lib/assets/logos/logo-birdnet.png';
  import logoGoogle from '$lib/assets/logos/logo-google.png';
  import logoJyu from '$lib/assets/logos/logo-jyu.jpeg';

  const MODEL_LOGOS: Record<string, string> = {
    birdnet: logoBirdnet,
    perch: logoGoogle,
    bsg: logoJyu,
  };

  function getModelLogo(id: string): string | null {
    for (const [prefix, logo] of Object.entries(MODEL_LOGOS)) {
      if (id.startsWith(prefix)) return logo;
    }
    return null;
  }

  // Catalog state
  let catalog = $state<CatalogEntry[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);

  // Install/uninstall state
  let installingId = $state<string | null>(null);
  let deletingId = $state<string | null>(null);
  let downloadProgress = $state<DownloadProgress | null>(null);

  // Modal state
  let licenseModel = $state<CatalogEntry | null>(null);
  let removeConfirmModel = $state<CatalogEntry | null>(null);

  // Element bindings should NOT use $state - causes showModal() to fail
  let licenseDialogRef: HTMLDialogElement | null = null;
  let removeDialogRef: HTMLDialogElement | null = null;

  // Gallery tab state
  type GalleryTab = 'installed' | 'available';
  let galleryTab = $state<GalleryTab>('installed');

  // Store state
  let store = $derived($settingsStore);
  let birdnet = $derived($birdnetSettings);

  // Derived catalog views
  const installedEntries = $derived(catalog.filter(e => e.installed));
  const availableBirds = $derived(
    catalog.filter(e => !e.installed && e.compatible && e.category === 'bird')
  );
  const availableBats = $derived(
    catalog.filter(e => !e.installed && e.compatible && e.category === 'bat')
  );

  // Gallery tabs definition
  const galleryTabs: TabDefinition[] = $derived([
    {
      id: 'installed',
      label: t('analysis.gallery.tabs.installed'),
      icon: Package,
      content: installedTabContent,
    },
    {
      id: 'available',
      label: t('analysis.gallery.tabs.available'),
      icon: Download,
      content: availableTabContent,
    },
  ]);

  // SSE cleanup handle
  let progressCleanup: (() => void) | null = null;

  onMount(() => {
    loadCatalog();
    return () => {
      if (progressCleanup) {
        progressCleanup();
      }
    };
  });

  async function loadCatalog() {
    loading = true;
    error = null;
    try {
      const response = await fetchCatalog();
      catalog = response.catalog;
    } catch (e) {
      error = e instanceof Error ? e.message : t('analysis.gallery.errors.catalogLoadFailed');
    } finally {
      loading = false;
    }
  }

  function openLicenseDialog(entry: CatalogEntry) {
    licenseModel = entry;
    licenseDialogRef?.showModal();
  }

  function closeLicenseDialog() {
    licenseDialogRef?.close();
    licenseModel = null;
  }

  async function handleInstall() {
    if (!licenseModel) return;
    const modelId = licenseModel.id;
    closeLicenseDialog();
    installingId = modelId;
    downloadProgress = null;

    try {
      await installModel(modelId);

      // Subscribe to progress events
      progressCleanup = subscribeInstallProgress(
        modelId,
        (progress: DownloadProgress) => {
          downloadProgress = progress;
        },
        () => {
          // Complete
          installingId = null;
          downloadProgress = null;
          progressCleanup = null;
          loadCatalog();
        },
        (err: string) => {
          // Error
          error = err;
          installingId = null;
          downloadProgress = null;
          progressCleanup = null;
        }
      );
    } catch (e) {
      error = e instanceof Error ? e.message : t('analysis.gallery.errors.installFailed');
      installingId = null;
    }
  }

  function openRemoveDialog(entry: CatalogEntry) {
    removeConfirmModel = entry;
    removeDialogRef?.showModal();
  }

  function closeRemoveDialog() {
    removeDialogRef?.close();
    removeConfirmModel = null;
  }

  async function handleUninstall() {
    if (!removeConfirmModel) return;
    const modelId = removeConfirmModel.id;
    closeRemoveDialog();
    deletingId = modelId;

    try {
      await uninstallModel(modelId);
      await loadCatalog();
    } catch (e) {
      error = e instanceof Error ? e.message : t('analysis.gallery.errors.removeFailed');
    } finally {
      deletingId = null;
    }
  }

  function updateThreshold(value: number) {
    settingsActions.updateSection('birdnet', {
      ...birdnet,
      threshold: value,
    });
  }

  /** Compute download percentage for progress bar */
  function progressPercent(p: DownloadProgress): number {
    if (p.totalBytes <= 0) return 0;
    return Math.min(100, Math.round((p.downloadedBytes / p.totalBytes) * 100));
  }

  /** Human-readable status label */
  function statusLabel(status: DownloadProgress['status']): string {
    switch (status) {
      case 'downloading':
        return t('analysis.gallery.progress.downloading');
      case 'verifying':
        return t('analysis.gallery.progress.verifying');
      case 'loading':
        return t('analysis.gallery.progress.loading');
      case 'complete':
        return t('analysis.gallery.progress.complete');
      case 'failed':
        return t('analysis.gallery.progress.failed');
      default:
        return '';
    }
  }
</script>

{#snippet installedTabContent()}
  <div class="space-y-4">
    {#if loading}
      <div class="flex items-center justify-center py-12">
        <Loader2 class="size-6 animate-spin text-[var(--color-primary)]" />
        <span class="ml-3 text-sm text-[var(--color-base-content)]/80"
          >{t('analysis.gallery.loading')}</span
        >
      </div>
    {:else if error}
      <div
        class="flex items-center gap-3 rounded-lg border border-[var(--color-error)]/30 bg-[var(--color-error)]/10 px-4 py-3 text-sm"
        role="alert"
      >
        <AlertTriangle class="size-5 shrink-0 text-[var(--color-error)]" />
        <span class="text-[var(--color-base-content)]">{error}</span>
        <button
          onclick={loadCatalog}
          class="ml-auto flex items-center gap-1.5 rounded-md bg-[var(--color-base-200)] px-3 py-1.5 text-xs font-medium text-[var(--color-base-content)] hover:bg-[var(--color-base-300)] transition-colors"
        >
          <RefreshCw class="size-3.5" />
          {t('analysis.gallery.retry')}
        </button>
      </div>
    {:else}
      <div class="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
        <!-- Built-in BirdNET model (always present) -->
        <div
          class="rounded-lg border border-[var(--color-base-300)] bg-[var(--color-base-200)] p-4"
        >
          <div class="flex items-start gap-3">
            <img src={logoBirdnet} alt="" class="size-10 shrink-0 rounded-lg" />
            <div class="min-w-0 flex-1">
              <h4 class="text-sm font-semibold text-[var(--color-base-content)]">BirdNET v2.4</h4>
              <p class="mt-0.5 line-clamp-2 text-xs text-[var(--color-base-content)]/80">
                {t('analysis.gallery.builtInDescription')}
              </p>
              <p class="mt-1 text-xs text-[var(--color-base-content)]/80">
                Cornell Lab of Ornithology / Chemnitz University
              </p>
            </div>
          </div>
          <div
            class="mt-3 flex items-center justify-between border-t border-[var(--color-base-300)] pt-3"
          >
            <div class="flex items-center gap-2 text-xs text-[var(--color-base-content)]/80">
              <span>v2.4</span>
              <span>{t('analysis.gallery.species', { count: '6,000+' })}</span>
            </div>
            <span
              class="inline-flex items-center gap-1 rounded-full bg-[var(--color-primary)]/15 px-2.5 py-0.5 text-xs font-medium text-[var(--color-primary)]"
            >
              {t('analysis.gallery.builtIn')}
            </span>
          </div>
        </div>

        <!-- Installed additional models -->
        {#each installedEntries as entry (entry.id)}
          {@const isDeleting = deletingId === entry.id}
          {@const logo = getModelLogo(entry.id)}
          <div
            class="rounded-lg border border-[var(--color-base-300)] bg-[var(--color-base-200)] p-4"
          >
            <div class="flex items-start gap-3">
              {#if logo}
                <img src={logo} alt="" class="size-10 shrink-0 rounded-lg" />
              {:else}
                <div class="shrink-0 rounded-lg bg-[var(--color-primary)]/10 p-2.5">
                  {#if entry.category === 'bat'}
                    <Radar size={24} class="text-[var(--color-primary)]" />
                  {:else}
                    <BrainCircuit size={24} class="text-[var(--color-primary)]" />
                  {/if}
                </div>
              {/if}
              <div class="min-w-0 flex-1">
                <h4 class="text-sm font-semibold text-[var(--color-base-content)]">{entry.name}</h4>
                <p class="mt-0.5 line-clamp-2 text-xs text-[var(--color-base-content)]/80">
                  {entry.description}
                </p>
                <p class="mt-1 text-xs text-[var(--color-base-content)]/80">{entry.author}</p>
              </div>
            </div>
            <!-- Metadata grid -->
            <div
              class="mt-3 grid grid-cols-2 gap-x-4 gap-y-1 border-t border-[var(--color-base-300)] pt-3 text-xs"
            >
              {#if entry.region}
                <div class="text-[var(--color-base-content)]/80">
                  {t('analysis.gallery.regionLabel')}
                </div>
                <div class="text-[var(--color-base-content)]/80">{entry.region}</div>
              {/if}
              <div class="text-[var(--color-base-content)]/80">
                {t('analysis.gallery.speciesLabel')}
              </div>
              <div class="text-[var(--color-base-content)]/80">
                {t('analysis.gallery.species', { count: entry.speciesCount })}
              </div>
            </div>
            <!-- Action footer -->
            <div class="mt-3 flex items-center justify-between">
              <span class="text-xs text-[var(--color-base-content)]/80">v{entry.version}</span>
              <button
                onclick={() => openRemoveDialog(entry)}
                disabled={isDeleting}
                class="inline-flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-xs font-medium text-[var(--color-error)] hover:bg-[var(--color-error)]/10 transition-colors disabled:opacity-50"
                aria-label="{t('analysis.gallery.remove')} {entry.name}"
              >
                {#if isDeleting}
                  <Loader2 class="size-3.5 animate-spin" />
                  {t('analysis.gallery.removing')}
                {:else}
                  <Trash2 class="size-3.5" />
                  {t('analysis.gallery.remove')}
                {/if}
              </button>
            </div>
          </div>
        {/each}
      </div>

      {#if installedEntries.length === 0}
        <p class="py-4 text-center text-sm text-[var(--color-base-content)]/80">
          {t('analysis.gallery.noInstalledModels')}
        </p>
      {/if}
    {/if}
  </div>
{/snippet}

{#snippet modelCard(entry: CatalogEntry)}
  {@const isInstalling = installingId === entry.id}
  {@const progress = isInstalling ? downloadProgress : null}
  {@const logo = getModelLogo(entry.id)}
  <div
    class="flex h-full flex-col rounded-lg border border-[var(--color-base-300)] bg-[var(--color-base-200)] p-4"
  >
    <!-- Header: logo + name/description/author -->
    <div class="flex items-start gap-3">
      {#if logo}
        <img src={logo} alt="" class="size-10 shrink-0 rounded-lg" />
      {:else}
        <div class="shrink-0 rounded-lg bg-[var(--color-primary)]/10 p-2.5">
          {#if entry.category === 'bat'}
            <Radar size={24} class="text-[var(--color-primary)]" />
          {:else}
            <BrainCircuit size={24} class="text-[var(--color-primary)]" />
          {/if}
        </div>
      {/if}
      <div class="min-w-0 flex-1">
        <h4 class="text-sm font-semibold text-[var(--color-base-content)]">
          {entry.name}
        </h4>
        <p class="mt-0.5 line-clamp-2 text-xs text-[var(--color-base-content)]/80">
          {entry.description}
        </p>
        <p class="mt-1 text-xs text-[var(--color-base-content)]/80">{entry.author}</p>
      </div>
    </div>

    <!-- Progress bar (shown during install) -->
    {#if progress}
      <div class="mt-3 space-y-1.5">
        <div class="h-2 w-full overflow-hidden rounded-full bg-[var(--color-base-300)]">
          <div
            class="h-full rounded-full bg-[var(--color-primary)] transition-all duration-300"
            style:width="{progressPercent(progress)}%"
          ></div>
        </div>
        <div class="flex items-center justify-between text-xs text-[var(--color-base-content)]/80">
          <span>{statusLabel(progress.status)}</span>
          {#if progress.status === 'downloading' && progress.totalBytes > 0}
            <span>
              {formatBytes(progress.downloadedBytes)} / {formatBytes(progress.totalBytes)}
            </span>
          {/if}
        </div>
      </div>
    {/if}

    <!-- Metadata grid -->
    <div
      class="mt-3 grid grid-cols-2 gap-x-4 gap-y-1 border-t border-[var(--color-base-300)] pt-3 text-xs"
    >
      {#if entry.region}
        <div class="text-[var(--color-base-content)]/80">{t('analysis.gallery.regionLabel')}</div>
        <div class="text-[var(--color-base-content)]">{entry.region}</div>
      {/if}
      <div class="text-[var(--color-base-content)]/80">{t('analysis.gallery.speciesLabel')}</div>
      <div class="text-[var(--color-base-content)]">
        {t('analysis.gallery.species', { count: entry.speciesCount })}
      </div>
      <div class="text-[var(--color-base-content)]/80">{t('analysis.gallery.license.license')}</div>
      <div>
        {#if entry.commercialUse}
          <span
            class="inline-flex items-center gap-1 rounded-full bg-[var(--color-success)]/15 px-2 py-0.5 text-xs text-[var(--color-success)]"
            title={t('analysis.gallery.license.commercialUseAllowed')}
          >
            <Shield class="size-3" />
            {entry.license}
          </span>
        {:else}
          <span
            class="inline-flex items-center gap-1 rounded-full bg-[var(--color-warning)]/15 px-2 py-0.5 text-xs text-[var(--color-warning)]"
            title={t('analysis.gallery.license.nonCommercialOnly')}
          >
            <ShieldAlert class="size-3" />
            {entry.license}
          </span>
        {/if}
      </div>
    </div>

    <!-- Action footer (pushed to bottom via mt-auto) -->
    <div class="mt-auto flex items-center justify-between pt-3">
      <span class="text-xs text-[var(--color-base-content)]/80">v{entry.version}</span>
      <button
        onclick={() => openLicenseDialog(entry)}
        disabled={isInstalling || installingId !== null}
        class="inline-flex items-center gap-1.5 rounded-md bg-[var(--color-primary)] px-3 py-1.5 text-xs font-medium text-[var(--color-primary-content)] hover:bg-[var(--color-primary)]/80 transition-colors disabled:opacity-50"
        aria-label="{t('analysis.gallery.install')} {entry.name}"
      >
        {#if isInstalling}
          <Loader2 class="size-3.5 animate-spin" />
          {t('analysis.gallery.installing')}
        {:else}
          <Download class="size-3.5" />
          {t('analysis.gallery.install')}
        {/if}
      </button>
    </div>
  </div>
{/snippet}

{#snippet availableTabContent()}
  <div class="space-y-6">
    {#if loading}
      <div class="flex items-center justify-center py-12">
        <Loader2 class="size-6 animate-spin text-[var(--color-primary)]" />
        <span class="ml-3 text-sm text-[var(--color-base-content)]/80"
          >{t('analysis.gallery.loading')}</span
        >
      </div>
    {:else if error}
      <div
        class="flex items-center gap-3 rounded-lg border border-[var(--color-error)]/30 bg-[var(--color-error)]/10 px-4 py-3 text-sm"
        role="alert"
      >
        <AlertTriangle class="size-5 shrink-0 text-[var(--color-error)]" />
        <span class="text-[var(--color-base-content)]">{error}</span>
        <button
          onclick={loadCatalog}
          class="ml-auto flex items-center gap-1.5 rounded-md bg-[var(--color-base-200)] px-3 py-1.5 text-xs font-medium text-[var(--color-base-content)] hover:bg-[var(--color-base-300)] transition-colors"
        >
          <RefreshCw class="size-3.5" />
          {t('analysis.gallery.retry')}
        </button>
      </div>
    {:else}
      <!-- Bird Classifiers -->
      {#if availableBirds.length > 0}
        <div>
          <h3
            class="mb-3 text-sm font-semibold uppercase tracking-wider text-[var(--color-base-content)]/80"
          >
            {t('analysis.gallery.categories.bird')}
          </h3>
          <div class="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
            {#each availableBirds as entry (entry.id)}
              {@render modelCard(entry)}
            {/each}
          </div>
        </div>
      {/if}

      <!-- Bat Classifiers -->
      {#if availableBats.length > 0}
        <div>
          <h3
            class="mb-3 text-sm font-semibold uppercase tracking-wider text-[var(--color-base-content)]/80"
          >
            {t('analysis.gallery.categories.bat')}
          </h3>
          <div class="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
            {#each availableBats as entry (entry.id)}
              {@render modelCard(entry)}
            {/each}
          </div>
        </div>
      {/if}

      {#if availableBirds.length === 0 && availableBats.length === 0}
        <p class="py-8 text-center text-sm text-[var(--color-base-content)]/80">
          {t('analysis.gallery.noAvailableModels')}
        </p>
      {/if}
    {/if}
  </div>
{/snippet}

<!-- Main Content -->
<main class="settings-page-content space-y-6" aria-label="Analysis settings configuration">
  <!-- Detection Settings Section -->
  <SettingsSection
    title={t('analysis.detection.title')}
    description={t('analysis.detection.description')}
    defaultOpen={true}
  >
    <div class="space-y-4">
      <div class="grid grid-cols-1 gap-x-6 md:grid-cols-2">
        <NumberField
          label={t('analysis.detection.confidenceThreshold.label')}
          value={birdnet?.threshold ?? 0.3}
          onUpdate={updateThreshold}
          min={0}
          max={1}
          step={0.05}
          disabled={store.isLoading || store.isSaving}
          helpText={t('analysis.detection.confidenceThreshold.helpText')}
        />
      </div>
    </div>
  </SettingsSection>

  <!-- Model Gallery Section -->
  <SettingsSection
    title={t('analysis.gallery.title')}
    description={t('analysis.gallery.description')}
    defaultOpen={true}
  >
    <SettingsTabs tabs={galleryTabs} bind:activeTab={galleryTab} showActions={false} />
  </SettingsSection>
</main>

<!-- License Acceptance Dialog -->
<dialog
  bind:this={licenseDialogRef}
  class="rounded-xl border border-[var(--color-base-300)] bg-[var(--color-base-100)] p-0 shadow-xl backdrop:bg-black/50"
  aria-labelledby="license-dialog-title"
>
  {#if licenseModel}
    <div class="w-full max-w-lg p-6">
      <h3 id="license-dialog-title" class="text-lg font-semibold text-[var(--color-base-content)]">
        {t('analysis.gallery.license.title')}
      </h3>
      <div class="mt-4 space-y-3">
        <div class="rounded-lg bg-[var(--color-base-200)] p-4 text-sm">
          <div class="space-y-2">
            <div class="flex items-center justify-between">
              <span class="text-[var(--color-base-content)]/80"
                >{t('analysis.gallery.license.model')}</span
              >
              <span class="font-medium text-[var(--color-base-content)]">{licenseModel.name}</span>
            </div>
            <div class="flex items-center justify-between">
              <span class="text-[var(--color-base-content)]/80"
                >{t('analysis.gallery.license.author')}</span
              >
              <span class="text-[var(--color-base-content)]">{licenseModel.author}</span>
            </div>
            <div class="flex items-center justify-between">
              <span class="text-[var(--color-base-content)]/80"
                >{t('analysis.gallery.license.license')}</span
              >
              <span class="text-[var(--color-base-content)]">{licenseModel.license}</span>
            </div>
            <div class="flex items-center justify-between">
              <span class="text-[var(--color-base-content)]/80"
                >{t('analysis.gallery.license.commercialUse')}</span
              >
              {#if licenseModel.commercialUse}
                <span class="inline-flex items-center gap-1 text-[var(--color-success)]">
                  <Shield class="size-3.5" />
                  {t('analysis.gallery.license.allowed')}
                </span>
              {:else}
                <span class="inline-flex items-center gap-1 text-[var(--color-warning)]">
                  <ShieldAlert class="size-3.5" />
                  {t('analysis.gallery.license.notAllowed')}
                </span>
              {/if}
            </div>
            <div class="flex items-center justify-between">
              <span class="text-[var(--color-base-content)]/80"
                >{t('analysis.gallery.license.downloadSize')}</span
              >
              <span class="text-[var(--color-base-content)]"
                >{formatBytes(licenseModel.totalSizeBytes)}</span
              >
            </div>
          </div>
        </div>

        {#if !licenseModel.commercialUse}
          <div
            class="flex items-start gap-2 rounded-lg border border-[var(--color-warning)]/30 bg-[var(--color-warning)]/10 px-3 py-2.5 text-sm"
          >
            <ShieldAlert class="mt-0.5 size-4 shrink-0 text-[var(--color-warning)]" />
            <p class="text-[var(--color-base-content)]">
              {t('analysis.gallery.license.nonCommercialWarning')}
            </p>
          </div>
        {/if}
      </div>

      <div class="mt-6 flex justify-end gap-3">
        <button
          onclick={closeLicenseDialog}
          class="rounded-lg border border-[var(--color-base-300)] px-4 py-2 text-sm font-medium text-[var(--color-base-content)] hover:bg-[var(--color-base-200)] transition-colors"
        >
          {t('common.cancel')}
        </button>
        <button
          onclick={handleInstall}
          class="inline-flex items-center gap-2 rounded-lg bg-[var(--color-primary)] px-4 py-2 text-sm font-medium text-[var(--color-primary-content)] hover:bg-[var(--color-primary)]/80 transition-colors"
        >
          <Download class="size-4" />
          {t('analysis.gallery.license.acceptAndInstall')}
        </button>
      </div>
    </div>
  {/if}
</dialog>

<!-- Remove Confirmation Dialog -->
<dialog
  bind:this={removeDialogRef}
  class="rounded-xl border border-[var(--color-base-300)] bg-[var(--color-base-100)] p-0 shadow-xl backdrop:bg-black/50"
  aria-labelledby="remove-dialog-title"
>
  {#if removeConfirmModel}
    <div class="w-full max-w-md p-6">
      <div class="flex items-start gap-3">
        <div class="shrink-0 rounded-full bg-[var(--color-error)]/10 p-2">
          <AlertTriangle class="size-5 text-[var(--color-error)]" />
        </div>
        <div>
          <h3
            id="remove-dialog-title"
            class="text-lg font-semibold text-[var(--color-base-content)]"
          >
            {t('analysis.gallery.removeDialog.title', { name: removeConfirmModel.name })}
          </h3>
          <p class="mt-2 text-sm text-[var(--color-base-content)]/80">
            {t('analysis.gallery.removeDialog.confirmation')}
          </p>
        </div>
      </div>

      <div class="mt-6 flex justify-end gap-3">
        <button
          onclick={closeRemoveDialog}
          class="rounded-lg border border-[var(--color-base-300)] px-4 py-2 text-sm font-medium text-[var(--color-base-content)] hover:bg-[var(--color-base-200)] transition-colors"
        >
          {t('common.cancel')}
        </button>
        <button
          onclick={handleUninstall}
          class="inline-flex items-center gap-2 rounded-lg bg-[var(--color-error)] px-4 py-2 text-sm font-medium text-white hover:bg-[var(--color-error)]/80 transition-colors"
        >
          <Trash2 class="size-4" />
          {t('analysis.gallery.remove')}
        </button>
      </div>
    </div>
  {/if}
</dialog>

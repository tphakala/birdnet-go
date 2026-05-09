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
  } from '@lucide/svelte';

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
      label: 'Installed',
      icon: Package,
      content: installedTabContent,
    },
    {
      id: 'available',
      label: 'Available',
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
      error = e instanceof Error ? e.message : 'Failed to load model catalog';
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
      error = e instanceof Error ? e.message : 'Failed to start model installation';
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
      error = e instanceof Error ? e.message : 'Failed to remove model';
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
        return 'Downloading...';
      case 'verifying':
        return 'Verifying...';
      case 'loading':
        return 'Loading...';
      case 'complete':
        return 'Complete';
      case 'failed':
        return 'Failed';
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
        <span class="ml-3 text-sm text-[var(--color-base-content)]/70">Loading models...</span>
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
          Retry
        </button>
      </div>
    {:else}
      <div class="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
        <!-- Built-in BirdNET model (always present) -->
        <div
          class="rounded-lg border border-[var(--color-base-300)] bg-[var(--color-base-200)] p-4"
        >
          <div class="flex items-start gap-3">
            <div class="shrink-0 rounded-lg bg-[var(--color-primary)]/10 p-2.5">
              <BrainCircuit size={24} class="text-[var(--color-primary)]" />
            </div>
            <div class="min-w-0 flex-1">
              <h4 class="text-sm font-semibold text-[var(--color-base-content)]">BirdNET v2.4</h4>
              <p class="mt-0.5 line-clamp-2 text-xs text-[var(--color-base-content)]/70">
                Built-in bird species classifier by the BirdNET team.
              </p>
              <p class="mt-1 text-xs text-[var(--color-base-content)]/60">
                Cornell Lab of Ornithology / Chemnitz University
              </p>
            </div>
          </div>
          <div
            class="mt-3 flex items-center justify-between border-t border-[var(--color-base-300)] pt-3"
          >
            <div class="flex items-center gap-2 text-xs text-[var(--color-base-content)]/60">
              <span>v2.4</span>
              <span>6,000+ species</span>
            </div>
            <span
              class="inline-flex items-center gap-1 rounded-full bg-[var(--color-primary)]/15 px-2.5 py-0.5 text-xs font-medium text-[var(--color-primary)]"
            >
              Built-in
            </span>
          </div>
        </div>

        <!-- Installed additional models -->
        {#each installedEntries as entry (entry.id)}
          {@const isDeleting = deletingId === entry.id}
          <div
            class="rounded-lg border border-[var(--color-base-300)] bg-[var(--color-base-200)] p-4"
          >
            <div class="flex items-start gap-3">
              <div class="shrink-0 rounded-lg bg-[var(--color-primary)]/10 p-2.5">
                <BrainCircuit size={24} class="text-[var(--color-primary)]" />
              </div>
              <div class="min-w-0 flex-1">
                <h4 class="text-sm font-semibold text-[var(--color-base-content)]">{entry.name}</h4>
                <p class="mt-0.5 line-clamp-2 text-xs text-[var(--color-base-content)]/70">
                  {entry.description}
                </p>
                <p class="mt-1 text-xs text-[var(--color-base-content)]/60">{entry.author}</p>
              </div>
            </div>
            <div
              class="mt-3 flex items-center justify-between border-t border-[var(--color-base-300)] pt-3"
            >
              <div class="flex items-center gap-2 text-xs text-[var(--color-base-content)]/60">
                <span>v{entry.version}</span>
                <span>{entry.speciesCount} species</span>
                {#if entry.region}
                  <span>{entry.region}</span>
                {/if}
              </div>
              <button
                onclick={() => openRemoveDialog(entry)}
                disabled={isDeleting}
                class="inline-flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-xs font-medium text-[var(--color-error)] hover:bg-[var(--color-error)]/10 transition-colors disabled:opacity-50"
                aria-label="Remove {entry.name}"
              >
                {#if isDeleting}
                  <Loader2 class="size-3.5 animate-spin" />
                  Removing...
                {:else}
                  <Trash2 class="size-3.5" />
                  Remove
                {/if}
              </button>
            </div>
          </div>
        {/each}
      </div>

      {#if installedEntries.length === 0}
        <p class="py-4 text-center text-sm text-[var(--color-base-content)]/60">
          No additional models installed. Browse the Available tab to add more classifiers.
        </p>
      {/if}
    {/if}
  </div>
{/snippet}

{#snippet availableTabContent()}
  <div class="space-y-6">
    {#if loading}
      <div class="flex items-center justify-center py-12">
        <Loader2 class="size-6 animate-spin text-[var(--color-primary)]" />
        <span class="ml-3 text-sm text-[var(--color-base-content)]/70">Loading models...</span>
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
          Retry
        </button>
      </div>
    {:else}
      <!-- Bird Classifiers -->
      {#if availableBirds.length > 0}
        <div>
          <h3
            class="mb-3 text-sm font-semibold uppercase tracking-wider text-[var(--color-base-content)]/50"
          >
            Bird Classifiers
          </h3>
          <div class="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
            {#each availableBirds as entry (entry.id)}
              {@const isInstalling = installingId === entry.id}
              {@const progress = isInstalling ? downloadProgress : null}
              <div
                class="rounded-lg border border-[var(--color-base-300)] bg-[var(--color-base-200)] p-4"
              >
                <div class="flex items-start gap-3">
                  <div class="shrink-0 rounded-lg bg-[var(--color-primary)]/10 p-2.5">
                    <BrainCircuit size={24} class="text-[var(--color-primary)]" />
                  </div>
                  <div class="min-w-0 flex-1">
                    <h4 class="text-sm font-semibold text-[var(--color-base-content)]">
                      {entry.name}
                    </h4>
                    <p class="mt-0.5 line-clamp-2 text-xs text-[var(--color-base-content)]/70">
                      {entry.description}
                    </p>
                    <p class="mt-1 text-xs text-[var(--color-base-content)]/60">{entry.author}</p>
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
                    <div
                      class="flex items-center justify-between text-xs text-[var(--color-base-content)]/60"
                    >
                      <span>{statusLabel(progress.status)}</span>
                      {#if progress.status === 'downloading' && progress.totalBytes > 0}
                        <span>
                          {formatBytes(progress.downloadedBytes)} / {formatBytes(
                            progress.totalBytes
                          )}
                        </span>
                      {/if}
                    </div>
                  </div>
                {/if}

                <div
                  class="mt-3 flex items-center justify-between border-t border-[var(--color-base-300)] pt-3"
                >
                  <div class="flex items-center gap-2 text-xs text-[var(--color-base-content)]/60">
                    <span>v{entry.version}</span>
                    <span>{entry.speciesCount} species</span>
                    {#if entry.region}
                      <span>{entry.region}</span>
                    {/if}
                  </div>
                  <div class="flex items-center gap-1.5">
                    <!-- License badge -->
                    {#if entry.commercialUse}
                      <span
                        class="inline-flex items-center gap-1 rounded-full bg-[var(--color-success)]/15 px-2 py-0.5 text-xs text-[var(--color-success)]"
                        title="Commercial use allowed"
                      >
                        <Shield class="size-3" />
                        {entry.license}
                      </span>
                    {:else}
                      <span
                        class="inline-flex items-center gap-1 rounded-full bg-[var(--color-warning)]/15 px-2 py-0.5 text-xs text-[var(--color-warning)]"
                        title="Non-commercial use only"
                      >
                        <ShieldAlert class="size-3" />
                        {entry.license}
                      </span>
                    {/if}
                    <!-- Install button -->
                    <button
                      onclick={() => openLicenseDialog(entry)}
                      disabled={isInstalling || installingId !== null}
                      class="inline-flex items-center gap-1.5 rounded-md bg-[var(--color-primary)] px-2.5 py-1.5 text-xs font-medium text-[var(--color-primary-content)] hover:bg-[var(--color-primary)]/80 transition-colors disabled:opacity-50"
                      aria-label="Install {entry.name}"
                    >
                      {#if isInstalling}
                        <Loader2 class="size-3.5 animate-spin" />
                        Installing...
                      {:else}
                        <Download class="size-3.5" />
                        Install
                      {/if}
                    </button>
                  </div>
                </div>
              </div>
            {/each}
          </div>
        </div>
      {/if}

      <!-- Bat Classifiers -->
      {#if availableBats.length > 0}
        <div>
          <h3
            class="mb-3 text-sm font-semibold uppercase tracking-wider text-[var(--color-base-content)]/50"
          >
            Bat Classifiers
          </h3>
          <div class="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
            {#each availableBats as entry (entry.id)}
              {@const isInstalling = installingId === entry.id}
              {@const progress = isInstalling ? downloadProgress : null}
              <div
                class="rounded-lg border border-[var(--color-base-300)] bg-[var(--color-base-200)] p-4"
              >
                <div class="flex items-start gap-3">
                  <div class="shrink-0 rounded-lg bg-[var(--color-primary)]/10 p-2.5">
                    <BrainCircuit size={24} class="text-[var(--color-primary)]" />
                  </div>
                  <div class="min-w-0 flex-1">
                    <h4 class="text-sm font-semibold text-[var(--color-base-content)]">
                      {entry.name}
                    </h4>
                    <p class="mt-0.5 line-clamp-2 text-xs text-[var(--color-base-content)]/70">
                      {entry.description}
                    </p>
                    <p class="mt-1 text-xs text-[var(--color-base-content)]/60">{entry.author}</p>
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
                    <div
                      class="flex items-center justify-between text-xs text-[var(--color-base-content)]/60"
                    >
                      <span>{statusLabel(progress.status)}</span>
                      {#if progress.status === 'downloading' && progress.totalBytes > 0}
                        <span>
                          {formatBytes(progress.downloadedBytes)} / {formatBytes(
                            progress.totalBytes
                          )}
                        </span>
                      {/if}
                    </div>
                  </div>
                {/if}

                <div
                  class="mt-3 flex items-center justify-between border-t border-[var(--color-base-300)] pt-3"
                >
                  <div class="flex items-center gap-2 text-xs text-[var(--color-base-content)]/60">
                    <span>v{entry.version}</span>
                    <span>{entry.speciesCount} species</span>
                    {#if entry.region}
                      <span>{entry.region}</span>
                    {/if}
                  </div>
                  <div class="flex items-center gap-1.5">
                    <!-- License badge -->
                    {#if entry.commercialUse}
                      <span
                        class="inline-flex items-center gap-1 rounded-full bg-[var(--color-success)]/15 px-2 py-0.5 text-xs text-[var(--color-success)]"
                        title="Commercial use allowed"
                      >
                        <Shield class="size-3" />
                        {entry.license}
                      </span>
                    {:else}
                      <span
                        class="inline-flex items-center gap-1 rounded-full bg-[var(--color-warning)]/15 px-2 py-0.5 text-xs text-[var(--color-warning)]"
                        title="Non-commercial use only"
                      >
                        <ShieldAlert class="size-3" />
                        {entry.license}
                      </span>
                    {/if}
                    <!-- Install button -->
                    <button
                      onclick={() => openLicenseDialog(entry)}
                      disabled={isInstalling || installingId !== null}
                      class="inline-flex items-center gap-1.5 rounded-md bg-[var(--color-primary)] px-2.5 py-1.5 text-xs font-medium text-[var(--color-primary-content)] hover:bg-[var(--color-primary)]/80 transition-colors disabled:opacity-50"
                      aria-label="Install {entry.name}"
                    >
                      {#if isInstalling}
                        <Loader2 class="size-3.5 animate-spin" />
                        Installing...
                      {:else}
                        <Download class="size-3.5" />
                        Install
                      {/if}
                    </button>
                  </div>
                </div>
              </div>
            {/each}
          </div>
        </div>
      {/if}

      {#if availableBirds.length === 0 && availableBats.length === 0}
        <p class="py-8 text-center text-sm text-[var(--color-base-content)]/60">
          No additional models available for your platform.
        </p>
      {/if}
    {/if}
  </div>
{/snippet}

<!-- Main Content -->
<main class="settings-page-content space-y-6" aria-label="Analysis settings configuration">
  <!-- Detection Settings Section -->
  <SettingsSection
    title="Detection Settings"
    description="Configure confidence thresholds for species classification."
    defaultOpen={true}
  >
    <div class="space-y-4">
      <div class="grid grid-cols-1 gap-x-6 md:grid-cols-2">
        <NumberField
          label="Confidence Threshold"
          value={birdnet?.threshold ?? 0.3}
          onUpdate={updateThreshold}
          min={0}
          max={1}
          step={0.05}
          disabled={store.isLoading || store.isSaving}
          helpText="Minimum confidence score (0.0 - 1.0) required for a detection to be recorded."
        />
      </div>
    </div>
  </SettingsSection>

  <!-- Model Gallery Section -->
  <SettingsSection
    title="Model Gallery"
    description="Manage classifier models for bird and bat detection."
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
        License Agreement
      </h3>
      <div class="mt-4 space-y-3">
        <div class="rounded-lg bg-[var(--color-base-200)] p-4 text-sm">
          <div class="space-y-2">
            <div class="flex items-center justify-between">
              <span class="text-[var(--color-base-content)]/60">Model</span>
              <span class="font-medium text-[var(--color-base-content)]">{licenseModel.name}</span>
            </div>
            <div class="flex items-center justify-between">
              <span class="text-[var(--color-base-content)]/60">Author</span>
              <span class="text-[var(--color-base-content)]">{licenseModel.author}</span>
            </div>
            <div class="flex items-center justify-between">
              <span class="text-[var(--color-base-content)]/60">License</span>
              <span class="text-[var(--color-base-content)]">{licenseModel.license}</span>
            </div>
            <div class="flex items-center justify-between">
              <span class="text-[var(--color-base-content)]/60">Commercial Use</span>
              {#if licenseModel.commercialUse}
                <span class="inline-flex items-center gap-1 text-[var(--color-success)]">
                  <Shield class="size-3.5" />
                  Allowed
                </span>
              {:else}
                <span class="inline-flex items-center gap-1 text-[var(--color-warning)]">
                  <ShieldAlert class="size-3.5" />
                  Not allowed
                </span>
              {/if}
            </div>
            <div class="flex items-center justify-between">
              <span class="text-[var(--color-base-content)]/60">Download Size</span>
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
              This model is licensed for non-commercial use only. By installing, you agree to comply
              with the license terms.
            </p>
          </div>
        {/if}
      </div>

      <div class="mt-6 flex justify-end gap-3">
        <button
          onclick={closeLicenseDialog}
          class="rounded-lg border border-[var(--color-base-300)] px-4 py-2 text-sm font-medium text-[var(--color-base-content)] hover:bg-[var(--color-base-200)] transition-colors"
        >
          Cancel
        </button>
        <button
          onclick={handleInstall}
          class="inline-flex items-center gap-2 rounded-lg bg-[var(--color-primary)] px-4 py-2 text-sm font-medium text-[var(--color-primary-content)] hover:bg-[var(--color-primary)]/80 transition-colors"
        >
          <Download class="size-4" />
          Accept & Install
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
            Remove {removeConfirmModel.name}?
          </h3>
          <p class="mt-2 text-sm text-[var(--color-base-content)]/70">
            This will remove the model files from disk. Label data is retained for existing
            detections.
          </p>
        </div>
      </div>

      <div class="mt-6 flex justify-end gap-3">
        <button
          onclick={closeRemoveDialog}
          class="rounded-lg border border-[var(--color-base-300)] px-4 py-2 text-sm font-medium text-[var(--color-base-content)] hover:bg-[var(--color-base-200)] transition-colors"
        >
          Cancel
        </button>
        <button
          onclick={handleUninstall}
          class="inline-flex items-center gap-2 rounded-lg bg-[var(--color-error)] px-4 py-2 text-sm font-medium text-white hover:bg-[var(--color-error)]/80 transition-colors"
        >
          <Trash2 class="size-4" />
          Remove
        </button>
      </div>
    </div>
  {/if}
</dialog>

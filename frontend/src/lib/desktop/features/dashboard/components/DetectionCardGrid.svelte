<!--
  DetectionCardGrid.svelte

  A card-based grid layout for displaying recent bird detections.
  Features prominent spectrogram visualization with overlaid metadata.

  Usage:
  - Dashboard view for recent detections
  - Visual browsing of detection recordings

  Props:
  - data: Detection[] - Array of detection objects
  - loading?: boolean - Loading state
  - error?: string | null - Error message
  - onRefresh: () => void - Callback to refresh data
  - limit?: number - Number of detections to show
  - onLimitChange?: (limit: number) => void - Callback when limit changes
  - newDetectionIds?: Set<number> - IDs of newly arrived detections
  - onFreezeStart?: () => void - Callback when interaction starts (freeze updates)
  - onFreezeEnd?: () => void - Callback when interaction ends (resume updates)
  - updatesAreFrozen?: boolean - Whether updates are currently frozen
  - className?: string - Additional CSS classes
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import DetectionCard from './DetectionCard.svelte';
  import ConfirmModal from '$lib/desktop/components/modals/ConfirmModal.svelte';
  import { fetchWithCSRF } from '$lib/utils/api';
  import type { Detection } from '$lib/types/detection.types';
  import { RefreshCw, XCircle, ChevronDown, Check } from '@lucide/svelte';
  import { onMount } from 'svelte';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { cn } from '$lib/utils/cn';
  import { buildAppUrl } from '$lib/utils/urlHelpers';

  const logger = loggers.ui;

  interface Props {
    data: Detection[];
    loading?: boolean;
    error?: string | null;
    onRefresh: () => void;
    limit?: number;
    // eslint-disable-next-line no-unused-vars
    onLimitChange?: (limit: number) => void;
    newDetectionIds?: Set<number>;
    onFreezeStart?: () => void;
    onFreezeEnd?: () => void;
    updatesAreFrozen?: boolean;
    className?: string;
  }

  let {
    data = [],
    loading = false,
    error = null,
    onRefresh,
    limit = 6,
    onLimitChange,
    newDetectionIds = new Set(),
    onFreezeStart,
    onFreezeEnd,
    updatesAreFrozen = false,
    className = '',
  }: Props = $props();

  // State for number of detections to show - captures initial prop value without creating dependency
  // Uses untrack() to explicitly capture initial value only (local state is independent after init)
  let selectedLimit = $state(untrack(() => limit));

  // Track excluded species by common name (session-local tracking)
  // Note: In production, this could be fetched from the API on mount for persistence
  let excludedSpecies = $state(new Set<string>());

  // Helper to check if a species is excluded
  function isSpeciesExcluded(commonName: string): boolean {
    return excludedSpecies.has(commonName);
  }

  // Custom dropdown state
  let showLimitDropdown = $state(false);
  let dropdownRef = $state<HTMLDivElement | undefined>(undefined);
  let dropdownButtonRef = $state<HTMLButtonElement | undefined>(undefined);
  const limitOptions = [6, 12, 24, 48];

  // Toggle dropdown
  function toggleLimitDropdown() {
    showLimitDropdown = !showLimitDropdown;
  }

  // Select a limit option
  function selectLimit(value: number) {
    handleLimitChange(value);
    showLimitDropdown = false;
  }

  // Handle click outside to close dropdown
  function handleDropdownClickOutside(event: MouseEvent) {
    if (!showLimitDropdown) return;
    const target = event.target as Node;
    if (!dropdownRef?.contains(target) && !dropdownButtonRef?.contains(target)) {
      showLimitDropdown = false;
    }
  }

  // Handle keyboard navigation for dropdown
  function handleDropdownKeyDown(event: KeyboardEvent) {
    if (!showLimitDropdown) {
      if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault();
        showLimitDropdown = true;
      }
      return;
    }

    switch (event.key) {
      case 'Escape':
        showLimitDropdown = false;
        dropdownButtonRef?.focus();
        break;
      case 'ArrowDown':
        event.preventDefault();
        {
          const currentIndex = limitOptions.indexOf(selectedLimit);
          const nextIndex = Math.min(currentIndex + 1, limitOptions.length - 1);
          const nextOption = limitOptions.at(nextIndex);
          if (nextOption !== undefined) selectLimit(nextOption);
        }
        break;
      case 'ArrowUp':
        event.preventDefault();
        {
          const currentIndex = limitOptions.indexOf(selectedLimit);
          const prevIndex = Math.max(currentIndex - 1, 0);
          const prevOption = limitOptions.at(prevIndex);
          if (prevOption !== undefined) selectLimit(prevOption);
        }
        break;
    }
  }

  onMount(() => {
    document.addEventListener('click', handleDropdownClickOutside);
    return () => {
      document.removeEventListener('click', handleDropdownClickOutside);
    };
  });

  // Updates the number of detections to display and persists the preference
  function handleLimitChange(newLimit: number) {
    selectedLimit = newLimit;

    if (typeof window !== 'undefined') {
      try {
        localStorage.setItem('recentDetectionLimit', newLimit.toString());
      } catch (e) {
        logger.error('Failed to save detection limit:', e);
      }
    }

    if (onLimitChange) {
      onLimitChange(newLimit);
    }
  }

  // Modal states
  let showConfirmModal = $state(false);
  let selectedDetection = $state<Detection | null>(null);
  let confirmModalConfig = $state({
    title: '',
    message: '',
    confirmLabel: 'Confirm',
    onConfirm: async () => {},
  });

  // Action handlers
  function handleReview(detection: Detection) {
    window.location.href = buildAppUrl(`/ui/detections/${detection.id}?tab=review`);
  }

  function handleToggleSpecies(detection: Detection) {
    const isExcluded = isSpeciesExcluded(detection.commonName);
    confirmModalConfig = {
      title: isExcluded
        ? t('dashboard.recentDetections.modals.showSpecies', { species: detection.commonName })
        : t('dashboard.recentDetections.modals.ignoreSpecies', { species: detection.commonName }),
      message: isExcluded
        ? t('dashboard.recentDetections.modals.showSpeciesConfirm', {
            species: detection.commonName,
          })
        : t('dashboard.recentDetections.modals.ignoreSpeciesConfirm', {
            species: detection.commonName,
          }),
      confirmLabel: t('common.buttons.confirm'),
      onConfirm: async () => {
        try {
          await fetchWithCSRF('/api/v2/detections/ignore', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ common_name: detection.commonName }),
          });
          // Toggle local exclusion state
          if (isExcluded) {
            excludedSpecies.delete(detection.commonName);
          } else {
            excludedSpecies.add(detection.commonName);
          }
          // Trigger reactivity by reassigning
          excludedSpecies = new Set(excludedSpecies);
          onRefresh();
        } catch (err) {
          logger.error('Error toggling species exclusion:', err);
        }
      },
    };
    selectedDetection = detection;
    showConfirmModal = true;
  }

  function handleToggleLock(detection: Detection) {
    confirmModalConfig = {
      title: detection.locked
        ? t('dashboard.recentDetections.modals.unlockDetection')
        : t('dashboard.recentDetections.modals.lockDetection'),
      message: detection.locked
        ? t('dashboard.recentDetections.modals.unlockDetectionConfirm', {
            species: detection.commonName,
          })
        : t('dashboard.recentDetections.modals.lockDetectionConfirm', {
            species: detection.commonName,
          }),
      confirmLabel: t('common.buttons.confirm'),
      onConfirm: async () => {
        try {
          await fetchWithCSRF(`/api/v2/detections/${detection.id}/lock`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ locked: !detection.locked }),
          });
          onRefresh();
        } catch (err) {
          logger.error('Error toggling lock status:', err);
        }
      },
    };
    selectedDetection = detection;
    showConfirmModal = true;
  }

  function handleDelete(detection: Detection) {
    confirmModalConfig = {
      title: t('dashboard.recentDetections.modals.deleteDetection', {
        species: detection.commonName,
      }),
      message: t('dashboard.recentDetections.modals.deleteDetectionConfirm', {
        species: detection.commonName,
      }),
      confirmLabel: t('common.buttons.delete'),
      onConfirm: async () => {
        try {
          await fetchWithCSRF(`/api/v2/detections/${detection.id}`, {
            method: 'DELETE',
          });
          onRefresh();
        } catch (err) {
          logger.error('Error deleting detection:', err);
        }
      },
    };
    selectedDetection = detection;
    showConfirmModal = true;
  }
</script>

<section
  class={cn(
    'card col-span-12 bg-base-100 shadow-sm rounded-2xl border border-border-100 overflow-hidden',
    className
  )}
>
  <!-- Card Header -->
  <div class="px-6 py-4 border-b border-base-200">
    <div class="flex items-center justify-between">
      <div class="flex flex-col">
        <h3 class="font-semibold">{t('dashboard.recentDetections.title')}</h3>
        <p class="text-sm text-base-content/60">{t('dashboard.recentDetections.subtitle')}</p>
      </div>
      <div class="flex items-center gap-3">
        <!-- Custom Show Limit Dropdown -->
        <div class="limit-dropdown-container">
          <button
            bind:this={dropdownButtonRef}
            type="button"
            class="limit-dropdown-trigger"
            onclick={toggleLimitDropdown}
            onkeydown={handleDropdownKeyDown}
            aria-expanded={showLimitDropdown}
            aria-haspopup="listbox"
            aria-label={t('dashboard.recentDetections.controls.show') + ' ' + selectedLimit}
          >
            <span class="limit-dropdown-value">{selectedLimit}</span>
            <ChevronDown
              class={cn('limit-dropdown-icon', showLimitDropdown && 'limit-dropdown-icon-open')}
            />
          </button>

          {#if showLimitDropdown}
            <div
              bind:this={dropdownRef}
              class="limit-dropdown-menu"
              role="listbox"
              aria-label={t('dashboard.recentDetections.controls.show')}
            >
              {#each limitOptions as option (option)}
                <button
                  type="button"
                  class={cn(
                    'limit-dropdown-option',
                    selectedLimit === option && 'limit-dropdown-option-selected'
                  )}
                  role="option"
                  aria-selected={selectedLimit === option}
                  onclick={() => selectLimit(option)}
                >
                  <span class="limit-dropdown-option-text">{option}</span>
                  {#if selectedLimit === option}
                    <Check class="limit-dropdown-check" />
                  {/if}
                </button>
              {/each}
            </div>
          {/if}
        </div>

        <button
          onclick={onRefresh}
          class="btn btn-sm btn-ghost"
          class:opacity-50={updatesAreFrozen}
          disabled={loading || updatesAreFrozen}
          title={updatesAreFrozen
            ? t('dashboard.recentDetections.controls.refreshPaused')
            : t('dashboard.recentDetections.controls.refresh')}
          aria-label={t('dashboard.recentDetections.controls.refresh')}
        >
          <RefreshCw class={loading ? 'size-4 animate-spin' : 'size-4'} />
        </button>
      </div>
    </div>
  </div>

  <!-- Content -->
  <div class="p-4">
    {#if error}
      <div class="alert alert-error">
        <XCircle class="size-6" />
        <span>{error}</span>
      </div>
    {:else}
      <div class="relative">
        <!-- Loading overlay -->
        {#if loading}
          <div
            class="absolute inset-0 bg-base-100/80 z-20 flex items-center justify-center rounded-lg pointer-events-none"
          >
            <span class="loading loading-spinner loading-md"></span>
          </div>
        {/if}

        <!-- Detection Cards Grid -->
        <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
          {#each data.slice(0, selectedLimit) as detection (detection.id)}
            <DetectionCard
              {detection}
              isNew={newDetectionIds.has(detection.id)}
              isExcluded={isSpeciesExcluded(detection.commonName)}
              {onFreezeStart}
              {onFreezeEnd}
              onReview={() => handleReview(detection)}
              onToggleSpecies={() => handleToggleSpecies(detection)}
              onToggleLock={() => handleToggleLock(detection)}
              onDelete={() => handleDelete(detection)}
            />
          {/each}
        </div>

        {#if data.length === 0}
          <div class="text-center py-8 text-base-content/60">
            {t('dashboard.recentDetections.noDetections')}
          </div>
        {/if}
      </div>
    {/if}
  </div>
</section>

<!-- Modals -->
{#if selectedDetection}
  <ConfirmModal
    isOpen={showConfirmModal}
    title={confirmModalConfig.title}
    message={confirmModalConfig.message}
    confirmLabel={confirmModalConfig.confirmLabel}
    onClose={() => {
      showConfirmModal = false;
      selectedDetection = null;
    }}
    onConfirm={async () => {
      try {
        await confirmModalConfig.onConfirm();
      } finally {
        showConfirmModal = false;
        selectedDetection = null;
      }
    }}
  />
{/if}

<style>
  /* ========================================================================
     Custom Limit Dropdown Styling
     ======================================================================== */

  .limit-dropdown-container {
    position: relative;
  }

  .limit-dropdown-trigger {
    display: inline-flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    min-width: 4.5rem;
    padding: 0.5rem 0.75rem;
    font-size: 0.875rem;
    font-weight: 600;
    border-radius: 0.5rem;
    border: 1px solid rgb(226 232 240); /* slate-200 */
    background-color: rgb(255 255 255);
    color: rgb(15 23 42); /* slate-900 */
    cursor: pointer;
    transition: all 150ms ease;
  }

  .limit-dropdown-trigger:hover {
    background-color: rgb(248 250 252); /* slate-50 */
    border-color: rgb(203 213 225); /* slate-300 */
  }

  .limit-dropdown-trigger:focus {
    outline: none;
  }

  :global([data-theme='dark']) .limit-dropdown-trigger {
    background-color: rgb(30 41 59); /* slate-800 */
    border-color: rgb(71 85 105); /* slate-600 */
    color: rgb(241 245 249); /* slate-100 */
  }

  :global([data-theme='dark']) .limit-dropdown-trigger:hover {
    background-color: rgb(51 65 85); /* slate-700 */
    border-color: rgb(100 116 139); /* slate-500 */
  }

  .limit-dropdown-value {
    font-variant-numeric: tabular-nums;
  }

  .limit-dropdown-icon {
    width: 1rem;
    height: 1rem;
    color: rgb(148 163 184); /* slate-400 */
    transition: transform 200ms ease;
  }

  .limit-dropdown-icon-open {
    transform: rotate(180deg);
  }

  :global([data-theme='dark']) .limit-dropdown-icon {
    color: rgb(100 116 139); /* slate-500 */
  }

  .limit-dropdown-menu {
    position: absolute;
    top: calc(100% + 0.25rem);
    right: 0;
    z-index: 100;
    min-width: 5rem;
    padding: 0.25rem;
    border-radius: 0.5rem;
    border: 1px solid rgb(226 232 240); /* slate-200 */
    background-color: rgb(255 255 255);
    box-shadow:
      0 10px 15px -3px rgba(0, 0, 0, 0.1),
      0 4px 6px -2px rgba(0, 0, 0, 0.05);
    animation: dropdown-enter 150ms ease-out;
  }

  @keyframes dropdown-enter {
    from {
      opacity: 0;
      transform: translateY(-0.25rem);
    }

    to {
      opacity: 1;
      transform: translateY(0);
    }
  }

  :global([data-theme='dark']) .limit-dropdown-menu {
    background-color: rgb(30 41 59); /* slate-800 */
    border-color: rgb(51 65 85); /* slate-700 */
    box-shadow:
      0 10px 15px -3px rgba(0, 0, 0, 0.3),
      0 4px 6px -2px rgba(0, 0, 0, 0.2);
  }

  .limit-dropdown-option {
    display: flex;
    align-items: center;
    justify-content: space-between;
    width: 100%;
    padding: 0.5rem 0.75rem;
    font-size: 0.875rem;
    font-weight: 500;
    border-radius: 0.375rem;
    background-color: transparent;
    color: rgb(51 65 85); /* slate-700 */
    cursor: pointer;
    transition: all 100ms ease;
  }

  .limit-dropdown-option:hover {
    background-color: rgb(241 245 249); /* slate-100 */
    color: rgb(15 23 42); /* slate-900 */
  }

  :global([data-theme='dark']) .limit-dropdown-option {
    color: rgb(203 213 225); /* slate-300 */
  }

  :global([data-theme='dark']) .limit-dropdown-option:hover {
    background-color: rgb(51 65 85); /* slate-700 */
    color: rgb(241 245 249); /* slate-100 */
  }

  .limit-dropdown-option-selected {
    background-color: rgb(240 249 255); /* sky-50 */
    color: rgb(2 132 199); /* sky-600 */
  }

  .limit-dropdown-option-selected:hover {
    background-color: rgb(224 242 254); /* sky-100 */
  }

  :global([data-theme='dark']) .limit-dropdown-option-selected {
    background-color: rgb(12 74 110); /* sky-900 */
    color: rgb(125 211 252); /* sky-300 */
  }

  :global([data-theme='dark']) .limit-dropdown-option-selected:hover {
    background-color: rgb(7 89 133); /* sky-800 */
    color: rgb(186 230 253); /* sky-200 */
  }

  .limit-dropdown-option-text {
    font-variant-numeric: tabular-nums;
  }

  .limit-dropdown-check {
    width: 1rem;
    height: 1rem;
    color: rgb(2 132 199); /* sky-600 */
  }

  :global([data-theme='dark']) .limit-dropdown-check {
    color: rgb(125 211 252); /* sky-300 */
  }
</style>

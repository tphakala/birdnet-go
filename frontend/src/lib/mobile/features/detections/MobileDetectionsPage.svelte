<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '$lib/i18n';
  import { fetchWithCSRF } from '$lib/utils/api';
  import { actionIcons } from '$lib/utils/icons';
  import { getLogger } from '$lib/utils/logger';
  import DetectionRow from '../../components/detection/DetectionRow.svelte';
  import FilterModal, { type FilterState } from '../../components/ui/FilterModal.svelte';
  import FilterBadge from '../../components/ui/FilterBadge.svelte';

  const logger = getLogger('mobile-detections');

  interface Detection {
    id: number;
    commonName: string;
    scientificName: string;
    confidence: number;
    date: string;
    time: string;
    thumbnailUrl?: string;
  }

  let loading = $state(true);
  let error = $state<string | null>(null);
  let detections = $state<Detection[]>([]);
  let expandedId = $state<number | null>(null);
  let searchQuery = $state('');
  let hasMore = $state(true);
  let loadingMore = $state(false);

  // Audio playback
  let audioElement: HTMLAudioElement | null = $state(null);
  let playingId = $state<number | null>(null);

  // Filter state
  let showFilters = $state(false);
  let filters = $state<FilterState>({
    species: '',
    startDate: '',
    endDate: '',
    confidenceMin: 0,
    timeOfDay: [],
    hourStart: 0,
    hourEnd: 23,
    verified: '',
  });

  // Derive active filter count
  let activeFilterCount = $derived.by(() => {
    let count = 0;
    if (filters.species) count++;
    if (filters.startDate || filters.endDate) count++;
    if (filters.confidenceMin > 0) count++;
    if (filters.timeOfDay.length > 0) count++;
    if (filters.hourStart > 0 || filters.hourEnd < 23) count++;
    if (filters.verified) count++;
    return count;
  });

  // Debounce utility
  function debounce(fn: () => void, delay: number): () => void {
    let timeoutId: ReturnType<typeof setTimeout>;
    return () => {
      clearTimeout(timeoutId);
      timeoutId = setTimeout(() => fn(), delay);
    };
  }

  const debouncedSearch = debounce(() => {
    loadDetections();
  }, 300);

  async function loadDetections(append = false) {
    try {
      error = null;
      if (!append) loading = true;

      const offset = append ? detections.length : 0;
      const queryParams = new URLSearchParams({
        numResults: '20',
        offset: String(offset),
      });

      // Add search query
      if (searchQuery) {
        queryParams.set('search', searchQuery);
      }

      // Add filter parameters
      if (filters.species) {
        queryParams.set('species', filters.species);
      }
      if (filters.startDate) {
        queryParams.set('start_date', filters.startDate);
      }
      if (filters.endDate) {
        queryParams.set('end_date', filters.endDate);
      }
      if (filters.confidenceMin > 0) {
        queryParams.set('confidence', `>=${filters.confidenceMin}`);
      }
      if (filters.timeOfDay.length > 0) {
        queryParams.set('timeOfDay', filters.timeOfDay.join(','));
      }
      if (filters.hourStart > 0 || filters.hourEnd < 23) {
        queryParams.set('hourRange', `${filters.hourStart}-${filters.hourEnd}`);
      }
      if (filters.verified) {
        queryParams.set('verified', filters.verified);
      }

      const response = (await fetchWithCSRF(`/api/v2/detections?${queryParams.toString()}`)) as {
        data?: Detection[];
        total?: number;
      };

      const newDetections = response.data ?? [];

      if (append) {
        detections = [...detections, ...newDetections];
      } else {
        detections = newDetections;
      }

      hasMore = response.total ? detections.length < response.total : newDetections.length === 20;
    } catch (err) {
      logger.error('Failed to load detections', err);
      error = 'Failed to load detections';
    } finally {
      loading = false;
      loadingMore = false;
    }
  }

  async function loadMore() {
    if (loadingMore || !hasMore) return;
    loadingMore = true;
    await loadDetections(true);
  }

  function toggleExpanded(id: number) {
    expandedId = expandedId === id ? null : id;
  }

  function handlePlay(detection: Detection) {
    if (!audioElement) return;

    // If same detection is playing, toggle pause/play
    if (playingId === detection.id) {
      if (audioElement.paused) {
        audioElement.play().catch(err => logger.error('Audio play failed', err));
      } else {
        audioElement.pause();
      }
      return;
    }

    // Play new detection
    playingId = detection.id;
    audioElement.src = `/api/v2/audio/${detection.id}`;
    audioElement.play().catch(err => {
      logger.error('Audio play failed', err);
      playingId = null;
    });
  }

  function handleAudioEnded() {
    playingId = null;
  }

  function handleVerify(detection: Detection) {
    // TODO: Implement verify action
    logger.debug('Verify detection', { id: detection.id });
  }

  function handleDismiss(detection: Detection) {
    // TODO: Implement dismiss action
    logger.debug('Dismiss detection', { id: detection.id });
  }

  function handleSearchInput() {
    debouncedSearch();
  }

  function handleSearch() {
    loadDetections();
  }

  function handleApplyFilters(newFilters: FilterState) {
    filters = newFilters;
    showFilters = false;
    loadDetections();
  }

  function handleClearFilters() {
    filters = {
      species: '',
      startDate: '',
      endDate: '',
      confidenceMin: 0,
      timeOfDay: [],
      hourStart: 0,
      hourEnd: 23,
      verified: '',
    };
  }

  onMount(() => {
    loadDetections();
  });
</script>

<div class="flex flex-col h-full">
  <!-- Search Header -->
  <div class="sticky top-0 z-10 bg-base-200 p-3 border-b border-base-300">
    <div class="flex gap-2">
      <div class="relative flex-1">
        <input
          type="search"
          bind:value={searchQuery}
          oninput={handleSearchInput}
          onkeydown={e => e.key === 'Enter' && handleSearch()}
          placeholder={t('detections.searchPlaceholder')}
          class="input input-bordered w-full pl-10"
        />
        <span class="absolute left-3 top-1/2 -translate-y-1/2 text-base-content/40">
          {@html actionIcons.search}
        </span>
      </div>
      <button
        class="btn btn-ghost relative"
        aria-label="Filters"
        onclick={() => (showFilters = true)}
      >
        {@html actionIcons.filter}
        <FilterBadge count={activeFilterCount} />
      </button>
    </div>
  </div>

  <!-- Detections List -->
  <div class="flex-1 overflow-y-auto">
    {#if loading}
      <div class="flex justify-center py-12">
        <span class="loading loading-spinner loading-lg"></span>
      </div>
    {:else if error}
      <div class="p-4">
        <div class="alert alert-error">
          <span>{error}</span>
          <button class="btn btn-sm" onclick={() => loadDetections()}>Retry</button>
        </div>
      </div>
    {:else}
      <div class="divide-y divide-base-200">
        {#each detections as detection (detection.id)}
          <DetectionRow
            {detection}
            expanded={expandedId === detection.id}
            onToggle={() => toggleExpanded(detection.id)}
            onPlay={() => handlePlay(detection)}
            onVerify={() => handleVerify(detection)}
            onDismiss={() => handleDismiss(detection)}
          />
        {/each}

        {#if detections.length === 0}
          <div class="text-center py-12 text-base-content/60">
            {t('detections.noResults')}
          </div>
        {/if}
      </div>

      <!-- Load More -->
      {#if hasMore && detections.length > 0}
        <div class="p-4 text-center">
          <button class="btn btn-ghost" onclick={loadMore} disabled={loadingMore}>
            {#if loadingMore}
              <span class="loading loading-spinner loading-sm"></span>
            {:else}
              {t('common.loadMore')}
            {/if}
          </button>
        </div>
      {/if}
    {/if}
  </div>
</div>

<!-- Filter Modal -->
<FilterModal
  open={showFilters}
  bind:filters
  onClose={() => (showFilters = false)}
  onApply={handleApplyFilters}
  onClear={handleClearFilters}
/>

<!-- Hidden audio element for playback -->
<audio bind:this={audioElement} onended={handleAudioEnded} hidden></audio>

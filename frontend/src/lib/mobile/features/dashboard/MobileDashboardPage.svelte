<script lang="ts">
  import { onMount } from 'svelte';
  import { getLogger } from '$lib/utils/logger';
  import { t } from '$lib/i18n';
  import { getLocalDateString } from '$lib/utils/date';
  import DashboardSummaryCard from './DashboardSummaryCard.svelte';
  import DetectionRow from '../../components/detection/DetectionRow.svelte';
  import type { Detection } from '$lib/types/detection.types';

  const logger = getLogger('mobile-dashboard');

  interface DashboardData {
    totalDetections: number;
    uniqueSpecies: number;
    topSpecies: string | null;
    recentDetections: Detection[];
  }

  let loading = $state(true);
  let error = $state<string | null>(null);
  let data = $state<DashboardData | null>(null);
  let expandedId = $state<number | null>(null);

  async function loadDashboard() {
    try {
      error = null;
      loading = true;

      // Fetch daily summary for today
      const today = getLocalDateString();
      const summaryResponse = await fetch(`/api/v2/analytics/species/daily?date=${today}`);
      if (!summaryResponse.ok) {
        throw new Error(`Failed to fetch summary: ${summaryResponse.statusText}`);
      }
      const summaryData = await summaryResponse.json();

      // Fetch recent detections
      const detectionsResponse = await fetch(
        '/api/v2/detections/recent?limit=10&includeWeather=false'
      );
      if (!detectionsResponse.ok) {
        throw new Error(`Failed to fetch detections: ${detectionsResponse.statusText}`);
      }
      const detectionsData = await detectionsResponse.json();

      // Calculate summary stats from daily summary
      const totalDetections = summaryData.reduce(
        (sum: number, species: { count: number }) => sum + species.count,
        0
      );
      const uniqueSpecies = summaryData.length;
      const topSpecies = summaryData.length > 0 ? summaryData[0].common_name : null;

      data = {
        totalDetections,
        uniqueSpecies,
        topSpecies,
        recentDetections: detectionsData,
      };
    } catch (err) {
      logger.error('Failed to load dashboard', err);
      error = err instanceof Error ? err.message : 'Failed to load dashboard data';
    } finally {
      loading = false;
    }
  }

  function toggleExpanded(id: number) {
    expandedId = expandedId === id ? null : id;
  }

  function handlePlay(detection: Detection) {
    // TODO: Implement audio playback
    logger.debug('Play detection', { id: detection.id });
  }

  function handleVerify(detection: Detection) {
    // TODO: Implement verify action
    logger.debug('Verify detection', { id: detection.id });
  }

  function handleDismiss(detection: Detection) {
    // TODO: Implement dismiss action
    logger.debug('Dismiss detection', { id: detection.id });
  }

  function navigateToDetections() {
    window.location.href = '/ui/detections';
  }

  onMount(() => {
    loadDashboard();
  });
</script>

<div class="flex flex-col gap-4 p-4">
  {#if loading}
    <div class="flex justify-center py-12">
      <span class="loading loading-spinner loading-lg"></span>
    </div>
  {:else if error}
    <div class="alert alert-error">
      <span>{error}</span>
      <button class="btn btn-sm" onclick={loadDashboard}>Retry</button>
    </div>
  {:else if data}
    <!-- Summary Card -->
    <DashboardSummaryCard
      totalDetections={data.totalDetections}
      uniqueSpecies={data.uniqueSpecies}
      topSpecies={data.topSpecies}
    />

    <!-- Recent Detections -->
    <div class="card bg-base-100 shadow-sm">
      <div class="card-body p-4">
        <div class="flex items-center justify-between">
          <h2 class="card-title text-base">{t('dashboard.recentDetections.title')}</h2>
          <button class="btn btn-ghost btn-xs" onclick={navigateToDetections}>
            {t('common.buttons.viewAll')}
          </button>
        </div>

        <div class="divide-y divide-base-200 -mx-4 mt-2">
          {#each data.recentDetections.slice(0, 5) as detection (detection.id)}
            <DetectionRow
              {detection}
              expanded={expandedId === detection.id}
              onToggle={() => toggleExpanded(detection.id)}
              onPlay={() => handlePlay(detection)}
              onVerify={() => handleVerify(detection)}
              onDismiss={() => handleDismiss(detection)}
            />
          {/each}

          {#if data.recentDetections.length === 0}
            <div class="text-center py-8 text-base-content/60">
              {t('dashboard.recentDetections.noDetections')}
            </div>
          {/if}
        </div>
      </div>
    </div>
  {/if}
</div>

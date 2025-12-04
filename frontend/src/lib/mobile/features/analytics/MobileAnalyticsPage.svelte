<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '$lib/i18n';
  import { getLogger } from '$lib/utils/logger';
  import { getLocalDateString } from '$lib/utils/date';
  import MobileStatCard from '../../components/ui/MobileStatCard.svelte';
  import ExpandableChartSection from '../../components/ui/ExpandableChartSection.svelte';
  import SpeciesRow from '../../components/species/SpeciesRow.svelte';

  const logger = getLogger('mobile-analytics');

  interface SpeciesSummary {
    common_name: string;
    scientific_name: string;
    count: number;
    avg_confidence: number;
  }

  interface HourlyData {
    hour: number;
    count: number;
  }

  interface AnalyticsData {
    totalDetections: number;
    uniqueSpecies: number;
    peakHour: string;
    topSpecies: string;
    species: SpeciesSummary[];
    hourlyData: HourlyData[];
  }

  let loading = $state(true);
  let error = $state<string | null>(null);
  let data = $state<AnalyticsData | null>(null);
  let expandedSection = $state<string | null>(null);

  function toggleSection(section: string) {
    expandedSection = expandedSection === section ? null : section;
  }

  function formatPeakHour(hourlyData: HourlyData[]): string {
    if (!hourlyData || hourlyData.length === 0) return '-';
    const peak = hourlyData.reduce((max, curr) => (curr.count > max.count ? curr : max));
    const hour = peak.hour;
    const ampm = hour >= 12 ? 'PM' : 'AM';
    const displayHour = hour % 12 || 12;
    return `${displayHour} ${ampm}`;
  }

  function navigateToSpecies(scientificName: string) {
    window.location.href = `/ui/analytics/species?name=${encodeURIComponent(scientificName)}`;
  }

  async function loadAnalytics() {
    try {
      error = null;
      loading = true;

      const today = getLocalDateString();
      const weekAgo = new Date();
      weekAgo.setDate(weekAgo.getDate() - 7);
      const startDate = getLocalDateString(weekAgo);

      // Fetch species summary
      const speciesResponse = await fetch(
        `/api/v2/analytics/species/summary?start_date=${startDate}&end_date=${today}`
      );
      if (!speciesResponse.ok) {
        throw new Error('Failed to fetch species summary');
      }
      const speciesData: SpeciesSummary[] = await speciesResponse.json();

      // Fetch hourly distribution
      const hourlyResponse = await fetch(
        `/api/v2/analytics/time/distribution/hourly?start_date=${startDate}&end_date=${today}`
      );
      if (!hourlyResponse.ok) {
        throw new Error('Failed to fetch hourly data');
      }
      const hourlyData: HourlyData[] = await hourlyResponse.json();

      // Calculate summary stats
      const totalDetections = speciesData.reduce((sum, s) => sum + s.count, 0);
      const uniqueSpecies = speciesData.length;
      const topSpecies = speciesData.length > 0 ? speciesData[0].common_name : '-';
      const peakHour = formatPeakHour(hourlyData);

      data = {
        totalDetections,
        uniqueSpecies,
        peakHour,
        topSpecies,
        species: speciesData,
        hourlyData,
      };
    } catch (err) {
      logger.error('Failed to load analytics', err);
      error = err instanceof Error ? err.message : 'Failed to load analytics';
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    loadAnalytics();
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
      <button class="btn btn-sm" onclick={loadAnalytics}>Retry</button>
    </div>
  {:else if data}
    <!-- Stats Grid (2x2) -->
    <div class="grid grid-cols-2 gap-3">
      <MobileStatCard
        title={t('analytics.stats.totalDetections')}
        value={data.totalDetections.toLocaleString()}
        subtitle="Last 7 days"
        iconClassName="bg-primary/20 text-primary"
      />
      <MobileStatCard
        title={t('analytics.stats.uniqueSpecies')}
        value={data.uniqueSpecies}
        subtitle="Last 7 days"
        iconClassName="bg-secondary/20 text-secondary"
      />
      <MobileStatCard
        title="Peak Hour"
        value={data.peakHour}
        subtitle="Most active"
        iconClassName="bg-accent/20 text-accent"
      />
      <MobileStatCard
        title={t('analytics.stats.mostCommon')}
        value={data.topSpecies}
        subtitle="Top species"
        iconClassName="bg-success/20 text-success"
      />
    </div>

    <!-- Expandable Charts -->
    <ExpandableChartSection
      title="Detection Trends"
      expanded={expandedSection === 'trends'}
      onToggle={() => toggleSection('trends')}
    >
      <div
        class="h-48 bg-base-200 rounded-lg flex items-center justify-center text-base-content/40"
      >
        Chart placeholder - Daily trend
      </div>
    </ExpandableChartSection>

    <ExpandableChartSection
      title="Time of Day Pattern"
      expanded={expandedSection === 'timeOfDay'}
      onToggle={() => toggleSection('timeOfDay')}
    >
      <div
        class="h-48 bg-base-200 rounded-lg flex items-center justify-center text-base-content/40"
      >
        Chart placeholder - Hourly distribution
      </div>
    </ExpandableChartSection>

    <!-- Species List -->
    <div class="card bg-base-100 shadow-sm">
      <div class="p-4 border-b border-base-200">
        <h2 class="font-medium">Species</h2>
      </div>
      <div class="divide-y divide-base-200">
        {#each data.species.slice(0, 10) as species (species.scientific_name)}
          <SpeciesRow
            species={{
              commonName: species.common_name,
              scientificName: species.scientific_name,
              count: species.count,
              thumbnailUrl: `/api/v2/media/species-image?name=${encodeURIComponent(species.scientific_name)}`,
            }}
            onClick={() => navigateToSpecies(species.scientific_name)}
          />
        {/each}

        {#if data.species.length === 0}
          <div class="text-center py-8 text-base-content/60">
            No species detected in this period
          </div>
        {/if}
      </div>
    </div>
  {/if}
</div>

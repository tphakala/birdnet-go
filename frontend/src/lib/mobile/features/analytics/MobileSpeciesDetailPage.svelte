<script lang="ts">
  import { onMount } from 'svelte';
  import { getLogger } from '$lib/utils/logger';
  import MobileStatCard from '../../components/ui/MobileStatCard.svelte';
  import ExpandableChartSection from '../../components/ui/ExpandableChartSection.svelte';
  import DetectionRow from '../../components/detection/DetectionRow.svelte';

  const logger = getLogger('mobile-species-detail');

  interface Props {
    speciesName?: string;
  }

  let { speciesName }: Props = $props();

  interface SpeciesDetail {
    commonName: string;
    scientificName: string;
    totalDetections: number;
    firstSeen: string;
    lastSeen: string;
    thumbnailUrl: string;
  }

  interface Detection {
    id: number;
    commonName: string;
    scientificName: string;
    confidence: number;
    date: string;
    time: string;
  }

  let loading = $state(true);
  let error = $state<string | null>(null);
  let species = $state<SpeciesDetail | null>(null);
  let recentDetections = $state<Detection[]>([]);
  let expandedSection = $state<string | null>(null);
  let expandedDetectionId = $state<number | null>(null);

  function toggleSection(section: string) {
    expandedSection = expandedSection === section ? null : section;
  }

  function toggleDetection(id: number) {
    expandedDetectionId = expandedDetectionId === id ? null : id;
  }

  function formatDate(dateStr: string): string {
    if (!dateStr) return '-';
    const date = new Date(dateStr);
    return date.toLocaleDateString();
  }

  async function loadSpeciesDetail() {
    if (!speciesName) {
      error = 'No species specified';
      loading = false;
      return;
    }

    try {
      error = null;
      loading = true;

      // Fetch species summary to get counts
      const summaryResponse = await fetch(
        `/api/v2/analytics/species/summary?species=${encodeURIComponent(speciesName)}`
      );

      let speciesData = null;
      if (summaryResponse.ok) {
        const summaryData = await summaryResponse.json();
        if (Array.isArray(summaryData) && summaryData.length > 0) {
          speciesData = summaryData[0];
        }
      }

      // Fetch recent detections for this species
      const detectionsResponse = await fetch(
        `/api/v2/detections/recent?limit=10&species=${encodeURIComponent(speciesName)}`
      );

      let detections: Detection[] = [];
      if (detectionsResponse.ok) {
        detections = await detectionsResponse.json();
      }

      // Build species detail from available data
      const firstDetection = detections.length > 0 ? detections[detections.length - 1] : null;
      const lastDetection = detections.length > 0 ? detections[0] : null;

      species = {
        commonName: speciesData?.common_name ?? speciesName,
        scientificName: speciesData?.scientific_name ?? speciesName,
        totalDetections: speciesData?.count ?? detections.length,
        firstSeen: firstDetection?.date ?? '-',
        lastSeen: lastDetection ? `${lastDetection.date} ${lastDetection.time}` : '-',
        thumbnailUrl: `/api/v2/media/species-image?name=${encodeURIComponent(speciesName)}`,
      };

      recentDetections = detections;
    } catch (err) {
      logger.error('Failed to load species detail', err);
      error = err instanceof Error ? err.message : 'Failed to load species detail';
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    // Get species name from URL if not provided as prop
    if (!speciesName) {
      const params = new URLSearchParams(window.location.search);
      speciesName = params.get('name') ?? undefined;
    }
    loadSpeciesDetail();
  });
</script>

<div class="flex flex-col">
  {#if loading}
    <div class="flex justify-center py-12">
      <span class="loading loading-spinner loading-lg"></span>
    </div>
  {:else if error}
    <div class="p-4">
      <div class="alert alert-error">
        <span>{error}</span>
        <button class="btn btn-sm" onclick={loadSpeciesDetail}>Retry</button>
      </div>
    </div>
  {:else if species}
    <!-- Hero Section -->
    <div class="relative h-48 bg-base-200">
      <img
        src={species.thumbnailUrl}
        alt={species.commonName}
        class="w-full h-full object-cover"
        onerror={e => {
          const target = e.currentTarget as HTMLImageElement;
          target.style.display = 'none';
        }}
      />
      <div class="absolute inset-0 bg-gradient-to-t from-base-100/90 to-transparent"></div>
      <div class="absolute bottom-0 left-0 right-0 p-4">
        <h1 class="text-2xl font-bold">{species.commonName}</h1>
        <p class="text-base-content/70 italic">{species.scientificName}</p>
      </div>
    </div>

    <div class="flex flex-col gap-4 p-4">
      <!-- Stats Grid (2 columns) -->
      <div class="grid grid-cols-2 gap-3">
        <MobileStatCard
          title="Total Detections"
          value={species.totalDetections.toLocaleString()}
          iconClassName="bg-primary/20 text-primary"
        />
        <MobileStatCard
          title="First Seen"
          value={formatDate(species.firstSeen)}
          iconClassName="bg-secondary/20 text-secondary"
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
          Species detection trend chart
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
          Species hourly distribution chart
        </div>
      </ExpandableChartSection>

      <!-- Recent Detections -->
      <div class="card bg-base-100 shadow-sm">
        <div class="p-4 border-b border-base-200">
          <h2 class="font-medium">Recent Detections</h2>
        </div>
        <div class="divide-y divide-base-200">
          {#each recentDetections as detection (detection.id)}
            <DetectionRow
              {detection}
              expanded={expandedDetectionId === detection.id}
              onToggle={() => toggleDetection(detection.id)}
            />
          {/each}

          {#if recentDetections.length === 0}
            <div class="text-center py-8 text-base-content/60">No recent detections</div>
          {/if}
        </div>
      </div>
    </div>
  {/if}
</div>

<!--
  Test wrapper for ReviewCard component
  Provides mock detection data for testing
-->
<script lang="ts">
  import ReviewCard from './ReviewCard.svelte';
  import type { Detection } from '$lib/types/detection.types';

  interface Props {
    detection?: Detection;
    onSaveComplete?: () => void;
  }

  let { detection, onSaveComplete }: Props = $props();

  // Default mock detection if not provided
  const defaultDetection: Detection = {
    id: 1,
    date: '2024-12-30',
    time: '12:00:00',
    source: 'test',
    beginTime: '12:00:00',
    endTime: '12:00:03',
    speciesCode: 'test',
    scientificName: 'Testus birdus',
    commonName: 'Test Bird',
    confidence: 0.95,
    verified: 'unverified',
    locked: false,
    comments: [],
  };

  // Use $derived to properly track detection prop changes
  const actualDetection = $derived(detection ?? defaultDetection);
</script>

<ReviewCard detection={actualDetection} {onSaveComplete} />

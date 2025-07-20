<script lang="ts">
  import type { DetectionsListData } from '$lib/types/detection.types';
  import DetectionsList from './DetectionsList.svelte';

  interface Props {
    data: DetectionsListData | null;
    loading?: boolean;
    error?: string | null;
    onPageChange: (newPage: number) => void;
    onDetailsClick: (id: number) => void;
    onRefresh: () => void;
  }

  let {
    data = null,
    loading = false,
    error = null,
    onPageChange,
    onDetailsClick,
    onRefresh,
  }: Props = $props();
</script>

<section class="card col-span-12 bg-base-100 shadow-sm">
  <div class="card-body grow-0 p-2 sm:p-4 sm:pt-3">
    <DetectionsList {data} {loading} {error} {onPageChange} {onDetailsClick} {onRefresh} />
  </div>
</section>

<script lang="ts">
  import { setContext } from 'svelte';
  import StreamCard, { type StreamStatus } from './StreamCard.svelte';
  import type { StreamHealthResponse } from './StreamManager.svelte';
  import type { StreamConfig } from '$lib/stores/settings';

  interface Props {
    stream: StreamConfig;
    status?: StreamStatus;
    disabled?: boolean;
    index?: number;
    onUpdate: (_stream: StreamConfig) => boolean;
    onDelete: () => void;
  }

  let {
    stream,
    status = 'unknown',
    disabled = false,
    index = 0,
    onUpdate,
    onDelete,
  }: Props = $props();

  const streamHealthState: Record<string, StreamHealthResponse> = {};

  setContext('streamHealth', streamHealthState);
</script>

<StreamCard {stream} {status} {disabled} {index} {onUpdate} {onDelete} />

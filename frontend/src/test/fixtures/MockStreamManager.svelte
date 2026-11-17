<!--
  Test fixture: stand-in for StreamManager that commits the streams it was
  given, with one gain change, through onUpdateStreams when its button is
  clicked, so a page test can assert how the page saves without the real cards.

  @component
-->
<script lang="ts">
  import type { StreamConfig } from '$lib/stores/settings';
  import type { CommitOptions } from '$lib/stores/settingsAutoSave.svelte';

  interface Props {
    streams?: StreamConfig[];
    onUpdateStreams: (_streams: StreamConfig[], _options: CommitOptions) => void;
  }

  let { streams = [], onUpdateStreams }: Props = $props();
</script>

<button
  type="button"
  onclick={() =>
    onUpdateStreams(
      streams.map(stream => ({ ...stream, gain: (stream.gain ?? 0) + 1 })),
      { field: 'rtsp:streams:row:gain', debounceMs: 400 }
    )}
>
  commit-streams
</button>

<!--
  Test fixture: stand-in for StreamTestButton that reports a finished
  connection test with a probed sample rate as soon as it mounts, so a stream
  card can be checked in the state where a probed rate exists.

  @component
-->
<script lang="ts">
  import { onMount } from 'svelte';

  /** Probed rate the fixture reports; differs from the server's default rate. */
  const PROBED_SAMPLE_RATE = 44100;

  interface Props {
    onResult?: (_result: { sampleRate: number; channels: number } | null) => void;
  }

  let { onResult }: Props = $props();

  onMount(() => {
    onResult?.({ sampleRate: PROBED_SAMPLE_RATE, channels: 1 });
  });
</script>

<div data-testid="stream-test-button"></div>

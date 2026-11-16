<!--
  Test fixture: stand-in for StreamTestButton. Its button reports a finished
  connection test of the URL it was given, with a probed sample rate, so a test
  decides when a stream counts as tested.

  @component
-->
<script lang="ts">
  /** Probed rate the fixture reports; differs from the server's default rate. */
  const PROBED_SAMPLE_RATE = 44100;

  interface Props {
    url?: string;
    onResult?: (_result: { sampleRate: number; channels: number } | null) => void;
  }

  let { url = '', onResult }: Props = $props();
</script>

<button
  type="button"
  data-testid="stream-test-button"
  data-url={url}
  onclick={() => onResult?.({ sampleRate: PROBED_SAMPLE_RATE, channels: 1 })}
>
  run-stream-test
</button>

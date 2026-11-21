<!--
  Test fixture: stand-in for StreamTestButton. Its button reports a finished
  connection test of the URL it was given, with a probed sample rate, so a test
  decides when a stream counts as tested. A second button reports the cleared
  result the real button sends when its URL changes. A third pair reports a test
  that finishes after the URL field moved on: the first remembers the URL, the
  second reports the result for the remembered URL, as the real button does.

  @component
-->
<script lang="ts">
  /** Probed rate the fixture reports; differs from the server's default rate. */
  const PROBED_SAMPLE_RATE = 44100;

  interface Props {
    url?: string;
    onResult?: (_result: { sampleRate: number; channels: number } | null, _url: string) => void;
  }

  let { url = '', onResult }: Props = $props();
  let startedUrl = '';
</script>

<button
  type="button"
  data-testid="stream-test-button"
  data-url={url}
  onclick={() => onResult?.({ sampleRate: PROBED_SAMPLE_RATE, channels: 1 }, url.trim())}
>
  run-stream-test
</button>
<button type="button" data-testid="stream-test-clear" onclick={() => onResult?.(null, url.trim())}>
  clear-stream-test
</button>
<button type="button" data-testid="stream-test-start" onclick={() => (startedUrl = url.trim())}>
  start-stream-test
</button>
<button
  type="button"
  data-testid="stream-test-finish"
  onclick={() => onResult?.({ sampleRate: PROBED_SAMPLE_RATE, channels: 1 }, startedUrl)}
>
  finish-stream-test
</button>

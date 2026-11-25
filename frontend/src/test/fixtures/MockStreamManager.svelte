<!--
  Test fixture: stand-in for StreamManager whose buttons send changed copies of
  the streams it was given through onUpdateStreams, so a page test can assert how
  the page writes and saves them without the real cards. It also shows whether it
  is disabled, why, which mount it is and which URLs the page knows as tested,
  and a button reports a test of a new URL through onUrlTested.

  @component
-->
<script module lang="ts">
  // Counts mounts, so a test can tell a remounted manager from a kept one.
  let mounts = 0;
</script>

<script lang="ts">
  import type { StreamConfig } from '$lib/stores/settings';

  interface Props {
    streams?: StreamConfig[];
    savedStreams?: StreamConfig[];
    testedUrls?: ReadonlySet<string>;
    disabled?: boolean;
    disabledReason?: string;
    onUpdateStreams: (_streams: StreamConfig[]) => void;
    onUrlTested?: (_url: string) => void;
  }

  let {
    streams = [],
    savedStreams = [],
    testedUrls = new Set<string>(),
    disabled = false,
    disabledReason = '',
    onUpdateStreams,
    onUrlTested,
  }: Props = $props();
  const mountId = ++mounts;

  const NEW_URL = 'rtsp://new/stream';
  const withGain = (delta: number) =>
    streams.map(stream => ({ ...stream, gain: (stream.gain ?? 0) + delta }));
  const withName = (name: string) =>
    streams.map((stream, index) => (index === 0 ? { ...stream, name } : stream));
  const withUrl = (url: string) =>
    streams.map((stream, index) => (index === 0 ? { ...stream, url } : stream));
</script>

<p
  data-testid="manager-state"
  data-mount={mountId}
  data-disabled={disabled}
  data-saved={savedStreams.length}
  data-tested={[...testedUrls].join(',')}
>
  {disabledReason}
</p>
<button type="button" onclick={() => onUpdateStreams(withGain(1))}>commit-streams</button>
<button type="button" onclick={() => onUpdateStreams(withGain(-1))}>revert-streams</button>
<button type="button" onclick={() => onUpdateStreams(withName(''))}>blank-name</button>
<button type="button" onclick={() => onUpdateStreams(withName('Old 2'))}>rename-stream</button>
<button type="button" onclick={() => onUpdateStreams(withName('Old'))}>rename-back</button>
<button type="button" onclick={() => onUpdateStreams(withUrl(NEW_URL))}>new-url</button>
<button type="button" onclick={() => onUpdateStreams(withUrl('rtsp://old'))}>old-url</button>
<button type="button" onclick={() => onUrlTested?.(NEW_URL)}>test-new-url</button>

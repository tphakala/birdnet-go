<!--
  Test fixture: stand-in for SoundCardManager whose buttons send changed copies
  of the sources it was given through onUpdateSources, so a page test can assert
  how the page writes and saves them without the real cards. It also shows whether
  it is disabled, why, and which mount it is.

  @component
-->
<script module lang="ts">
  // Counts mounts, so a test can tell a remounted manager from a kept one.
  let mounts = 0;
</script>

<script lang="ts">
  import type { AudioSourceConfig } from '$lib/stores/settings';

  interface Props {
    sources?: AudioSourceConfig[];
    disabled?: boolean;
    disabledReason?: string;
    onUpdateSources: (_sources: AudioSourceConfig[]) => void;
  }

  let { sources = [], disabled = false, disabledReason = '', onUpdateSources }: Props = $props();
  const mountId = ++mounts;

  const withGain = (delta: number) =>
    sources.map(source => ({ ...source, gain: source.gain + delta }));
  const withName = (name: string) =>
    sources.map((source, index) => (index === 0 ? { ...source, name } : source));
</script>

<p data-testid="manager-state" data-mount={mountId} data-disabled={disabled}>{disabledReason}</p>
<button type="button" onclick={() => onUpdateSources(withGain(1))}>commit-sources</button>
<button type="button" onclick={() => onUpdateSources(withGain(-1))}>revert-sources</button>
<button type="button" onclick={() => onUpdateSources(withName(''))}>blank-name</button>
<button type="button" onclick={() => onUpdateSources(withName('Garden 2'))}>rename-source</button>
<button type="button" onclick={() => onUpdateSources(withName('Garden'))}>rename-back</button>

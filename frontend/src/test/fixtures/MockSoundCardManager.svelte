<!--
  Test fixture: stand-in for SoundCardManager whose buttons send changed copies
  of the sources it was given through onUpdateSources, so a page test can assert
  how the page writes and saves them without the real cards.

  @component
-->
<script lang="ts">
  import type { AudioSourceConfig } from '$lib/stores/settings';

  interface Props {
    sources?: AudioSourceConfig[];
    onUpdateSources: (_sources: AudioSourceConfig[]) => void;
  }

  let { sources = [], onUpdateSources }: Props = $props();

  const withGain = (delta: number) =>
    sources.map(source => ({ ...source, gain: source.gain + delta }));
  const withName = (name: string) =>
    sources.map((source, index) => (index === 0 ? { ...source, name } : source));
</script>

<button type="button" onclick={() => onUpdateSources(withGain(1))}>commit-sources</button>
<button type="button" onclick={() => onUpdateSources(withGain(-1))}>revert-sources</button>
<button type="button" onclick={() => onUpdateSources(withName(''))}>blank-name</button>
<button type="button" onclick={() => onUpdateSources(withName('Garden 2'))}>rename-source</button>
<button type="button" onclick={() => onUpdateSources(withName('Garden'))}>rename-back</button>

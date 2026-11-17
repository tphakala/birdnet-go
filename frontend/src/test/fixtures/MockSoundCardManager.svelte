<!--
  Test fixture: stand-in for SoundCardManager that commits the sources it was
  given, with one gain change, through onUpdateSources when its button is
  clicked, so a page test can assert how the page saves without the real cards.

  @component
-->
<script lang="ts">
  import type { AudioSourceConfig } from '$lib/stores/settings';
  import type { CommitOptions } from '$lib/stores/settingsAutoSave.svelte';

  interface Props {
    sources?: AudioSourceConfig[];
    onUpdateSources: (_sources: AudioSourceConfig[], _options: CommitOptions) => void;
  }

  let { sources = [], onUpdateSources }: Props = $props();
</script>

<button
  type="button"
  onclick={() =>
    onUpdateSources(
      sources.map(source => ({ ...source, gain: source.gain + 1 })),
      { field: 'audio:sources:row:gain', debounceMs: 400 }
    )}
>
  commit-sources
</button>

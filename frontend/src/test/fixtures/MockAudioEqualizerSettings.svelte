<!--
  Test fixture: stand-in for AudioEqualizerSettings that renders the sample
  rate it receives, so a form's preview wiring can be asserted without the
  equalizer editor. Other props are ignored.

  @component
-->
<script lang="ts">
  interface Props {
    sampleRate?: number;
  }

  let { sampleRate }: Props = $props();
</script>

<div data-testid="audio-equalizer-settings">{sampleRate ?? 'none'}</div>

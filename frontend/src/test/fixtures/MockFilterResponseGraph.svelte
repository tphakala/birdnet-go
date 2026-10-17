<!--
  Test fixture: stand-in for FilterResponseGraph that renders the props it
  receives as plain text, so a parent's wiring can be asserted without a
  canvas or a network request.

  @component
-->
<script lang="ts">
  interface Props {
    equalizer: { enabled: boolean; filters: unknown[] };
    sampleRate?: number;
  }

  let { equalizer, sampleRate }: Props = $props();
</script>

<div data-testid="filter-response-graph">
  enabled={String(equalizer.enabled)};filters={equalizer.filters.length};sampleRate={sampleRate ??
    'none'}
</div>

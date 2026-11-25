<!--
  Test fixture: stand-in for ModelToggleList that renders the current
  selection as plain text so a parent's default model pre-selection can be
  asserted without driving the real switches. Other props are ignored.

  @component
-->
<script lang="ts">
  interface Props {
    selectedModels?: string[];
  }

  let { selectedModels = [] }: Props = $props();
</script>

<div data-testid="model-toggle-list">{selectedModels.join(',')}</div>

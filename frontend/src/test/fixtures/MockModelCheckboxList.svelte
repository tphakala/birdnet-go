<!--
  Test fixture: stand-in for ModelCheckboxList that renders the current
  selection as plain text so a parent's default model pre-selection can be
  asserted without driving the real checkbox UI. Other props are ignored.

  @component
-->
<script lang="ts">
  interface Props {
    selectedModels?: string[];
  }

  let { selectedModels = [] }: Props = $props();
</script>

<div data-testid="model-checkbox-list">{selectedModels.join(',')}</div>

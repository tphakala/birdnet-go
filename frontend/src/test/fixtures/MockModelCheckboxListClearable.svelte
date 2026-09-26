<!--
  Test fixture: stand-in for ModelCheckboxList that renders the current
  selection as plain text (like MockModelCheckboxList) and additionally exposes
  a "clear" button that fires onToggle([]). This lets a parent-flow test both
  assert the selection and drive the "user cleared every model" case without the
  real checkbox UI. Other props are ignored.

  @component
-->
<script lang="ts">
  let {
    selectedModels = [],
    onToggle,
  }: {
    selectedModels?: string[];
    onToggle?: (_models: string[]) => void;
    [key: string]: unknown;
  } = $props();
</script>

<div data-testid="model-checkbox-list">{selectedModels.join(',')}</div>
<button type="button" data-testid="clear-models" onclick={() => onToggle?.([])}>clear</button>

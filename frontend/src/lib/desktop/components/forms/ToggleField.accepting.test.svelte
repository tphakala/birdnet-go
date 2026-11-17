<!-- Test host: a parent that accepts every change at once, inside onUpdate. -->
<script lang="ts">
  import ToggleField from './ToggleField.svelte';

  let value = $state(false);
</script>

<ToggleField label="Keep" {value} onUpdate={next => (value = next)} />
<p data-testid="parent-value">{value}</p>

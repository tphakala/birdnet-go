<script lang="ts">
  import { onMount } from 'svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import type { WizardStepProps } from './types';

  let { onValidChange, registerLeaveHandler }: WizardStepProps = $props();

  const options = [
    { value: 'one', label: 'Option one' },
    { value: 'two', label: 'Option two' },
  ];

  onMount(() => {
    onValidChange?.(true, undefined);
    return registerLeaveHandler?.(() => Promise.resolve());
  });
</script>

<label for="fixture-dropdown">Fixture dropdown</label>
<SelectDropdown id="fixture-dropdown" {options} value="one" searchable={true} />

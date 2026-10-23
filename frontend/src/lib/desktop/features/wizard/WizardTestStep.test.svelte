<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import type { WizardStepProps } from './types';
  import { stepControl } from './wizardTestStepControl';

  let { onValidChange, registerLeaveHandler }: WizardStepProps = $props();

  let valid = $state(stepControl.validQueue.shift() ?? true);
  const reason = stepControl.reasonQueue.shift();

  $effect(() => {
    const current = valid;
    untrack(() => onValidChange?.(current, reason));
  });

  onMount(() => registerLeaveHandler?.(() => stepControl.leave()));
</script>

<label>
  <input type="checkbox" bind:checked={valid} />
  Fixture step is valid
</label>

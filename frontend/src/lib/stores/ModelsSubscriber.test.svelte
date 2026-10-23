<!--
  Test harness for the models store. Mirrors how SoundCardManager and
  StreamManager use it: subscribe from an $effect and read the list through
  a $derived, so the store is exercised inside a real effect.
-->
<script lang="ts">
  import { fetchModels, getAvailableModels, modelsLoading } from './models.svelte';

  interface Props {
    onEffectRun?: () => void;
  }

  let { onEffectRun }: Props = $props();

  const availableModels = $derived(getAvailableModels());

  $effect(() => {
    onEffectRun?.();
    return fetchModels();
  });
</script>

<span data-testid="loading">{modelsLoading()}</span>
<span data-testid="count">{availableModels.length}</span>

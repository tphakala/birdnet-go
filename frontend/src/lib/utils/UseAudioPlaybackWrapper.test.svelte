<!--
  Thin test wrapper for useAudioPlayback composable.
  Calls the composable in a component context (required for onMount)
  and exposes the returned state object via a callback prop.
-->
<script lang="ts">
  import {
    useAudioPlayback,
    type AudioPlaybackOptions,
    type AudioPlaybackState,
  } from './useAudioPlayback.svelte';

  interface Props {
    options: AudioPlaybackOptions;
    onState?: (_state: AudioPlaybackState) => void;
  }

  let { options, onState }: Props = $props();

  // Pass options directly — the composable reads initial values
  // and handles subsequent changes via setAudioUrl().
  // svelte-ignore state_referenced_locally
  const state = useAudioPlayback(options);

  // Expose state to test harness via callback
  $effect(() => {
    onState?.(state);
  });
</script>

<div data-testid="wrapper">
  <span data-testid="isPlaying">{state.isPlaying}</span>
  <span data-testid="isLoading">{state.isLoading}</span>
  <span data-testid="progress">{state.progress}</span>
  <span data-testid="duration">{state.duration}</span>
  <span data-testid="currentTime">{state.currentTime}</span>
  <span data-testid="error">{state.error ?? ''}</span>
</div>

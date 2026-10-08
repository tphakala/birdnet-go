<script lang="ts">
  import Button from './Button.svelte';
  import type { ComponentProps } from 'svelte';
  import type { HTMLButtonAttributes } from 'svelte/elements';

  // Test harness: binds Button's ref and reports it through `holder`
  interface Props extends Omit<HTMLButtonAttributes, 'class'> {
    class?: string;
    variant?: ComponentProps<typeof Button>['variant'];
    holder?: { el?: HTMLButtonElement | null };
  }

  let { holder, ...rest }: Props = $props();
  let ref = $state<HTMLButtonElement>();

  $effect(() => {
    if (holder) holder.el = ref;
  });
</script>

<Button bind:ref {...rest}>
  {#snippet children()}
    Press me
  {/snippet}
</Button>

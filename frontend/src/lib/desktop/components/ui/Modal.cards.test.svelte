<script lang="ts">
  import Modal from './Modal.svelte';
  import RadioCardGroup from './RadioCardGroup.svelte';

  // Test host: a roving-tabindex card group between the close button and the
  // footer, as in the setup wizard steps.
  let value = $state<string | null>('a');
</script>

<Modal isOpen={true} title="Cards Modal">
  {#snippet children()}
    <RadioCardGroup
      aria-label="Choice"
      options={[
        { value: 'a', label: 'Card A' },
        { value: 'b', label: 'Card B' },
      ]}
      {value}
      onChange={next => (value = next)}
    />
  {/snippet}

  {#snippet footer()}
    <button type="button">Next</button>
  {/snippet}
</Modal>

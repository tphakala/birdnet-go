<script lang="ts">
  import Modal from './Modal.svelte';
  import SelectDropdown from '../forms/SelectDropdown.svelte';

  // Test host: a searchable dropdown between two buttons in a Modal, with the
  // option to put it last so Tab from it reaches the end of the dialog.
  let { dropdownLast = false }: { dropdownLast?: boolean } = $props();

  const options = [
    { value: 'a', label: 'Option A' },
    { value: 'b', label: 'Option B' },
  ];
</script>

<Modal isOpen={true} title="Select Modal" showCloseButton={false}>
  {#snippet children()}
    <button type="button">Before</button>
    <SelectDropdown {options} value="a" searchable={true} label="Choice" />
    {#if !dropdownLast}
      <button type="button">After</button>
    {/if}
  {/snippet}
</Modal>

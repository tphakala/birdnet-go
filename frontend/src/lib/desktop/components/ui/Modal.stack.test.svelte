<script lang="ts">
  import Modal from './Modal.svelte';

  // Test host for two Modals open at once, as the setup wizard and its leave
  // confirmation are. The second one opens later than the first.
  let {
    firstOpen = true,
    secondOpen = false,
    onCloseFirst = undefined,
    onCloseSecond = undefined,
    describeBody = false,
  }: {
    firstOpen?: boolean;
    secondOpen?: boolean;
    onCloseFirst?: () => void;
    onCloseSecond?: () => void;
    describeBody?: boolean;
  } = $props();
</script>

<Modal
  isOpen={firstOpen}
  title="Lower modal"
  showCloseButton={false}
  onClose={onCloseFirst}
  {describeBody}
>
  {#snippet children()}
    <button type="button">Lower action</button>
  {/snippet}
</Modal>

<Modal
  isOpen={secondOpen}
  title="Upper modal"
  showCloseButton={false}
  onClose={onCloseSecond}
  {describeBody}
>
  {#snippet children()}
    <button type="button">Upper action</button>
  {/snippet}
</Modal>

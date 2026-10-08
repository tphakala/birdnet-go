<script lang="ts">
  import Modal from './Modal.svelte';

  // Test host for the focus trap: a controllable set of buttons between two
  // buttons that stay outside the dialog.
  let {
    isOpen = true,
    firstDisabled = false,
    lastDisabled = false,
    lastTabindex = undefined,
    showExtra = false,
    showCard = false,
    showTrailingCard = false,
    showRadios = false,
    escapeHandled = false,
    onClose = undefined,
  }: {
    isOpen?: boolean;
    firstDisabled?: boolean;
    lastDisabled?: boolean;
    lastTabindex?: number;
    showExtra?: boolean;
    showCard?: boolean;
    showTrailingCard?: boolean;
    showRadios?: boolean;
    escapeHandled?: boolean;
    onClose?: () => void;
  } = $props();
</script>

<button type="button">Outside before</button>

<Modal {isOpen} title="Trap Modal" showCloseButton={false} {onClose}>
  {#snippet children()}
    {#if showRadios}
      <input type="radio" name="mode" value="a" aria-label="Mode A" />
      <input type="radio" name="mode" value="b" aria-label="Mode B" checked />
    {/if}
    {#if escapeHandled}
      <!-- Stands in for a control that uses Escape itself, such as an open list -->
      <input
        aria-label="Handles Escape"
        onkeydown={event => event.key === 'Escape' && event.preventDefault()}
      />
    {/if}
    <button type="button" disabled={firstDisabled}>First</button>
    {#if showCard}
      <div tabindex="-1" role="group" aria-label="Card">Card</div>
    {/if}
  {/snippet}

  {#snippet footer()}
    <button type="button">Middle</button>
    <button type="button" disabled={lastDisabled} tabindex={lastTabindex}>Last</button>
    {#if showExtra}
      <button type="button">Extra</button>
    {/if}
    {#if showTrailingCard}
      <div tabindex="-1" role="group" aria-label="Trailing card">Trailing card</div>
    {/if}
  {/snippet}
</Modal>

<button type="button">Outside after</button>

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
    radioLayout = 'none',
    hideFooter = false,
    visibilityHiddenLast = false,
    skippedLast = 'none',
    dialogHidden = false,
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
    /** split: a checked radio, a button, then the group's second radio, nothing else in the body */
    radioLayout?: 'none' | 'split' | 'trailing-unchecked' | 'leading-unchecked';
    hideFooter?: boolean;
    /** a last control that a test's checkVisibility stub reports hidden (data-vis-hidden) */
    visibilityHiddenLast?: boolean;
    /** a last control the trap must skip: a hidden attribute, an inert subtree or a hidden input */
    skippedLast?: 'none' | 'hidden-attribute' | 'inert' | 'hidden-input';
    /** marks the dialog itself as hidden for the same stub, as in its first open frame */
    dialogHidden?: boolean;
    onClose?: () => void;
  } = $props();
</script>

<button type="button">Outside before</button>

<Modal
  {isOpen}
  title="Trap Modal"
  showCloseButton={false}
  {onClose}
  data-vis-hidden={dialogHidden ? '' : undefined}
>
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
    {#if radioLayout === 'split'}
      <input type="radio" name="split" value="a" aria-label="Split A" checked />
      <button type="button">Between</button>
      <input type="radio" name="split" value="b" aria-label="Split B" />
    {:else}
      {#if radioLayout === 'leading-unchecked'}
        <input type="radio" name="pick" value="p" aria-label="Pick P" />
        <input type="radio" name="pick" value="q" aria-label="Pick Q" />
      {/if}
      <button type="button" disabled={firstDisabled}>First</button>
      {#if showCard}
        <div tabindex="-1" role="group" aria-label="Card">Card</div>
      {/if}
      {#if radioLayout === 'trailing-unchecked'}
        <input type="radio" name="size" value="x" aria-label="Size X" />
        <input type="radio" name="size" value="y" aria-label="Size Y" />
      {/if}
    {/if}
  {/snippet}

  {#snippet footer()}
    {#if !hideFooter}
      <button type="button">Middle</button>
      <button type="button" disabled={lastDisabled} tabindex={lastTabindex}>Last</button>
      {#if showExtra}
        <button type="button">Extra</button>
      {/if}
      {#if skippedLast === 'hidden-attribute'}
        <button type="button" hidden>Hidden attribute</button>
      {:else if skippedLast === 'inert'}
        <div inert><button type="button">Inert button</button></div>
      {:else if skippedLast === 'hidden-input'}
        <input type="hidden" value="x" />
      {/if}
      {#if visibilityHiddenLast}
        <button type="button" data-vis-hidden>Hidden last</button>
      {/if}
      {#if showTrailingCard}
        <div tabindex="-1" role="group" aria-label="Trailing card">Trailing card</div>
      {/if}
    {/if}
  {/snippet}
</Modal>

<button type="button">Outside after</button>

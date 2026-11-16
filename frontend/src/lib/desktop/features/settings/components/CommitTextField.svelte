<!--
  CommitTextField

  Purpose: A text setting on an immediately saved tab. Typing edits a local
  draft; the draft is committed on blur or Enter, Escape restores the saved
  value. A value the validator refuses is not committed and the reason shows
  at the field (aria-invalid, aria-describedby); editing clears it. The draft
  follows the saved value while the field is not focused.

  Props:
  - id: Id of the input, for its label
  - value: The saved value
  - validate: Returns the reason a trimmed value cannot be saved, or null
  - onCommit: Receives a trimmed, valid value that differs from the saved one
  - placeholder, maxlength, required, disabled (read-only while disabled)
  - describedBy: Id of an outside description (for example the save status)

  @component
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import { cn } from '$lib/utils/cn';

  interface Props {
    id: string;
    value: string;
    validate?: (_value: string) => string | null;
    onCommit: (_value: string) => void;
    placeholder?: string;
    maxlength?: number;
    required?: boolean;
    disabled?: boolean;
    describedBy?: string;
  }

  let {
    id,
    value,
    validate,
    onCommit,
    placeholder,
    maxlength,
    required = false,
    disabled = false,
    describedBy,
  }: Props = $props();

  const errorId = $derived(`${id}-error`);

  let draft = $state(untrack(() => value));
  let focused = $state(false);
  let error = $state<string | null>(null);

  $effect(() => {
    const saved = value;
    untrack(() => {
      if (!focused) draft = saved;
    });
  });

  function commit() {
    const trimmed = draft.trim();
    if (trimmed === value) {
      draft = value;
      error = null;
      return;
    }
    const reason = validate?.(trimmed) ?? null;
    error = reason;
    if (reason) return;
    draft = trimmed;
    onCommit(trimmed);
  }

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Enter') {
      event.preventDefault();
      commit();
    } else if (event.key === 'Escape') {
      event.preventDefault();
      draft = value;
      error = null;
    }
  }

  let ariaDescribedBy = $derived(
    [error ? errorId : undefined, describedBy].filter(Boolean).join(' ') || undefined
  );
</script>

<input
  {id}
  type="text"
  class={cn('input input-sm w-full', error && 'input-error')}
  bind:value={draft}
  {placeholder}
  {maxlength}
  {required}
  aria-required={required ? 'true' : undefined}
  aria-invalid={error ? 'true' : undefined}
  aria-describedby={ariaDescribedBy}
  readonly={disabled}
  onfocus={() => (focused = true)}
  onblur={() => {
    focused = false;
    commit();
  }}
  oninput={() => (error = null)}
  onkeydown={handleKeydown}
/>
{#if error}
  <p id={errorId} class="text-xs text-[var(--text-error)]">{error}</p>
{/if}

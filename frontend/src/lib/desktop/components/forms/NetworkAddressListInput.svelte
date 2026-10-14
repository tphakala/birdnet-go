<!--
  NetworkAddressListInput

  Edits a list of IP addresses and/or CIDR ranges as rows. A controlled component:
  `values` comes in, every change goes out through `onUpdate`, and nothing is read from
  or written to a store.

  - Add with the Add button or Enter. The text may hold several values separated by
    commas, spaces or line breaks (a pasted multi-line list becomes comma-separated
    text). Valid new values are added in order; values already in the list (compared by
    normalized network, so 10.0.0.1 and 10.0.0.1/32 match) are dropped and reported;
    invalid values and values over `maxItems` stay in the input and are reported.
  - Values passed in are never changed or dropped by rendering. Invalid or repeated
    rows are flagged with an icon and text until the user removes them. Rows are keyed
    by index, so repeated values render.
  - An add's report shows until the text is edited, a row is removed, or the parent changes
    `reportResetKey` (a form Reset).
  - The text typed but not added yet is the bindable `draft`. Left unbound it lives in
    the component; bind it to keep it when the component unmounts (a tab switch) and to
    block saving while it is not empty, instead of losing it silently.

  Validation mirrors Go's net.ParseIP and net.ParseCIDR (see $lib/utils/networkAddress).

  @component
-->
<script lang="ts">
  import { tick, type Snippet } from 'svelte';
  import type { ClipboardEventHandler } from 'svelte/elements';
  import { Plus, X, TriangleAlert } from '@lucide/svelte';
  import { cn } from '$lib/utils/cn';
  import { generateId } from '$lib/utils/uuid';
  import { t } from '$lib/i18n';
  import {
    networkAddressKey,
    isValidNetworkAddress,
    type NetworkAddressMode,
  } from '$lib/utils/networkAddress';

  /** What the rowAnnotation snippet receives for each row. */
  export interface NetworkAddressRow {
    value: string;
    index: number;
    /** Whether the value is acceptable for the field's mode (duplicates count as valid). */
    valid: boolean;
  }

  interface Props {
    /** The current list, in order. */
    values: string[];
    /** Called with the whole new list after an add or a remove. */
    onUpdate: (_values: string[]) => void;
    /** Which kinds of address the field accepts. */
    accept: NetworkAddressMode;
    /** Visible label of the input. */
    label: string;
    /** id of the input; generated when omitted. */
    id?: string;
    /** Help text shown under the input and linked to it. */
    helpText?: string;
    placeholder?: string;
    /** Shown instead of the list when it is empty. */
    emptyStateMessage?: string;
    /** Disables the input, the Add button and every remove button. */
    disabled?: boolean;
    /** Largest number of rows the field accepts; unlimited when omitted. */
    maxItems?: number;
    /** Extra content after a row's value, such as a badge. */
    rowAnnotation?: Snippet<[NetworkAddressRow]>;
    /** Text typed but not added yet. Bindable; internal state when not bound. */
    draft?: string;
    /** Changing this value hides the add report; a parent bumps it when the form is reset. */
    reportResetKey?: number;
    className?: string;
  }

  let {
    values,
    onUpdate,
    accept,
    label,
    id,
    helpText,
    placeholder,
    emptyStateMessage,
    disabled = false,
    maxItems,
    rowAnnotation,
    draft = $bindable(''),
    reportResetKey,
    className,
  }: Props = $props();

  /** Splits typed or pasted text into candidate values. */
  const TOKEN_SEPARATORS = /[\s,]+/;
  const LINE_BREAKS = /\r\n|\r|\n/;
  const PASTE_SEPARATOR = ', ';
  const MESSAGE_SEPARATOR = ', ';

  /** i18n key segment for each mode (keys are camelCase). */
  const MODE_KEYS = {
    cidr: 'cidr',
    ip: 'ip',
    'ip-or-cidr': 'ipOrCidr',
  } as const satisfies Record<NetworkAddressMode, string>;

  /** The report of the last add, with the parent's `reportResetKey` at that time. */
  interface AddReport {
    messages: string[];
    hasInvalid: boolean;
    resetKey: number | undefined;
  }
  const EMPTY_REPORT: AddReport = { messages: [], hasInvalid: false, resetKey: undefined };

  const baseId = generateId('network-address-list');
  let inputId = $derived(id ?? `${baseId}-input`);
  const helpId = `${baseId}-help`;
  const messageId = `${baseId}-message`;
  const pendingId = `${baseId}-pending`;
  const limitId = `${baseId}-limit`;

  let inputElement = $state<HTMLInputElement>();
  let listElement = $state<HTMLUListElement>();
  /** Report of the last add; cleared on edit and row removal, hidden once reportResetKey changes. */
  let report = $state<AddReport>(EMPTY_REPORT);
  let reportApplies = $derived(report.resetKey === reportResetKey);
  let messages = $derived(reportApplies ? report.messages : []);
  let hasInvalidTokens = $derived(reportApplies && report.hasInvalid);

  let modeKey = $derived(MODE_KEYS[accept]);
  let hasPending = $derived(draft.trim() !== '');
  let showPendingHint = $derived(hasPending && messages.length === 0);
  let atLimit = $derived(maxItems !== undefined && values.length >= maxItems);
  let canAdd = $derived(!disabled && hasPending && !atLimit);

  // Per-row flags: invalid for the mode, or the same network as an earlier row.
  let rows = $derived.by(() => {
    const seen = new Set<string>();
    return values.map((value, index) => {
      const valid = isValidNetworkAddress(value, accept);
      const key = valid ? networkAddressKey(value) : null;
      const duplicate = key !== null && seen.has(key);
      if (key !== null) seen.add(key);
      return { value, index, valid, duplicate };
    });
  });

  let describedBy = $derived(
    [
      messages.length > 0 ? messageId : undefined,
      showPendingHint ? pendingId : undefined,
      atLimit ? limitId : undefined,
      helpText ? helpId : undefined,
    ]
      .filter(Boolean)
      .join(' ') || undefined
  );

  function setText(next: string) {
    draft = next;
    report = EMPTY_REPORT;
  }

  function addFromInput() {
    if (!canAdd) return;

    const tokens = draft.split(TOKEN_SEPARATORS).filter(token => token !== '');
    // Only rows valid for the mode count, like the rows derivation: an invalid row must
    // not block adding its valid form.
    const keys = new Set(
      values
        .filter(value => isValidNetworkAddress(value, accept))
        .map(value => networkAddressKey(value))
        .filter((key): key is string => key !== null)
    );
    const added: string[] = [];
    const invalid: string[] = [];
    const duplicates: string[] = [];
    const overLimit: string[] = [];

    for (const token of tokens) {
      const key = isValidNetworkAddress(token, accept) ? networkAddressKey(token) : null;
      if (key === null) {
        invalid.push(token);
      } else if (keys.has(key)) {
        duplicates.push(token);
      } else if (maxItems !== undefined && values.length + added.length >= maxItems) {
        overLimit.push(token);
      } else {
        added.push(token);
        keys.add(key);
      }
    }

    if (added.length > 0) onUpdate([...values, ...added]);

    const lines: string[] = [];
    if (invalid.length > 0) {
      lines.push(
        t(`forms.networkAddressList.errors.invalid.${modeKey}`, {
          values: invalid.join(MESSAGE_SEPARATOR),
        })
      );
    }
    if (duplicates.length > 0) {
      lines.push(
        t('forms.networkAddressList.errors.duplicate', {
          values: duplicates.join(MESSAGE_SEPARATOR),
        })
      );
    }
    if (overLimit.length > 0) {
      lines.push(
        t('forms.networkAddressList.errors.limit', {
          max: maxItems ?? 0,
          values: overLimit.join(MESSAGE_SEPARATOR),
        })
      );
    }

    draft = [...invalid, ...overLimit].join(PASTE_SEPARATOR);
    report = { messages: lines, hasInvalid: invalid.length > 0, resetKey: reportResetKey };
  }

  // The Add button turns disabled once the input is empty, which would drop focus to
  // the page; keep the user in the input instead.
  function addWithButton() {
    addFromInput();
    inputElement?.focus();
  }

  // Removing a row removes its focused button. Move focus to the row that takes its
  // place, the new last row, or the input when the list is empty.
  async function removeAt(index: number) {
    if (disabled) return;
    // The report described the list as it was; a removal ends it.
    report = EMPTY_REPORT;
    onUpdate(values.filter((_, i) => i !== index));
    await tick();
    const buttons = listElement?.querySelectorAll<HTMLButtonElement>('[data-remove]') ?? [];
    const next = buttons.length > 0 ? buttons[Math.min(index, buttons.length - 1)] : undefined;
    (next ?? inputElement)?.focus();
  }

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Enter') {
      event.preventDefault();
      addFromInput();
    }
  }

  // A text input drops line breaks from pasted text, which would glue the values of a
  // multi-line paste together. Turn the breaks into commas and insert the text ourselves.
  const handlePaste: ClipboardEventHandler<HTMLInputElement> = async event => {
    const pasted = event.clipboardData?.getData('text') ?? '';
    const lines = pasted.split(LINE_BREAKS);
    if (lines.length < 2) return;
    event.preventDefault();

    const replacement = lines
      .map(line => line.trim())
      .filter(line => line !== '')
      .join(PASTE_SEPARATOR);
    const field = event.currentTarget;
    const start = field.selectionStart ?? draft.length;
    const end = field.selectionEnd ?? draft.length;
    setText(draft.slice(0, start) + replacement + draft.slice(end));

    await tick();
    const caret = start + replacement.length;
    field.setSelectionRange(caret, caret);
  };
</script>

<div class={cn('form-control min-w-0', className)}>
  <label class="label justify-start" for={inputId}>
    <span class="label-text">{label}</span>
  </label>

  <div class="flex gap-2">
    <input
      bind:this={inputElement}
      id={inputId}
      type="text"
      class={cn(
        'input input-sm flex-1 min-w-0 font-mono placeholder:font-sans',
        hasInvalidTokens && 'input-error'
      )}
      value={draft}
      {placeholder}
      {disabled}
      autocomplete="off"
      spellcheck="false"
      aria-describedby={describedBy}
      aria-invalid={hasInvalidTokens ? 'true' : undefined}
      oninput={event => setText(event.currentTarget.value)}
      onkeydown={handleKeydown}
      onpaste={handlePaste}
    />
    <button
      type="button"
      class="btn btn-primary btn-sm gap-1"
      disabled={!canAdd}
      onclick={addWithButton}
    >
      <Plus class="size-4" aria-hidden="true" />
      {t('forms.networkAddressList.add')}
    </button>
  </div>

  <!-- Always rendered so a report that appears is announced; only its text changes -->
  <div id={messageId} role="alert" class="text-xs leading-4 text-[var(--text-error)]">
    {#each messages as line, index (index)}
      <p class="mt-1">{line}</p>
    {/each}
  </div>

  {#if showPendingHint}
    <p id={pendingId} class="mt-1 text-xs text-muted">
      {t('forms.networkAddressList.pendingHint')}
    </p>
  {/if}

  {#if atLimit && maxItems !== undefined}
    <p id={limitId} class="mt-1 text-xs text-muted">
      {t('forms.networkAddressList.limitReached', { max: maxItems })}
    </p>
  {/if}

  {#if helpText}
    <span id={helpId} class="help-text mt-1">{helpText}</span>
  {/if}

  {#if rows.length > 0}
    <ul
      bind:this={listElement}
      class="mt-3 rounded-lg border border-[var(--border-100)] divide-y divide-[var(--border-100)]"
      aria-label={label}
    >
      {#each rows as row (row.index)}
        <li class="flex items-center gap-2 px-3 py-1.5 min-h-10">
          <span class="font-mono text-sm break-all min-w-0">{row.value}</span>
          {#if rowAnnotation}
            {@render rowAnnotation(row)}
          {/if}
          {#if !row.valid}
            <span class="inline-flex items-center gap-1 text-xs text-[var(--text-error)]">
              <TriangleAlert class="size-3.5 shrink-0" aria-hidden="true" />
              {t(`forms.networkAddressList.rowInvalid.${modeKey}`)}
            </span>
          {:else if row.duplicate}
            <span class="inline-flex items-center gap-1 text-xs text-muted">
              <TriangleAlert
                class="size-3.5 shrink-0 text-[var(--color-warning)]"
                aria-hidden="true"
              />
              {t('forms.networkAddressList.rowDuplicate')}
            </span>
          {/if}
          <button
            type="button"
            class="ml-auto inline-flex items-center justify-center p-1.5 rounded-md text-muted hover:text-[var(--text-error)] hover:bg-[var(--color-error)]/10 transition-colors disabled:opacity-50 disabled:cursor-not-allowed focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-[var(--color-primary)]"
            {disabled}
            aria-label={t('forms.networkAddressList.remove', { address: row.value })}
            data-remove
            onclick={() => removeAt(row.index)}
          >
            <X class="size-4" aria-hidden="true" />
          </button>
        </li>
      {/each}
    </ul>
  {:else}
    <p
      class="mt-3 rounded-lg border border-dashed border-[var(--border-200)] px-3 py-3 text-center text-sm text-muted"
    >
      {emptyStateMessage ?? t('forms.networkAddressList.empty')}
    </p>
  {/if}
</div>

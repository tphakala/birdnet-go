<!--
  Equalizer Number Input

  Purpose: One numeric parameter of an equalizer filter (frequency, Q, width or gain)
  in the equalizer editor: a labelled number input that commits on change (blur or
  Enter), never per keystroke, and says when it changed the entry.

  Behaviour:
  - On change the entry is resolved against [min, max]: an entry outside the range is
    clamped to the nearest bound, an entry that is not a number keeps the stored value.
    The input then shows the resolved value, and onCommit receives it unless the entry
    was not a number.
  - A notice under the input says why the shown value differs from the entry. It is
    linked with aria-describedby while it shows, and clears on the next edit or when
    the stored value changes from outside (a type switch, a removed row above).

  Props:
  - label: Translated parameter name
  - unit: Unit symbol shown after the label (optional)
  - value: Stored value
  - min, max, step: Input range and step
  - disabled: Disables the input
  - variant: 'edit' for an existing filter row, 'new' for the new-filter row (label style)
  - onCommit: Called with the committed value

  @component
-->
<script module lang="ts">
  /**
   * Layout of one parameter cell in an equalizer row (label, control, notice). From the
   * md breakpoint the cell spans three rows of the row grid as a subgrid, so the controls
   * of a row share one track and a notice grows the row without moving any control.
   * Labels and controls sit at the bottom of their tracks, as the rows' items-end did.
   */
  export const EQ_PARAMETER_CELL_CLASS =
    'flex flex-col md:row-span-3 md:grid md:grid-rows-subgrid md:gap-y-0 md:items-end';

  /** Row an equalizer field sits in: an existing filter, or the new-filter form. */
  export type EqRowVariant = 'edit' | 'new';

  /** Label classes per row variant: secondary text in filter rows, primary in the new row. */
  export function eqLabelClasses(variant: EqRowVariant): { label: string; text: string } {
    return variant === 'edit'
      ? { label: 'label pt-0', text: 'label-text-alt' }
      : { label: 'label', text: 'label-text' };
  }
</script>

<script lang="ts">
  import { t } from '$lib/i18n';
  import { generateId } from '$lib/utils/uuid';
  import { resolveNumericEntry, type NumericEntryNotice } from '$lib/utils/audio/equalizer';

  interface Props {
    label: string;
    unit?: string;
    value: number;
    min: number;
    max: number;
    step: number;
    disabled?: boolean;
    variant: EqRowVariant;
    onCommit: (_value: number) => void;
  }

  let { label, unit, value, min, max, step, disabled = false, variant, onCommit }: Props = $props();

  const inputId = generateId('eq-param');
  const noticeId = `${inputId}-notice`;

  let notice = $state<{ kind: NumericEntryNotice; value: number } | null>(null);

  // The value this input last committed. A plain variable, not state: the effect below
  // must rerun only when the value prop changes, not when a commit records its value.
  let lastCommitted: number | undefined;

  // A value that arrives from outside (not the one this input just committed) makes
  // any notice stale.
  $effect(() => {
    if (value !== lastCommitted) notice = null;
  });

  const noticeText = $derived.by(() => {
    if (!notice) return '';
    switch (notice.kind) {
      case 'min':
        return t('components.forms.numberField.adjustedToMinimum', { value: notice.value });
      case 'max':
        return t('components.forms.numberField.adjustedToMaximum', { value: notice.value });
      case 'empty':
        return t('settings.audio.audioFilters.emptyEntryRestored', { value: notice.value });
    }
    return '';
  });

  const labelClasses = $derived(eqLabelClasses(variant));

  function handleChange(input: HTMLInputElement) {
    const result = resolveNumericEntry(input.value, min, max, value);
    input.value = String(result.value);
    notice = result.notice ? { kind: result.notice, value: result.value } : null;
    if (result.notice === 'empty') return;
    lastCommitted = result.value;
    onCommit(result.value);
  }
</script>

<div class={EQ_PARAMETER_CELL_CLASS}>
  <label class={labelClasses.label} for={inputId}>
    <span class={labelClasses.text}>{label}{unit ? ` (${unit})` : ''}</span>
  </label>
  <input
    id={inputId}
    type="number"
    class="input input-sm w-full"
    {value}
    {min}
    {max}
    {step}
    {disabled}
    aria-describedby={notice ? noticeId : undefined}
    oninput={() => (notice = null)}
    onchange={e => handleChange(e.currentTarget)}
  />
  <p id={noticeId} role="status" class="help-text md:self-start" class:pt-1={notice !== null}>
    {noticeText}
  </p>
</div>

<!--
  Equalizer Number Input

  Purpose: One numeric parameter of an equalizer filter (frequency, Q, width or gain)
  in the equalizer editor: a labelled number input that commits on change (blur or
  Enter), never per keystroke, and reports when it changed the entry.

  Behaviour:
  - On change the entry is resolved against [min, max] and the step grid: an entry
    outside the range is clamped to the nearest bound, an entry inside it is rounded to
    the nearest step, and an entry that is not a number keeps the stored value. The
    input then shows the resolved value, and onCommit receives it unless the entry was
    not a number.
  - onNotice receives why the shown value differs from the entry (clamped or kept), or
    null when there is nothing to say: after an in-range commit, on the next edit, and
    when the value prop changes to anything other than what this input just committed.
    The parent shows the notice in space it reserves, so a notice never moves the page,
    and passes its id back as noticeId to link it with aria-describedby.

  Props:
  - label: Translated parameter name
  - unit: Unit symbol shown after the label (optional)
  - value: Stored value
  - min, max, step: Input range and step
  - disabled: Disables the input
  - variant: 'edit' for an existing filter row, 'new' for the new-filter row (label style)
  - noticeId: Id of the notice that currently describes this input, if any
  - onCommit: Called with the committed value
  - onNotice: Called with the notice for this input, or null to withdraw it

  @component
-->
<script module lang="ts">
  /**
   * Layout of one parameter cell in an equalizer row (label, control). From the md
   * breakpoint the cell spans the first two rows of the row grid as a subgrid, so the
   * labels and the controls of a row each share one track; the third row is the row's
   * notice band. Labels and controls sit at the bottom of their tracks.
   */
  export const EQ_PARAMETER_CELL_CLASS =
    'flex flex-col md:row-span-2 md:grid md:grid-rows-subgrid md:gap-y-0 md:items-end';

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
  import { untrack } from 'svelte';
  import { generateId } from '$lib/utils/uuid';
  import { resolveNumericEntry, type EqFieldNotice } from '$lib/utils/audio/equalizer';

  interface Props {
    label: string;
    unit?: string;
    value: number;
    min: number;
    max: number;
    step: number;
    disabled?: boolean;
    variant: EqRowVariant;
    noticeId?: string;
    onCommit: (_value: number) => void;
    onNotice: (_notice: EqFieldNotice | null) => void;
  }

  let {
    label,
    unit,
    value,
    min,
    max,
    step,
    disabled = false,
    variant,
    noticeId,
    onCommit,
    onNotice,
  }: Props = $props();

  const inputId = generateId('eq-param');

  // The value prop this input expects next: the value it last received, or the one it
  // just committed. A plain variable, not state: the effect below must rerun only when
  // the value prop changes, not when a commit records its value.
  let expectedValue: number | undefined;

  // A value that arrives from outside (not the one this input just committed) makes
  // any notice stale. Only the value prop is tracked: the parent passes a new onNotice
  // whenever it re-renders, which must not count as a change.
  $effect(() => {
    const current = value;
    untrack(() => {
      if (current !== expectedValue) onNotice(null);
      expectedValue = current;
    });
  });

  const labelClasses = $derived(eqLabelClasses(variant));

  function handleChange(input: HTMLInputElement) {
    const result = resolveNumericEntry(input.value, min, max, value, step);
    input.value = String(result.value);
    onNotice(result.notice ? { kind: result.notice, value: result.value } : null);
    if (result.notice === 'empty') return;
    expectedValue = result.value;
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
    aria-describedby={noticeId}
    oninput={() => onNotice(null)}
    onchange={e => handleChange(e.currentTarget)}
  />
</div>

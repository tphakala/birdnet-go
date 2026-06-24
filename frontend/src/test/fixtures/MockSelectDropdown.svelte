<!--
  Test fixture: a functional stand-in for SelectDropdown.svelte.

  The real SelectDropdown is a portal-based custom widget that is awkward to
  drive in jsdom. This mock renders a plain native <select> wired to the same
  `onChange` prop and exposes the passed `options` as real <option> elements so
  tests can both drive selection (fireEvent.change) and assert on the available
  options without opening a popup. The `data-testid` is derived from `label`
  so multiple dropdowns in the same form can be addressed individually.

  @component
-->
<script lang="ts">
  interface Option {
    value: string;
    label: string;
  }

  let {
    value = '',
    label = '',
    options = [],
    disabled = false,
    onChange,
  }: {
    value?: string | string[];
    label?: string;
    options?: Option[];
    disabled?: boolean;
    onChange?: (_value: string | string[]) => void;
  } = $props();

  const selected = $derived(Array.isArray(value) ? (value[0] ?? '') : value);
</script>

<select
  data-testid={`select-${label}`}
  value={selected}
  {disabled}
  onchange={e => onChange?.((e.currentTarget as HTMLSelectElement).value)}
>
  {#each options as opt (opt.value)}
    <option value={opt.value}>{opt.label}</option>
  {/each}
</select>

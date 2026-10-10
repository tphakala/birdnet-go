<script lang="ts">
  import RadioCardGroup from './RadioCardGroup.svelte';
  import type { RadioCardOption } from './RadioCardGroup.types';

  interface Props {
    options: RadioCardOption[];
    initial: string | null;
    spy: (_value: string) => void;
    columns?: 1 | 2;
    className?: string;
    groupAttrs?: Record<string, string>;
    /** When set, the option that was checked before a change is removed from the list by that change. */
    dropPrevious?: boolean;
  }

  let {
    options,
    initial,
    spy,
    columns,
    className,
    groupAttrs = {},
    dropPrevious = false,
  }: Props = $props();

  let dropped = $state<string[]>([]);
  let shown = $derived(options.filter(o => !dropped.includes(o.value)));

  // The parent owns the selection, as in the wizard steps
  // svelte-ignore state_referenced_locally
  let value = $state<string | null>(initial);
</script>

<button type="button">before</button>
<RadioCardGroup
  options={shown}
  {value}
  {columns}
  {className}
  aria-label="Test group"
  onChange={v => {
    spy(v);
    if (dropPrevious && value !== null) dropped.push(value);
    value = v;
  }}
  {...groupAttrs}
/>
<button type="button">after</button>

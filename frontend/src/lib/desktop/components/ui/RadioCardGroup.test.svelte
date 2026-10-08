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
  }

  let { options, initial, spy, columns, className, groupAttrs = {} }: Props = $props();

  // The parent owns the selection, as in the wizard steps
  // svelte-ignore state_referenced_locally
  let value = $state<string | null>(initial);
</script>

<button type="button">before</button>
<RadioCardGroup
  {options}
  {value}
  {columns}
  {className}
  aria-label="Test group"
  onChange={v => {
    spy(v);
    value = v;
  }}
  {...groupAttrs}
/>
<button type="button">after</button>

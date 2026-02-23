<!--
  Tests: {#each options as option (option.value)} pattern
  Reproduces: SelectDropdown (line 477) and SelectField (line 103)
  Bug: When options array has duplicate value fields, Svelte emits each_key_duplicate warning
-->
<script lang="ts">
  interface Option {
    value: string;
    label: string;
  }

  interface Props {
    options: Option[];
  }

  let { options }: Props = $props();
</script>

<select data-testid="option-select">
  {#each options as option (option.value)}
    <option value={option.value}>{option.label}</option>
  {/each}
</select>

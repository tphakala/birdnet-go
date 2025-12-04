<script lang="ts">
  /* eslint-disable no-unused-vars -- $bindable() props are used in template bind: directives */
  interface Props {
    id?: string;
    label: string;
    checked: boolean;
    disabled?: boolean;
    helpText?: string;
    onchange?: (checked: boolean) => void;
  }

  let {
    id = crypto.randomUUID(),
    label,
    checked = $bindable(),
    disabled = false,
    helpText,
    onchange,
  }: Props = $props();

  function handleChange() {
    checked = !checked;
    onchange?.(checked);
  }
</script>

<div class="form-control w-full">
  <label class="label cursor-pointer justify-between py-4" for={id}>
    <div class="flex flex-col gap-1">
      <span class="label-text font-medium">{label}</span>
      {#if helpText}
        <span class="label-text-alt text-base-content/60">{helpText}</span>
      {/if}
    </div>
    <input
      {id}
      type="checkbox"
      class="toggle toggle-primary"
      bind:checked
      {disabled}
      onchange={handleChange}
    />
  </label>
</div>

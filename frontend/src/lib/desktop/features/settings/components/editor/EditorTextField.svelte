<!--
  EditorTextField - Text or number input with label, matching AlertRuleEditor styling.

  Features:
  - Pure Tailwind styling (no DaisyUI)
  - Text and number input types
  - Optional help text
  - Number clamping on blur
  - Accessible label association

  @component
-->
<script lang="ts">
  interface Props {
    label: string;
    value: string | number;
    onUpdate: (_value: string | number) => void;
    type?: 'text' | 'number';
    placeholder?: string;
    min?: number;
    max?: number;
    step?: number;
    disabled?: boolean;
    helpText?: string;
    id?: string;
  }

  let {
    label,
    value = $bindable(),
    onUpdate,
    type = 'text',
    placeholder = '',
    min,
    max,
    step,
    disabled = false,
    helpText = '',
    id,
  }: Props = $props();

  // Unique suffix per instance to prevent ID collisions when labels repeat
  const idSuffix = crypto?.randomUUID?.().slice(0, 8) ?? Math.random().toString(36).slice(2, 10);

  const fieldId = $derived(
    id ||
      `editor-field-${label
        .toLowerCase()
        .replace(/\s+/g, '-')
        .replace(/[^a-z0-9-]/g, '')}-${idSuffix}`
  );
  const helpId = $derived(helpText ? `${fieldId}-help` : undefined);

  function handleChange(event: Event) {
    const target = event.currentTarget as HTMLInputElement;
    if (type === 'number') {
      const num = parseFloat(target.value);
      if (!isNaN(num)) {
        const clamped =
          min !== undefined && num < min ? min : max !== undefined && num > max ? max : num;
        value = clamped;
        onUpdate(clamped);
      }
    } else {
      value = target.value;
      onUpdate(target.value);
    }
  }
</script>

<div>
  <label for={fieldId} class="block text-xs font-medium text-[var(--color-base-content)]/60 mb-1">
    {label}
  </label>
  <input
    {type}
    id={fieldId}
    {placeholder}
    {disabled}
    {min}
    {max}
    {step}
    bind:value
    onchange={handleChange}
    aria-describedby={helpId}
    class="w-full px-3 py-2 rounded-lg text-sm bg-[var(--color-base-200)] border border-[var(--color-base-300)] text-[var(--color-base-content)] placeholder:text-[var(--color-base-content)]/40 outline-none focus:ring-2 focus:ring-[var(--color-primary)]/20 focus:border-[var(--color-primary)] transition-colors tabular-nums disabled:opacity-50 disabled:cursor-not-allowed"
  />
  {#if helpText}
    <span id={helpId} class="text-xs text-[var(--color-base-content)]/40 mt-1 block"
      >{helpText}</span
    >
  {/if}
</div>

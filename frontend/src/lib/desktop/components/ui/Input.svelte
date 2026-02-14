<script lang="ts">
  import { cn } from '$lib/utils/cn';

  interface Props {
    type?:
      | 'text'
      | 'email'
      | 'password'
      | 'number'
      | 'date'
      | 'datetime-local'
      | 'time'
      | 'url'
      | 'tel'
      | 'checkbox'
      | 'radio'
      | 'file'
      | 'search'
      | 'color'
      | 'range';
    value?: string;
    id?: string;
    name?: string;
    placeholder?: string;
    disabled?: boolean;
    readonly?: boolean;
    required?: boolean;
    className?: string;
    min?: string | number;
    max?: string | number;
    step?: string | number;
    pattern?: string;
    maxlength?: number;
    'aria-label'?: string;
    'aria-describedby'?: string;
  }

  let {
    type = 'text',
    value = $bindable(),
    id,
    name,
    placeholder,
    disabled = false,
    readonly = false,
    required = false,
    className = '',
    min,
    max,
    step,
    pattern,
    maxlength,
    'aria-label': ariaLabel,
    'aria-describedby': ariaDescribedby,
    ...rest
  }: Props = $props();
</script>

<input
  {type}
  bind:value
  {id}
  {name}
  {placeholder}
  {disabled}
  {readonly}
  {required}
  {min}
  {max}
  {step}
  {pattern}
  {maxlength}
  aria-label={ariaLabel}
  aria-describedby={ariaDescribedby}
  class={cn('input w-full', className)}
  {...rest}
/>

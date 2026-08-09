<script lang="ts">
  import { formatNumber } from '$lib/utils/formatters';

  type Tone = 'default' | 'muted' | 'success' | 'error';
  type Size = 'sm' | 'md' | 'lg';

  interface Props {
    value: number;
    label: string;
    tone?: Tone;
    size?: Size;
  }

  let { value, label, tone = 'default', size = 'sm' }: Props = $props();

  function toneClass(t: Tone): string {
    switch (t) {
      case 'default':
        return 'text-[var(--color-base-content)]';
      case 'muted':
        return 'text-[var(--color-base-content)]/60';
      case 'success':
        return 'text-[var(--color-success)]';
      case 'error':
        return 'text-[var(--color-error)]';
    }
  }

  function boxClass(s: Size): string {
    switch (s) {
      case 'sm':
      case 'md':
        return 'p-2 rounded';
      case 'lg':
        return 'p-3 rounded-lg';
    }
  }

  function valueClass(s: Size): string {
    switch (s) {
      case 'sm':
        return 'text-base font-semibold';
      case 'md':
        return 'text-lg font-semibold';
      case 'lg':
        return 'text-xl font-bold';
    }
  }
</script>

<div class="text-center {boxClass(size)} bg-[var(--color-base-200)]">
  <div class="{valueClass(size)} {toneClass(tone)}">
    {formatNumber(value)}
  </div>
  <div class="text-xs text-[var(--color-base-content)]/60">
    {label}
  </div>
</div>

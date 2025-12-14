<!--
  BandReject Filter Icon Component
  Thin wrapper for use with SelectDropdown icon prop

  @component
-->
<script lang="ts">
  import type { HTMLAttributes } from 'svelte/elements';
  import { cn } from '$lib/utils/cn.js';
  import BandRejectSvg from '$lib/assets/icons/filters/bandreject.svg?raw';

  interface Props extends HTMLAttributes<HTMLElement> {
    class?: string;
  }

  let { class: className = '', ...rest }: Props = $props();
</script>

<span
  class={cn('size-5 shrink-0 [&>svg]:size-full [&>svg]:block', className)}
  aria-hidden="true"
  {...rest}
>
  {@html BandRejectSvg}
</span>

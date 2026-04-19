<script lang="ts">
  import type { HTMLAttributes } from 'svelte/elements';
  import { cn } from '$lib/utils/cn.js';
  import GithubMarkSvg from '$lib/assets/icons/github-mark.svg?raw';

  interface Props extends HTMLAttributes<HTMLElement> {
    class?: string;
  }

  let { class: className = '', ...rest }: Props = $props();
</script>

<span
  class={cn('inline-flex shrink-0 [&>svg]:size-full [&>svg]:block', className)}
  aria-hidden="true"
  {...rest}
>
  {@html GithubMarkSvg}
</span>

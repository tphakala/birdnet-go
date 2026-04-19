<script lang="ts">
  import type { HTMLAttributes } from 'svelte/elements';
  import { cn } from '$lib/utils/cn';
  // @lucide/svelte v1.0 dropped all brand icons including Github.
  // This component is an intentional exception to the "no inline SVGs" rule,
  // following the same {@html} pattern used by BandRejectIcon.svelte and DatabaseIcon.svelte.
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

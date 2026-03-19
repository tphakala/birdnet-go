<script lang="ts">
  import type { ContentStep } from './types';

  interface Props {
    step: ContentStep;
  }

  let { step }: Props = $props();
</script>

<!--
  Safety note: step.content is developer-controlled changelog/release-note HTML,
  not user-supplied input. Using {@html} is intentional and safe here.
-->
<div
  class="text-sm leading-relaxed text-[var(--color-base-content)] [&_h3]:text-base [&_h3]:font-semibold [&_h3]:mt-4 [&_h3]:mb-2 [&_p]:mb-3 [&_ul]:list-disc [&_ul]:pl-5 [&_ul]:mb-3 [&_li]:mb-1 [&_code]:bg-[var(--color-base-300)] [&_code]:px-1 [&_code]:rounded [&_code]:text-xs [&_a]:text-[var(--color-primary)] [&_a]:underline"
>
  {@html step.content}
</div>

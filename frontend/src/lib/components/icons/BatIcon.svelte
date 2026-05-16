<!--
  Bat Icon Component

  Temporary local icon based on Lucide PR #3914 (by RajnishKMehta).
  Replace with `import { Bat } from '@lucide/svelte'` once the icon
  ships in an official Lucide release.

  Matches the Lucide Svelte component API: accepts `class` and `size` props.

  @component
-->
<script lang="ts">
  interface Props {
    size?: number;
    class?: string;
  }

  let { size = 24, class: className = '' }: Props = $props();
</script>

<svg
  xmlns="http://www.w3.org/2000/svg"
  width={size}
  height={size}
  viewBox="0 0 24 24"
  fill="none"
  stroke="currentColor"
  stroke-width="2"
  stroke-linecap="round"
  stroke-linejoin="round"
  class={className}
  aria-hidden="true"
>
  <path
    d="M12 20c1.111-3.048 3-5 5-4 .74-2.286 2.778-3.762 5-3-.173-2.595.13-5.314-2-7.5-1.708 2.648-3.358 2.557-5 2.5-3-6 6-7-3-2-9-5 0-4-3 2-1.5 0-3.292.148-5-2.5-2.13 2.186-1.827 4.905-2 7.5 2.222-.762 4.26.714 5 3 2-1 3.889.952 5 4"
  />
  <path d="M9 8a3 3 0 0 0 6 0" />
</svg>

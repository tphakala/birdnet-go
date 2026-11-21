<!--
  Test fixture: stand-in for RootLayout that renders only its page content.

  The App tests that stub RootLayout to nothing cannot see the routed page. This one keeps the
  page (children) and drops the sidebar, header and banners.

  @component
-->
<script lang="ts">
  import type { Snippet } from 'svelte';

  let { children }: { children?: Snippet } = $props();
</script>

{@render children?.()}

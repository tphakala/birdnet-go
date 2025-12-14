<!--
  BandReject Filter Icon Component
  Thin wrapper for use with SelectDropdown icon prop

  @component
-->
<script lang="ts">
  import BandRejectSvg from '$lib/assets/icons/filters/bandreject.svg?raw';

  interface Props {
    class?: string;
  }

  let { class: className = 'size-5' }: Props = $props();
</script>

<span class="{className} shrink-0 [&>svg]:size-full [&>svg]:block">
  {@html BandRejectSvg}
</span>

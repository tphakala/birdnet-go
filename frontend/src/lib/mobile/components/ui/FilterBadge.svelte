<script lang="ts">
  interface Props {
    count: number;
  }

  let { count }: Props = $props();
</script>

{#if count > 0}
  <span class="badge badge-primary badge-xs absolute -top-1 -right-1">{count}</span>
{/if}

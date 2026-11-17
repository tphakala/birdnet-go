<!--
  Test fixture: stand-in for StreamChannelControls. Its button asks for a channel
  analysis of the URL it was given; the root shows whether an analysis runs and
  the channel mode the parent holds, so a test can follow a late analysis result.

  @component
-->
<script lang="ts">
  interface Props {
    channelMode?: string;
    analyzeUrl?: string;
    isAnalyzing?: boolean;
    onAnalyze?: (_url: string) => void;
  }

  let { channelMode = '', analyzeUrl = '', isAnalyzing = false, onAnalyze }: Props = $props();
</script>

<div
  data-testid="channel-controls"
  data-mode={channelMode}
  data-analyzing={isAnalyzing ? 'true' : 'false'}
>
  <button type="button" data-testid="channel-analyze" onclick={() => onAnalyze?.(analyzeUrl)}>
    analyze-channels
  </button>
</div>

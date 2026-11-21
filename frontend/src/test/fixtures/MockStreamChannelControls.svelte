<!--
  Test fixture: stand-in for StreamChannelControls. Its button asks for a channel
  analysis of the URL it was given; the root shows whether an analysis runs and
  the channel mode, detected channel count and analysis error the parent holds, so a test
  can follow a late analysis result; a second button picks a channel by hand.

  @component
-->
<script lang="ts">
  interface Props {
    channelMode?: string;
    channels?: number;
    analyzeUrl?: string;
    isAnalyzing?: boolean;
    analysisError?: string | null;
    onAnalyze?: (_url: string) => void;
    onChange?: (_mode: 'left' | 'right') => void;
  }

  let {
    channelMode = '',
    channels = 0,
    analyzeUrl = '',
    isAnalyzing = false,
    analysisError = null,
    onAnalyze,
    onChange,
  }: Props = $props();
</script>

<div
  data-testid="channel-controls"
  data-mode={channelMode}
  data-channels={channels}
  data-analyzing={isAnalyzing ? 'true' : 'false'}
  data-error={analysisError ?? ''}
>
  <button type="button" data-testid="channel-analyze" onclick={() => onAnalyze?.(analyzeUrl)}>
    analyze-channels
  </button>
  <button type="button" data-testid="channel-pick-right" onclick={() => onChange?.('right')}>
    pick-right
  </button>
</div>

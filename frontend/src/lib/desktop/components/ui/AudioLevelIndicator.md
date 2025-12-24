# AudioLevelIndicator Component

A Svelte 5 TypeScript component that displays real-time audio levels from connected microphones and allows audio streaming via HLS.

## Features

- Real-time audio level visualization with circular progress indicator
- Support for multiple audio sources with dropdown selection
- HLS audio streaming with play/stop controls
- Automatic reconnection for SSE (Server-Sent Events)
- Media session API integration for browser media controls
- Responsive design with accessibility features
- Clipping detection with visual feedback (red indicator)

## Usage

```svelte
<script>
  import AudioLevelIndicator from '$lib/components/ui/AudioLevelIndicator.svelte';
</script>

<AudioLevelIndicator securityEnabled={false} accessAllowed={true} />
```

## Props

- `className` (string, optional): Additional CSS classes
- `securityEnabled` (boolean, optional): Whether security is enabled in the app
- `accessAllowed` (boolean, optional): Whether the user has access to audio features

## API Integration

Uses v2 API endpoints:

- `/api/v2/streams/audio-level` - SSE endpoint for real-time audio levels
- `/api/v2/streams/hls/{sourceId}/start` - Start audio streaming
- `/api/v2/streams/hls/{sourceId}/stop` - Stop audio streaming
- `/api/v2/streams/hls/{sourceId}/playlist.m3u8` - HLS playlist
- `/api/v2/streams/hls/heartbeat` - Keep-alive heartbeat

## Dependencies

- HLS.js library (loaded via script tag in main HTML)
- Tailwind CSS for styling
- Browser support for EventSource API

## Implementation Notes

1. **Audio Sources**: The component automatically detects available audio sources from the SSE stream
2. **Smoothing**: Audio levels are smoothed using a factor of 0.4 for better visualization
3. **Inactive Detection**: Sources are marked as "silent" after 5 seconds of zero level
4. **Error Handling**: Includes automatic reconnection with exponential backoff
5. **Performance**: Cleans up resources on component destroy and page navigation

## Browser Support

- Modern browsers with EventSource support
- HLS.js for non-native HLS support
- Native HLS support for Safari/iOS
- Media Session API for enhanced media controls (where supported)

## Security Considerations

- CSRF token handling should be implemented at the application level
- Audio streaming requires proper authentication when security is enabled
- The component respects `securityEnabled` and `accessAllowed` props

## Future Enhancements

- WebRTC support for lower latency
- Recording capabilities
- Audio visualization enhancements

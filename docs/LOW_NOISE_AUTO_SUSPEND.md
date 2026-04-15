# Low-Noise Auto-Suspend Configuration Example

This feature allows BirdNET-Go to automatically suspend audio analysis when the audio level is consistently low, reducing unnecessary CPU usage during quiet periods.

## Configuration

Add the `lowNoiseAutoSleep` section to your audio source configuration in `config.yaml`:

```yaml
realtime:
  audio:
    sources:
      - name: "Front Yard Microphone"
        device: "hw:0,0"
        lownoiseautosleep:
          enabled: true
          suspendthreshold: 15      # Audio level (0-100) below which to suspend analysis
          resumethreshold: 25       # Audio level (0-100) above which to resume analysis
          minsuspendframes: 3       # Consecutive low-volume frames before suspending (default: 3)
          minresumeframes: 2        # Consecutive high-volume frames before resuming (default: 2)
```

## Parameters

### `enabled` (boolean)
- **Default**: `false`
- **Description**: Enable or disable low-noise auto-suspend for this audio source

### `suspendthreshold` (integer, 0-100)
- **Required**: Yes (when enabled)
- **Description**: Audio level below which analysis will be suspended after `minsuspendframes` consecutive frames
- **Example**: `15` means suspend when audio level drops to 15 or below

### `resumethreshold` (integer, 0-100)
- **Required**: Yes (when enabled)
- **Description**: Audio level above which analysis will resume after `minresumeframes` consecutive frames
- **Example**: `25` means resume when audio level rises to 25 or above
- **Important**: Must be higher than `suspendthreshold` to prevent oscillation (hysteresis)

### `minsuspendframes` (integer, >= 0)
- **Default**: `3`
- **Description**: Number of consecutive low-volume frames required before suspending analysis
- **Purpose**: Prevents brief quiet moments from triggering suspension

### `minresumeframes` (integer, >= 0)
- **Default**: `2`
- **Description**: Number of consecutive high-volume frames required before resuming analysis
- **Purpose**: Ensures sustained audio activity before resuming

## How It Works

1. **Audio Level Monitoring**: The system continuously monitors the RMS audio level (0-100) for each audio source
2. **Suspension Logic**: When audio level drops below `suspendthreshold` for `minsuspendframes` consecutive frames, analysis is suspended
3. **Hysteresis Zone**: Audio levels between `suspendthreshold` and `resumethreshold` maintain the current state without triggering changes
4. **Resume Logic**: When audio level rises above `resumethreshold` for `minresumeframes` consecutive frames, analysis resumes
5. **No Data Loss**: Audio data continues to be captured but BirdNET inference is skipped during suspension

## Benefits

- **Reduced CPU Usage**: Saves computational resources during quiet periods (e.g., nighttime, indoor silence)
- **Lower Power Consumption**: Particularly beneficial for battery-powered or edge devices
- **Automatic Operation**: No manual intervention required - adapts to ambient noise levels
- **Per-Source Configuration**: Each audio source can have different thresholds based on its environment

## Example Scenarios

### Outdoor Microphone (Variable Noise)
```yaml
lownoiseautosleep:
  enabled: true
  suspendthreshold: 20
  resumethreshold: 30
  minsuspendframes: 5    # Wait longer to avoid wind gusts
  minresumeframes: 2
```

### Indoor Microphone (Quiet Environment)
```yaml
lownoiseautosleep:
  enabled: true
  suspendthreshold: 10   # Lower threshold for quieter environment
  resumethreshold: 20
  minsuspendframes: 3
  minresumeframes: 2
```

### High-Traffic Area (Frequent Activity)
```yaml
lownoiseautosleep:
  enabled: true
  suspendthreshold: 30   # Higher threshold to avoid frequent suspensions
  resumethreshold: 40
  minsuspendframes: 10   # Require longer quiet period
  minresumeframes: 3
```

## Monitoring

When enabled, the system logs suspension and resume events:

```
INFO  analysis suspended due to low audio level source=Front_Yard audio_level=12 suspend_threshold=15
INFO  analysis resumed due to high audio level source=Front_Yard audio_level=28 resume_threshold=25 suspended_duration=5m30s
```

Debug logging provides additional details:
```
DEBUG volume suspend state changed source=Front_Yard suspended=true audio_level=12
DEBUG analysis still suspended source=Front_Yard audio_level=8 suspended_duration=10m15s
```

## Validation Rules

The configuration is validated on startup:
- `suspendthreshold` must be between 0 and 100
- `resumethreshold` must be between 0 and 100
- `resumethreshold` must be greater than `suspendthreshold` (hysteresis requirement)
- `minsuspendframes` and `minresumeframes` must be non-negative

## Troubleshooting

### Analysis Never Suspends
- Check if audio levels are consistently above `suspendthreshold`
- Reduce `suspendthreshold` value
- Increase `minsuspendframes` if brief quiet periods are not reaching the threshold

### Analysis Suspends Too Frequently
- Increase `suspendthreshold` value
- Increase `minsuspendframes` to require longer quiet periods
- Widen the gap between `suspendthreshold` and `resumethreshold`

### Oscillating Between Suspended and Active
- Ensure `resumethreshold` is sufficiently higher than `suspendthreshold`
- Increase `minresumeframes` to require more sustained activity before resuming
- Increase `minsuspendframes` to require more sustained quiet before suspending

## Performance Impact

- **Suspended State**: Near-zero CPU usage for BirdNET inference (only audio level calculation)
- **Transition Overhead**: Minimal - state changes are logged but don't impact performance
- **Memory Usage**: Negligible - only tracks state per audio source

## Compatibility

- Works with all audio source types (ALSA devices, RTSP streams, etc.)
- Compatible with multi-model analysis (each source can have different settings)
- Does not interfere with audio export, sound level monitoring, or other features

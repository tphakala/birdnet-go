# Audio Formats

BirdNET-Go supports five formats for saving detection audio clips: **AAC**, **Opus**, **FLAC**, **WAV**, and **MP3**. All five formats are encoded and decoded natively in Go without FFmpeg.

## Recommended Formats

- **AAC (`aac`, `.m4a`) - best general-purpose lossy format:** Offers the widest compatibility across browsers, phones, and media players while keeping file sizes small. This is the default format in `install.sh`.
- **Opus (`opus`, `.opus`) - best audio quality with the smallest files:** Produces the best audio quality with the smallest file sizes. A strong choice when disk space is limited.
- **FLAC (`flac`, `.flac`) - recommended for post-processing and archival:** Uses lossless compression to preserve the exact original audio with smaller files than WAV, and embeds detection metadata as Vorbis comments. Choose FLAC if you plan to edit clips in external audio software (noise reduction, filtering, spectral inspection) or use recordings to train a custom classifier.
- **WAV (`wav`, `.wav`) - uncompressed lossless:** Uncompressed PCM audio with no compression step; produces the largest files. Useful when avoiding compression overhead or when tools require raw PCM WAV.
- **MP3 (`mp3`, `.mp3`) - discouraged:** MP3 is an older format that delivers lower audio quality than AAC and Opus at the same bitrate. Avoid MP3 for new setups unless a legacy tool specifically requires it. The System Health page (**System > Health**) reports a warning when clip export is configured to use MP3.

## Format Comparison

| Format | Config value | File extension | Type     | Bitrate range      | Best for                                               |
| ------ | ------------ | -------------- | -------- | ------------------ | ------------------------------------------------------ |
| AAC    | `aac`        | `.m4a`         | Lossy    | `32k` to `320k`    | Widest device and browser compatibility                |
| Opus   | `opus`       | `.opus`        | Lossy    | `32k` to `256k`\*  | Best audio quality with the smallest file sizes        |
| FLAC   | `flac`       | `.flac`        | Lossless | N/A (lossless)     | External post-processing, archival, and bat recordings |
| WAV    | `wav`        | `.wav`         | Lossless | N/A (uncompressed) | Avoiding compression overhead and bat recordings       |
| MP3    | `mp3`        | `.mp3`         | Lossy    | `32k` to `320k`    | Legacy compatibility only (discouraged)                |

\*`realtime.audio.export.bitrate` accepts `32k` to `320k` in configuration; Opus caps the effective encoding bitrate at `256k`.

## Bat and Ultrasonic Recordings

For bat and other ultrasonic recordings captured above 48 kHz, **only lossless formats (`flac` and `wav`) are supported**.

Lossy codecs (AAC, Opus, and MP3) discard frequencies above roughly 20 kHz and cannot store ultrasonic bat echolocation calls. Because of this, BirdNET-Go uses a separate setting for ultrasonic captures:

- `realtime.audio.export.ultrasonictype`: accepts only `flac` (default) or `wav`.
- Bat detections captured above 48 kHz are always saved losslessly at their full source sample rate (96 to 384 kHz) using `ultrasonictype`, regardless of what `realtime.audio.export.type` is set to.
- Non-bat detections (such as birds) from a shared high-sample-rate source are downsampled to 48 kHz and saved using your regular `realtime.audio.export.type`.

## Bitrate and Clip Denoising

- **Bitrate (`realtime.audio.export.bitrate`):** Applies only to lossy formats (`aac`, `opus`, and `mp3`) and is ignored for `wav` and `flac`. The default in `config.yaml` is `96k`.
- **Denoising:** Built-in clip denoising works best on lossless clips (`flac` or `wav`). For lossy formats, bitrates below `64k` introduce compression artifacts that limit how clean the denoised result can get.

## Native Processing and Downloads

Saved clip exports, on-demand clip downloads from the web UI (Original, WAV, FLAC, AAC, Opus, and MP3), loudness normalization, denoise and gain previews, spectrogram rendering, and audible-bats playback all run natively without FFmpeg.

Formats without native Go codecs in BirdNET-Go, such as ALAC, Ogg Vorbis, and raw `.aac` (ADTS), are not supported.

## Input Audio Formats

- **Local sound cards:** Use 16-bit PCM mono at 48 kHz for bird detection (stereo input is downmixed to mono; 24-bit, 32-bit, and float device formats are converted to 16-bit PCM internally). For ultrasonic bat detection, supported capture rates are 96, 192, 256, and 384 kHz (BattyBirdNET requires at least 96 kHz with 192 kHz recommended; BSG-BAT requires at least 192 kHz with 384 kHz recommended).
- **RTSP and network streams:** Prefer lossless PCM (for example `pcm_s16be` / L16 over RTSP) or clean mono AAC/Opus at 48 kHz. Low-bitrate camera audio codecs often cut off high frequencies (~5 to 8 kHz), which degrades detection of high-pitched birds. For bat detection over a network stream, the stream must carry a lossless codec (such as PCM or FLAC) at 96 kHz or higher (192 kHz or higher for BSG-BAT); lossy stream codecs destroy ultrasonic content.

## Configuration

Audio export format settings can be changed in the web UI under **Settings > Audio > Recording** (**File Settings**) or in `config.yaml`. Changes hot-reload immediately without restarting BirdNET-Go.

```yaml
realtime:
  audio:
    export:
      enabled: true
      path: clips/
      type: aac # Recommended: aac, opus, flac, or wav (mp3 is discouraged)
      ultrasonictype: flac # Lossless only: flac (default) or wav, for bat captures above 48 kHz
      bitrate: 96k # Applies to lossy formats only (32k to 320k; Opus caps at 256k)
```

/**
 * Shared query options for the AudioPlayer tests.
 *
 * The player announces the spectrogram state twice: in a visually hidden role=status
 * region for screen readers and in a visible overlay. Pass this to getByText and
 * queryByText to assert on the overlay only.
 */
export const OVERLAY_ONLY = { ignore: '[role="status"], script, style' };

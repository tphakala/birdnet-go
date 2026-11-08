/**
 * Shared queries and query options for the AudioPlayer and AudioToolbar tests.
 *
 * OVERLAY_ONLY: the player announces the spectrogram state twice: in a visually hidden role=status
 * region for screen readers and in a visible overlay. Pass this to getByText and
 * queryByText to assert on the overlay only.
 */
export const OVERLAY_ONLY = { ignore: '[role="status"], script, style' };

/**
 * Matches the accessible name of the Denoise trigger in the toolbar: the control word,
 * a space, then the state it shows ("Denoise Light"). The options inside its menu start
 * with the same key but have no space after it, so they do not match.
 */
export const DENOISE_BUTTON_NAME = /^components\.audioPlayer\.processing\.denoise /;

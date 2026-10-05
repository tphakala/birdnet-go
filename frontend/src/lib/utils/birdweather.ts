/** A BirdWeather station token: 24 ASCII letters and digits. Must match birdweatherIDPattern in internal/conf/validate.go. */
const BIRDWEATHER_TOKEN_PATTERN = /^[A-Za-z0-9]{24}$/;

/** Whether value, trimmed, is a well-formed BirdWeather token. */
export function isBirdweatherToken(value: string): boolean {
  return BIRDWEATHER_TOKEN_PATTERN.test(value.trim());
}

/** Why a BirdWeather token cannot be saved: not entered, or not a 24 character token. */
export type BirdweatherTokenProblem = 'required' | 'format';

/**
 * The problem with token while BirdWeather sharing is on, or null when it can be saved as it is.
 * With sharing off the token is never checked, as the backend does not check it either.
 * A missing (null or undefined) token counts as not entered.
 * The backend does not trim, so a token with whitespace around it is a format problem here.
 */
export function birdweatherTokenProblem(
  enabled: boolean,
  token: string | null | undefined
): BirdweatherTokenProblem | null {
  if (!enabled) return null;
  const value = token ?? '';
  if (value.trim() === '') return 'required';
  return BIRDWEATHER_TOKEN_PATTERN.test(value) ? null : 'format';
}

/** A BirdWeather station token: 24 ASCII letters and digits. Must match birdweatherIDPattern in internal/conf/validate.go. */
const BIRDWEATHER_TOKEN_PATTERN = /^[A-Za-z0-9]{24}$/;

/** Whether value, trimmed, is a well-formed BirdWeather token. */
export function isBirdweatherToken(value: string): boolean {
  return BIRDWEATHER_TOKEN_PATTERN.test(value.trim());
}

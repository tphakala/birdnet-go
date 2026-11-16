import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { renderTyped, screen } from '../../../../../test/render-helpers';
import MoonBadge from './MoonBadge.svelte';

describe('overlay badges on the card media surface', () => {
  // The badges sit on a dark surface in both themes, so a monochrome emoji fallback must not
  // inherit the page text colour, which is dark in the light theme.
  it('MoonBadge sets white text', () => {
    renderTyped(MoonBadge, { props: { moonPhaseName: 'Full Moon' } });
    expect(screen.getByRole('img')).toHaveClass('text-white');
  });

  it('WeatherBadge sets white text on the whole badge, not only the temperature', () => {
    const source = readFileSync(
      join(dirname(fileURLToPath(import.meta.url)), 'WeatherBadge.svelte'),
      'utf8'
    );
    const badgeRule = /\.weather-badge\s*\{([^}]*)\}/.exec(source)?.[1] ?? '';
    expect(badgeRule).toMatch(/(?:^|;|\s)color:\s*white;/);
  });
});

import { describe, it, expect, afterEach } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import RestartCard from './RestartCard.svelte';
import { restartState } from '$lib/stores/restart.svelte';

const APPLICATION_BUTTON_NAME = 'restart.applicationRestart';
const CONTAINER_BUTTON_NAME = 'restart.containerRestart';

// The text tokens reach 4.5:1 on the 15% and 25% fill tints in both themes and on the pressed
// tint up to 30%; at 35% the dark error text falls to 4.2:1. Contrast itself is browser-only.
const TEXT_AND_TINTS = [
  [
    'application',
    APPLICATION_BUTTON_NAME,
    'text-[var(--text-warning)]',
    '--color-warning',
    /text-amber-/,
  ],
  ['container', CONTAINER_BUTTON_NAME, 'text-[var(--text-error)]', '--color-error', /text-red-/],
] as const;

describe('RestartCard buttons', () => {
  afterEach(() => {
    cleanup();
    restartState.container_restart_available = false;
  });

  it.each(TEXT_AND_TINTS)(
    'the %s button writes its label with a text token and keeps the pressed tint at 30%% or less',
    (_kind, name, textToken, fill, palette) => {
      restartState.container_restart_available = true;
      render(RestartCard);

      const button = screen.getByRole('button', { name });
      expect(button).toHaveClass(textToken);
      expect(button).toHaveClass(`active:bg-[var(${fill})]/30`);
      expect(button.className).not.toMatch(palette);
    }
  );
});

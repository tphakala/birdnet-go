import { describe, it, expect, afterEach } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import RestartCard from './RestartCard.svelte';
import { restartState } from '$lib/stores/restart.svelte';

// The text tokens reach 4.5:1 on the 15% and 25% fill tints in both themes and on the pressed
// tint up to 30%; at 35% the dark error text falls to 4.2:1. Contrast itself is browser-only.
describe('RestartCard buttons', () => {
  afterEach(() => {
    cleanup();
    restartState.container_restart_available = false;
  });

  it('writes both labels with a text token and keeps the pressed tint at 30% or less', () => {
    restartState.container_restart_available = true;
    render(RestartCard);

    expect(screen.getByRole('button', { name: 'restart.applicationRestart' })).toHaveClass(
      'text-[var(--text-warning)]',
      'active:bg-[var(--color-warning)]/30'
    );
    expect(screen.getByRole('button', { name: 'restart.containerRestart' })).toHaveClass(
      'text-[var(--text-error)]',
      'active:bg-[var(--color-error)]/30'
    );
  });
});

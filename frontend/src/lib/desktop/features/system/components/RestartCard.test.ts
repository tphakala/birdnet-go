import { describe, it, expect, afterEach } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import RestartCard from './RestartCard.svelte';

const RESTART_BUTTON_NAME = 'restart.applicationRestart';

describe('RestartCard', () => {
  afterEach(cleanup);

  it('writes the application restart label with the warning text token, which reaches AA on its amber tint', () => {
    render(RestartCard);

    const button = screen.getByRole('button', { name: RESTART_BUTTON_NAME });
    expect(button).toHaveClass('text-[var(--text-warning)]');
    expect(button.className).not.toMatch(/text-amber-/);
  });
});

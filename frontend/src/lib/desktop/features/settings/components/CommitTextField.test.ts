/**
 * CommitTextField commits a trimmed, valid, changed value on blur or Enter,
 * restores the saved value on Escape, and shows the validator's reason at the
 * field instead of committing. A draft pending when the focused field is removed
 * is committed, since the browser fires no blur then.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { cleanup, fireEvent, screen } from '@testing-library/svelte';
import { renderTyped } from '../../../../../test/render-helpers';
import CommitTextField from './CommitTextField.svelte';

function renderField(validate?: (value: string) => string | null) {
  const onCommit = vi.fn();
  const view = renderTyped(CommitTextField, {
    props: { id: 'name', value: 'Garden', validate, onCommit },
  });
  return { onCommit, view, input: screen.getByRole<HTMLInputElement>('textbox') };
}

describe('CommitTextField', () => {
  afterEach(() => {
    cleanup();
  });

  it('commits the trimmed value on blur and on Enter, never while typing or when unchanged', async () => {
    const { onCommit, input } = renderField();

    await fireEvent.input(input, { target: { value: '  Porch  ' } });
    expect(onCommit).not.toHaveBeenCalled();
    await fireEvent.blur(input);
    expect(onCommit).toHaveBeenCalledExactlyOnceWith('Porch');

    await fireEvent.input(input, { target: { value: 'Garden' } });
    await fireEvent.keyDown(input, { key: 'Enter' });
    // Unchanged against the saved value: nothing to commit.
    expect(onCommit).toHaveBeenCalledOnce();
  });

  it('restores the saved value on Escape', async () => {
    const { onCommit, input } = renderField();

    await fireEvent.input(input, { target: { value: 'Typo' } });
    await fireEvent.keyDown(input, { key: 'Escape' });
    expect(input.value).toBe('Garden');
    await fireEvent.blur(input);
    expect(onCommit).not.toHaveBeenCalled();
  });

  it('shows the reason a value is refused and clears it when the user edits', async () => {
    const { onCommit, input } = renderField(value => (value === 'Taken' ? 'Name taken' : null));

    await fireEvent.input(input, { target: { value: 'Taken' } });
    await fireEvent.blur(input);
    expect(onCommit).not.toHaveBeenCalled();
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(input).toHaveAccessibleDescription('Name taken');

    await fireEvent.input(input, { target: { value: 'Free' } });
    expect(input).not.toHaveAttribute('aria-invalid');
  });

  it('commits a pending draft when the focused field is removed without a blur', async () => {
    const { onCommit, view, input } = renderField();

    await fireEvent.focus(input);
    await fireEvent.input(input, { target: { value: 'Porch' } });
    view.unmount();
    expect(onCommit).toHaveBeenCalledExactlyOnceWith('Porch');
  });

  it('commits nothing on removal when the field is not focused or the draft is invalid', async () => {
    const first = renderField();
    await fireEvent.input(first.input, { target: { value: 'Porch' } });
    first.view.unmount();
    expect(first.onCommit).not.toHaveBeenCalled();

    const second = renderField(value => (value === 'Taken' ? 'Name taken' : null));
    await fireEvent.focus(second.input);
    await fireEvent.input(second.input, { target: { value: 'Taken' } });
    second.view.unmount();
    expect(second.onCommit).not.toHaveBeenCalled();
  });
});

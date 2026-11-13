import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { copyToClipboard } from './clipboard';

describe('copyToClipboard focus handling', () => {
  let trigger: HTMLButtonElement;
  const originalClipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard');
  // Browsers focus a textarea in select(); jsdom does not, so without this the
  // focus loss the fallback causes would not show up here.
  const originalSelect = HTMLTextAreaElement.prototype.select;

  beforeEach(() => {
    trigger = document.createElement('button');
    document.body.append(trigger);
    trigger.focus();
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true });
    HTMLTextAreaElement.prototype.select = function select(this: HTMLTextAreaElement) {
      this.focus();
    };
  });

  afterEach(() => {
    trigger.remove();
    HTMLTextAreaElement.prototype.select = originalSelect;
    if (originalClipboard) Object.defineProperty(navigator, 'clipboard', originalClipboard);
    else Reflect.deleteProperty(navigator, 'clipboard');
    Reflect.deleteProperty(document, 'execCommand');
    vi.restoreAllMocks();
  });

  it('returns focus to the button that had it after the textarea fallback copies', async () => {
    document.execCommand = vi.fn(() => true);

    expect(await copyToClipboard('text')).toBe(true);

    expect(trigger).toHaveFocus();
    expect(document.querySelector('textarea')).toBeNull();
  });

  it('returns focus when the fallback copy throws', async () => {
    document.execCommand = vi.fn(() => {
      throw new Error('blocked');
    });

    expect(await copyToClipboard('text')).toBe(false);

    expect(trigger).toHaveFocus();
    expect(document.querySelector('textarea')).toBeNull();
  });

  it('does not focus anything when nothing had focus', async () => {
    trigger.blur();
    document.execCommand = vi.fn(() => true);

    await copyToClipboard('text');

    expect(document.body).toHaveFocus();
  });

  it('leaves focus alone when the Clipboard API succeeds', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    const focusSpy = vi.spyOn(trigger, 'focus');

    expect(await copyToClipboard('text')).toBe(true);

    expect(writeText).toHaveBeenCalledWith('text');
    expect(focusSpy).not.toHaveBeenCalled();
    expect(trigger).toHaveFocus();
  });
});

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

  it.each([
    ['copies', true, () => true],
    [
      'throws',
      false,
      () => {
        throw new Error('blocked');
      },
    ],
  ])(
    'returns focus to the button that had it when the textarea fallback %s',
    async (_name, copied, exec) => {
      document.execCommand = vi.fn(exec);

      expect(await copyToClipboard('text')).toBe(copied);

      expect(trigger).toHaveFocus();
      expect(document.querySelector('textarea')).toBeNull();
    }
  );

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

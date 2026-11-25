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

  it('returns focus inside a popup document passed as targetDoc', async () => {
    const frame = document.createElement('iframe');
    document.body.append(frame);
    try {
      const popup = frame.contentDocument;
      if (!popup?.defaultView) throw new Error('iframe document has no window');
      popup.defaultView.HTMLTextAreaElement.prototype.select = function select(
        this: HTMLTextAreaElement
      ) {
        this.focus();
      };
      const popupButton = popup.createElement('button');
      popup.body.append(popupButton);
      popupButton.focus();
      popup.execCommand = vi.fn(() => true);

      expect(await copyToClipboard('text', popup)).toBe(true);

      expect(popup.activeElement).toBe(popupButton);
    } finally {
      frame.remove();
    }
  });

  it('falls back to the textarea and returns focus when the Clipboard API rejects', async () => {
    const writeText = vi.fn().mockRejectedValue(new Error('denied'));
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    document.execCommand = vi.fn(() => true);

    expect(await copyToClipboard('text')).toBe(true);

    expect(writeText).toHaveBeenCalledWith('text');
    expect(document.execCommand).toHaveBeenCalledWith('copy');
    expect(trigger).toHaveFocus();
    expect(document.querySelector('textarea')).toBeNull();
  });

  it('still returns focus when the textarea was already removed from the page', async () => {
    document.execCommand = vi.fn(() => {
      document.querySelector('textarea')?.remove();
      return true;
    });

    expect(await copyToClipboard('text')).toBe(true);

    expect(trigger).toHaveFocus();
  });

  it('does not refocus a previously focused element that left the page', async () => {
    const focusSpy = vi.spyOn(trigger, 'focus');
    document.execCommand = vi.fn(() => {
      trigger.remove();
      return true;
    });

    expect(await copyToClipboard('text')).toBe(true);

    // Only the select() stub in beforeEach focuses elements; the trigger is not one of them
    expect(focusSpy).not.toHaveBeenCalled();
  });

  it.each([
    ['body', () => document.body],
    ['the document element', () => document.documentElement],
  ])('does not refocus %s', async (_name, getElement) => {
    const element = getElement();
    trigger.blur();
    const activeSpy = vi.spyOn(document, 'activeElement', 'get').mockReturnValue(element);
    const focusSpy = vi.spyOn(element, 'focus');
    document.execCommand = vi.fn(() => true);

    try {
      expect(await copyToClipboard('text')).toBe(true);
    } finally {
      activeSpy.mockRestore();
    }

    expect(focusSpy).not.toHaveBeenCalled();
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

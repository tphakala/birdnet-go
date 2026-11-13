import { describe, it, expect, afterEach, vi } from 'vitest';
import { fireEvent } from '@testing-library/svelte';
import { radioGroupKeys } from './radioGroupKeys';

/** Builds a radiogroup of buttons whose own click handler moves aria-checked, like a real consumer. */
function buildGroup(labels: string[], ariaDisabled: string[] = []) {
  const group = document.createElement('div');
  group.setAttribute('role', 'radiogroup');
  const radios = labels.map(label => {
    const radio = document.createElement('button');
    radio.type = 'button';
    radio.setAttribute('role', 'radio');
    radio.setAttribute('aria-checked', 'false');
    radio.textContent = label;
    if (ariaDisabled.includes(label)) radio.setAttribute('aria-disabled', 'true');
    radio.addEventListener('click', () => {
      if (radio.getAttribute('aria-disabled') === 'true') return;
      radios.forEach(r => r.setAttribute('aria-checked', String(r === radio)));
    });
    group.appendChild(radio);
    return radio;
  });
  radios[0].setAttribute('aria-checked', 'true');
  document.body.appendChild(group);
  const action = radioGroupKeys(group);
  return { group, radios, destroy: () => action?.destroy?.() };
}

const checkedLabel = (radios: HTMLElement[]) =>
  radios.find(r => r.getAttribute('aria-checked') === 'true')?.textContent;

describe('radioGroupKeys', () => {
  let cleanup: (() => void) | undefined;

  afterEach(() => {
    cleanup?.();
    cleanup = undefined;
    document.body.replaceChildren();
  });

  function setup(labels: string[], ariaDisabled: string[] = []) {
    const built = buildGroup(labels, ariaDisabled);
    cleanup = built.destroy;
    return built;
  }

  it('moves selection and focus to the next radio on ArrowRight and ArrowDown', async () => {
    const { radios } = setup(['a', 'b', 'c']);
    radios[0].focus();

    await fireEvent.keyDown(radios[0], { key: 'ArrowRight' });
    expect(checkedLabel(radios)).toBe('b');
    expect(document.activeElement).toBe(radios[1]);

    await fireEvent.keyDown(radios[1], { key: 'ArrowDown' });
    expect(checkedLabel(radios)).toBe('c');
    expect(document.activeElement).toBe(radios[2]);
  });

  it('moves to the previous radio on ArrowLeft and ArrowUp', async () => {
    const { radios } = setup(['a', 'b', 'c']);
    radios[2].focus();

    await fireEvent.keyDown(radios[2], { key: 'ArrowLeft' });
    expect(document.activeElement).toBe(radios[1]);
    expect(checkedLabel(radios)).toBe('b');

    await fireEvent.keyDown(radios[1], { key: 'ArrowUp' });
    expect(document.activeElement).toBe(radios[0]);
  });

  it('wraps at both ends', async () => {
    const { radios } = setup(['a', 'b', 'c']);
    radios[2].focus();
    await fireEvent.keyDown(radios[2], { key: 'ArrowRight' });
    expect(checkedLabel(radios)).toBe('a');
    expect(document.activeElement).toBe(radios[0]);

    await fireEvent.keyDown(radios[0], { key: 'ArrowLeft' });
    expect(checkedLabel(radios)).toBe('c');
    expect(document.activeElement).toBe(radios[2]);
  });

  it('skips an aria-disabled radio', async () => {
    const { radios } = setup(['a', 'b', 'c'], ['b']);
    radios[0].focus();
    await fireEvent.keyDown(radios[0], { key: 'ArrowRight' });
    expect(checkedLabel(radios)).toBe('c');
    expect(document.activeElement).toBe(radios[2]);
  });

  it('skips a natively disabled radio', async () => {
    const { radios } = setup(['a', 'b', 'c']);
    radios[1].disabled = true;
    radios[0].focus();
    await fireEvent.keyDown(radios[0], { key: 'ArrowRight' });
    expect(checkedLabel(radios)).toBe('c');
    expect(document.activeElement).toBe(radios[2]);
  });

  it('skips a radio that is hidden or inert, or sits inside such an element', async () => {
    const { radios } = setup(['a', 'b', 'c', 'd', 'e']);
    radios[1].hidden = true;
    radios[2].setAttribute('inert', '');
    const wrapper = document.createElement('span');
    wrapper.hidden = true;
    radios[3].replaceWith(wrapper);
    wrapper.appendChild(radios[3]);
    radios[0].focus();

    await fireEvent.keyDown(radios[0], { key: 'ArrowRight' });

    expect(checkedLabel(radios)).toBe('e');
    expect(document.activeElement).toBe(radios[4]);
  });

  it('skips a radio that cannot take focus, such as one hidden by CSS', async () => {
    const { radios } = setup(['a', 'b', 'c']);
    // A radio hidden by display:none ignores focus() in a browser; jsdom has no layout, so mimic it.
    radios[1].focus = () => {};
    radios[0].focus();

    await fireEvent.keyDown(radios[0], { key: 'ArrowRight' });

    expect(checkedLabel(radios)).toBe('c');
    expect(document.activeElement).toBe(radios[2]);
  });

  it('selects a focused radio even when the window lacks system focus and :focus does not match', async () => {
    const { radios } = setup(['a', 'b', 'c']);
    // An unfocused window or iframe keeps the focused element as activeElement but never matches :focus.
    const realMatches = Element.prototype.matches;
    const matches = vi.spyOn(Element.prototype, 'matches').mockImplementation(function (
      this: Element,
      selector: string
    ) {
      return selector === ':focus' ? false : realMatches.call(this, selector);
    });
    radios[0].focus();

    try {
      await fireEvent.keyDown(radios[0], { key: 'ArrowRight' });
    } finally {
      matches.mockRestore();
    }

    expect(checkedLabel(radios)).toBe('b');
    expect(document.activeElement).toBe(radios[1]);
  });

  it('leaves selection and focus alone when every other radio is aria-disabled', async () => {
    const { radios } = setup(['a', 'b', 'c'], ['b', 'c']);
    radios[0].focus();
    await fireEvent.keyDown(radios[0], { key: 'ArrowRight' });
    expect(checkedLabel(radios)).toBe('a');
    expect(document.activeElement).toBe(radios[0]);
  });

  it('ignores an arrow pressed with Alt, Ctrl or Meta so browser shortcuts keep working', async () => {
    const { radios } = setup(['a', 'b', 'c']);
    radios[0].focus();
    await fireEvent.keyDown(radios[0], { key: 'ArrowLeft', altKey: true });
    await fireEvent.keyDown(radios[0], { key: 'ArrowRight', ctrlKey: true });
    await fireEvent.keyDown(radios[0], { key: 'ArrowRight', metaKey: true });
    expect(checkedLabel(radios)).toBe('a');
    expect(document.activeElement).toBe(radios[0]);
  });

  it('prevents the default scroll of a handled arrow key only', async () => {
    const { radios } = setup(['a', 'b']);
    radios[0].focus();
    const handled = new KeyboardEvent('keydown', {
      key: 'ArrowRight',
      bubbles: true,
      cancelable: true,
    });
    radios[0].dispatchEvent(handled);
    expect(handled.defaultPrevented).toBe(true);

    const other = new KeyboardEvent('keydown', { key: 'a', bubbles: true, cancelable: true });
    radios[1].dispatchEvent(other);
    expect(other.defaultPrevented).toBe(false);
  });

  it('stops handling keys after destroy', async () => {
    const { radios, destroy } = setup(['a', 'b']);
    destroy();
    radios[0].focus();
    await fireEvent.keyDown(radios[0], { key: 'ArrowRight' });
    expect(checkedLabel(radios)).toBe('a');
  });
});

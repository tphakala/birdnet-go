import { describe, it, expect, vi, afterEach } from 'vitest';
import { fireEvent, render } from '@testing-library/svelte';
import { tick } from 'svelte';
import { renderTyped, screen } from '../../../../test/render-helpers';
import { expectNoA11yViolations } from '$lib/utils/axe-utils';
import ExpandableText from './ExpandableText.svelte';

const LONG_TEXT = 'A long description that does not fit in three lines on a narrow card.';
const SHORT_TEXT = 'Short.';
const CLAMPED_HEIGHT = 50;
const FULL_HEIGHT = 84;

/**
 * jsdom has no layout, so scrollHeight and clientHeight are 0. Stub them so a paragraph
 * holding `clippedText` reports more content than it shows; any other text fits.
 */
function stubLayout(clippedText: string): void {
  vi.spyOn(Element.prototype, 'clientHeight', 'get').mockReturnValue(CLAMPED_HEIGHT);
  vi.spyOn(Element.prototype, 'scrollHeight', 'get').mockImplementation(function (this: Element) {
    return this.textContent === clippedText ? FULL_HEIGHT : CLAMPED_HEIGHT;
  });
}

function paragraphOf(text: string): HTMLElement {
  const p = screen.getByText(text);
  if (p.tagName !== 'P') throw new Error(`"${text}" is not in a paragraph`);
  return p;
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('ExpandableText', () => {
  it('renders no toggle when the text fits', () => {
    stubLayout(LONG_TEXT);
    renderTyped(ExpandableText, { props: { text: SHORT_TEXT } });

    expect(screen.getByText(SHORT_TEXT)).toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('toggles the clamp on clipped text and keeps the button state in step', async () => {
    stubLayout(LONG_TEXT);
    renderTyped(ExpandableText, { props: { text: LONG_TEXT, lines: 3 } });

    const paragraph = paragraphOf(LONG_TEXT);
    const toggle = screen.getByRole('button', { name: 'common.ui.showMore' });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(toggle).toHaveAttribute('aria-controls', paragraph.id);
    expect(paragraph).toHaveClass('line-clamp-3');

    await fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(toggle).toHaveAccessibleName('common.ui.showLess');
    expect(paragraph).not.toHaveClass('line-clamp-3');

    await fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(toggle).toHaveAccessibleName('common.ui.showMore');
    expect(paragraph).toHaveClass('line-clamp-3');
  });

  it('names the toggle after its subject, visible label first', () => {
    stubLayout(LONG_TEXT);
    renderTyped(ExpandableText, { props: { text: LONG_TEXT, subject: 'BSG-BAT' } });

    expect(screen.getByRole('button')).toHaveAccessibleName('common.ui.showMore BSG-BAT');
  });

  it('drops a collapsed toggle when the text changes to one that fits', async () => {
    stubLayout(LONG_TEXT);
    const { rerender } = render(ExpandableText, { props: { text: LONG_TEXT } });
    expect(screen.getByRole('button')).toBeInTheDocument();

    await rerender({ text: SHORT_TEXT });
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('drops an expanded toggle when the text changes to one that fits', async () => {
    stubLayout(LONG_TEXT);
    const { rerender } = render(ExpandableText, { props: { text: LONG_TEXT } });
    await fireEvent.click(screen.getByRole('button', { name: 'common.ui.showMore' }));

    await rerender({ text: SHORT_TEXT });
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
    expect(paragraphOf(SHORT_TEXT)).toHaveClass('line-clamp-3');
  });
});

describe('ExpandableText measurement', () => {
  afterEach(() => {
    Reflect.deleteProperty(document, 'fonts');
  });

  it('re-measures once web fonts have loaded', async () => {
    // jsdom has no FontFaceSet; an EventTarget stands in for document.fonts
    const fonts = new EventTarget();
    Object.defineProperty(document, 'fonts', { value: fonts, configurable: true });
    let clipped = false;
    vi.spyOn(Element.prototype, 'clientHeight', 'get').mockReturnValue(CLAMPED_HEIGHT);
    vi.spyOn(Element.prototype, 'scrollHeight', 'get').mockImplementation(() =>
      clipped ? FULL_HEIGHT : CLAMPED_HEIGHT
    );
    renderTyped(ExpandableText, { props: { text: LONG_TEXT } });
    expect(screen.queryByRole('button')).not.toBeInTheDocument();

    // The web font is wider than the fallback: same box, one more line
    clipped = true;
    fonts.dispatchEvent(new Event('loadingdone'));
    await tick();

    expect(screen.getByRole('button', { name: 'common.ui.showMore' })).toBeInTheDocument();
  });
});

describe('ExpandableText focus', () => {
  it('keeps focus in the text when Show less leaves nothing to collapse', async () => {
    // Clipped at first; the card then widens while expanded, so the clamped text fits
    let fits = false;
    vi.spyOn(Element.prototype, 'clientHeight', 'get').mockReturnValue(CLAMPED_HEIGHT);
    vi.spyOn(Element.prototype, 'scrollHeight', 'get').mockImplementation(() =>
      fits ? CLAMPED_HEIGHT : FULL_HEIGHT
    );
    renderTyped(ExpandableText, { props: { text: LONG_TEXT } });

    const toggle = screen.getByRole('button', { name: 'common.ui.showMore' });
    toggle.focus();
    await fireEvent.click(toggle);
    fits = true;
    await fireEvent.click(screen.getByRole('button', { name: 'common.ui.showLess' }));

    expect(screen.queryByRole('button')).not.toBeInTheDocument();
    expect(document.activeElement).toBe(paragraphOf(LONG_TEXT));
  });

  it('keeps focus in the text when an expanded text that fits is collapsed', async () => {
    // A is expanded, B fits (measured), then A returns expanded with nothing to clip
    stubLayout(LONG_TEXT);
    const { rerender } = render(ExpandableText, { props: { text: LONG_TEXT } });
    await fireEvent.click(screen.getByRole('button', { name: 'common.ui.showMore' }));
    await rerender({ text: SHORT_TEXT });
    vi.spyOn(Element.prototype, 'scrollHeight', 'get').mockReturnValue(CLAMPED_HEIGHT);
    await rerender({ text: LONG_TEXT });

    const showLess = screen.getByRole('button', { name: 'common.ui.showLess' });
    showLess.focus();
    await fireEvent.click(showLess);

    expect(screen.queryByRole('button')).not.toBeInTheDocument();
    expect(document.activeElement).toBe(paragraphOf(LONG_TEXT));
  });
  it('keeps focus in the text when the text changes under a focused toggle', async () => {
    // A is expanded, B fits (measured), A returns expanded with nothing clipped, then B again
    stubLayout(LONG_TEXT);
    const { rerender } = render(ExpandableText, { props: { text: LONG_TEXT } });
    await fireEvent.click(screen.getByRole('button', { name: 'common.ui.showMore' }));
    await rerender({ text: SHORT_TEXT });
    vi.spyOn(Element.prototype, 'scrollHeight', 'get').mockReturnValue(CLAMPED_HEIGHT);
    await rerender({ text: LONG_TEXT });
    screen.getByRole('button', { name: 'common.ui.showLess' }).focus();

    await rerender({ text: SHORT_TEXT });

    expect(screen.queryByRole('button')).not.toBeInTheDocument();
    expect(document.activeElement).toBe(paragraphOf(SHORT_TEXT));
  });
});

describe('ExpandableText Accessibility', () => {
  it('has no violations collapsed or expanded', async () => {
    stubLayout(LONG_TEXT);
    const { container } = renderTyped(ExpandableText, {
      props: { text: LONG_TEXT, subject: 'BSG-BAT' },
    });
    await expect(expectNoA11yViolations(container)).resolves.toBeUndefined();

    await fireEvent.click(screen.getByRole('button'));
    await expect(expectNoA11yViolations(container)).resolves.toBeUndefined();
  });
});

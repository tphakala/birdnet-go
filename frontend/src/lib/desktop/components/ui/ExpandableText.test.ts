import { describe, it, expect, vi, afterEach } from 'vitest';
import { fireEvent, render } from '@testing-library/svelte';
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

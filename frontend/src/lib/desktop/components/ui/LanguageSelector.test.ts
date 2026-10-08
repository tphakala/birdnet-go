import { describe, it, expect } from 'vitest';
import { screen } from '@testing-library/svelte';
import { renderTyped } from '../../../../test/render-helpers';
import LanguageSelector from './LanguageSelector.svelte';

describe('LanguageSelector Accessibility', () => {
  it('describes the trigger with the aria-describedby prop', () => {
    renderTyped(LanguageSelector, {
      props: { id: 'language-field', 'aria-describedby': 'language-help' },
    });

    expect(document.getElementById('language-field')).toHaveAttribute(
      'aria-describedby',
      'language-field-value language-help'
    );
    expect(screen.getAllByRole('button')).toHaveLength(1);
  });

  it('describes the trigger only by the selected language without the prop', () => {
    renderTyped(LanguageSelector, { props: { id: 'language-field' } });

    expect(document.getElementById('language-field')).toHaveAttribute(
      'aria-describedby',
      'language-field-value'
    );
  });

  it('names the trigger from the aria-label prop instead of the selected language', () => {
    renderTyped(LanguageSelector, { props: { 'aria-label': 'Interface language' } });

    expect(screen.getByRole('button', { name: 'Interface language' })).toBeInTheDocument();
  });
});

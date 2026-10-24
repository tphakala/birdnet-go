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
      'language-help'
    );
    expect(screen.getAllByRole('combobox')).toHaveLength(1);
  });

  it('has no description without the prop, since the selected language is the combobox value', () => {
    renderTyped(LanguageSelector, { props: { id: 'language-field' } });

    expect(document.getElementById('language-field')).not.toHaveAttribute('aria-describedby');
  });

  it('names the trigger from the aria-label prop instead of the selected language', () => {
    renderTyped(LanguageSelector, { props: { 'aria-label': 'Interface language' } });

    expect(screen.getByRole('combobox', { name: 'Interface language' })).toBeInTheDocument();
  });
});

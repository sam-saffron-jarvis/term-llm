import { getByRole } from '@testing-library/dom';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { clearPassword, el, field, input, select } from './dom';

describe('proxy form autofill protection', () => {
  it.each([
    ['text input', () => input()],
    ['URL input', () => input('', 'url')],
    ['secret input', () => input('', 'password')],
    ['textarea', () => el('textarea')],
    ['select', () => select([['secret', 'Stored secret']])],
  ] as const)('opts %s out of browser and password-manager autofill', (_name, create) => {
    const control = create();
    expect(control).toHaveAttribute('autocomplete', 'off');
    expect(control).toHaveAttribute('data-1p-ignore', 'true');
    expect(control).toHaveAttribute('data-bwignore', 'true');
    expect(control).toHaveAttribute('data-form-type', 'other');
    expect(control).toHaveAttribute('data-lpignore', 'true');
    expect(control).toHaveAttribute('data-protonpass-ignore', 'true');
  });
});

it('keeps credentials masked until explicitly toggled and never submits from the toggle', async () => {
  const user = userEvent.setup();
  const credential = input('', 'password');
  const form = el('form', field('Administrator token', credential, 'Test hint'));
  const submit = vi.fn((event: Event) => event.preventDefault());
  form.addEventListener('submit', submit);
  document.body.append(form);
  try {
    await user.type(credential, 'demo-only-value');
    expect(credential).toHaveAttribute('type', 'password');
    const toggle = getByRole(form, 'button', { name: 'Show administrator token' });
    expect(toggle).toHaveAttribute('aria-controls', credential.id);
    expect(credential).toHaveAccessibleName('Administrator token');
    expect(credential).toHaveAccessibleDescription('Test hint');
    await user.tab();
    expect(toggle).toHaveFocus();
    await user.keyboard('{Enter}');
    expect(toggle).toHaveAccessibleName('Hide administrator token');
    expect(credential).toHaveAttribute('type', 'text');
    expect(credential).toHaveValue('demo-only-value');
    expect(credential).toHaveAttribute('autocomplete', 'off');
    expect(credential).toHaveAttribute('spellcheck', 'false');
    expect(credential).toHaveAttribute('autocorrect', 'off');
    expect(credential).toHaveAttribute('autocapitalize', 'off');
    expect(toggle).toHaveFocus();
    await user.keyboard(' ');
    expect(credential).toHaveAttribute('type', 'password');
    expect(credential).toHaveValue('demo-only-value');
    await user.click(toggle);
    expect(credential).toHaveAttribute('type', 'text');
    expect(submit).not.toHaveBeenCalled();
    clearPassword(credential);
    expect(credential).toHaveValue('');
    expect(credential).toHaveAttribute('type', 'password');
    expect(toggle).toHaveAccessibleName('Show administrator token');
  } finally {
    form.remove();
  }
});

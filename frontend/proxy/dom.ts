import type { ProxyState } from '../src/api/proxy-client';
import { passwordManagerIgnoreProps } from '../src/components/input-attributes';

export type Child = Node | string;
export function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  ...children: Child[]
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (tag === 'input' || tag === 'textarea' || tag === 'select') {
    for (const [name, value] of Object.entries(passwordManagerIgnoreProps))
      node.setAttribute(name.toLowerCase(), value);
  }
  node.append(...children);
  return node;
}
export function titledValue(value: { text: string; title: string }): HTMLSpanElement {
  const node = el('span', value.text);
  node.title = value.title;
  node.setAttribute('aria-label', value.title);
  return node;
}
export function button(text: string, action?: () => void): HTMLButtonElement {
  const node = el('button', text);
  node.type = 'button';
  if (action) node.addEventListener('click', action);
  return node;
}
export function input(value = '', type = 'text'): HTMLInputElement {
  const node = el('input');
  node.type = type;
  node.value = value;
  return node;
}
const passwordConcealers = new WeakMap<HTMLInputElement, () => void>();
export function clearPassword(input: HTMLInputElement): void {
  input.value = '';
  passwordConcealers.get(input)?.();
}
function passwordControl(input: HTMLInputElement, label: string): HTMLElement {
  input.setAttribute('spellcheck', 'false');
  input.setAttribute('autocorrect', 'off');
  input.setAttribute('autocapitalize', 'off');
  const ns = 'http://www.w3.org/2000/svg';
  const icon = document.createElementNS(ns, 'svg');
  icon.setAttribute('viewBox', '0 0 24 24');
  icon.setAttribute('aria-hidden', 'true');
  icon.setAttribute('fill', 'none');
  icon.setAttribute('stroke', 'currentColor');
  icon.setAttribute('stroke-width', '1.7');
  icon.setAttribute('stroke-linecap', 'round');
  icon.setAttribute('stroke-linejoin', 'round');
  const shape = document.createElementNS(ns, 'path');
  icon.append(shape);
  const toggle = button('', () => setVisible(input.type === 'password'));
  toggle.setAttribute('aria-controls', input.id);
  toggle.append(icon);
  function setVisible(visible: boolean) {
    // Keep the original input, including its value and native form behavior.
    input.type = visible ? 'text' : 'password';
    const action = `${visible ? 'Hide' : 'Show'} ${label.toLowerCase()}`;
    toggle.setAttribute('aria-label', action);
    toggle.title = action;
    shape.setAttribute(
      'd',
      visible
        ? 'M2 10c2.6 3.3 6 5 10 5s7.4-1.7 10-5M5.6 13l-2 2.8M12 15v3.5M18.4 13l2 2.8'
        : 'M2 12s4-6 10-6 10 6 10 6-4 6-10 6S2 12 2 12Zm10-2.6a2.6 2.6 0 1 0 0 5.2 2.6 2.6 0 0 0 0-5.2Z',
    );
  }
  setVisible(false);
  passwordConcealers.set(input, () => setVisible(false));
  const wrapper = el('div', input, toggle);
  wrapper.className = 'password-control';
  return wrapper;
}
let fieldSequence = 0;
export function field(label: string, control: HTMLElement, hint = ''): HTMLDivElement {
  const id = control.id || `proxy-field-${++fieldSequence}`;
  control.id = id;
  const caption = el('label', label);
  caption.htmlFor = id;
  const body =
    control instanceof HTMLInputElement && control.type === 'password'
      ? passwordControl(control, label)
      : control;
  const wrapper = el('div', caption, body);
  wrapper.className = 'field';
  if (hint) {
    const description = el('small', hint);
    description.id = `${id}-hint`;
    control.setAttribute('aria-describedby', description.id);
    wrapper.append(description);
  }
  return wrapper;
}
export function select(values: [string, string][], value = ''): HTMLSelectElement {
  const node = el('select');
  for (const [key, text] of values) {
    const option = el('option', text);
    option.value = key;
    node.append(option);
  }
  node.value = value;
  return node;
}
export function check(label: string, checked: boolean): [HTMLLabelElement, HTMLInputElement] {
  const control = input('', 'checkbox');
  control.checked = checked;
  const node = el('label', control, label);
  node.className = 'check';
  return [node, control];
}
export function badge(text: string, kind = ''): HTMLSpanElement {
  const node = el('span', text);
  node.className = `badge ${kind}`;
  return node;
}
export function routeBadges(enabled: boolean, ...badges: HTMLSpanElement[]): HTMLDivElement {
  const node = el(
    'div',
    badge(enabled ? 'Enabled' : 'Disabled', enabled ? 'enabled' : 'muted'),
    ...badges,
  );
  node.className = 'route-badges';
  return node;
}
export function actions(...children: Child[]): HTMLElement {
  const node = el('div', ...children);
  node.className = 'actions';
  return node;
}
export function card(title: string, ...children: Child[]): HTMLElement {
  const heading = el('h2', title);
  heading.tabIndex = -1;
  return el('section', heading, ...children);
}
export function download(name: string, content: string, type: string): void {
  const url = URL.createObjectURL(new Blob([content], { type }));
  const link = el('a');
  link.href = url;
  link.download = name;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
export interface UIContext {
  run: (action: () => Promise<unknown>, source?: HTMLElement) => Promise<boolean>;
  refresh: (focusKey?: string, preserveView?: boolean) => Promise<ProxyState | undefined>;
  announce: (message: string) => void;
  navigate: (
    tab: 'Home' | 'Routes' | 'Secrets' | 'Clients' | 'Requests' | 'Debug' | 'Passkeys' | 'Admin',
  ) => void;
  protectToken: (shown: boolean, pending?: boolean) => void;
  /** A one-time client token is being issued or is still on screen. */
  hasProtectedToken?: () => boolean;
}
export function submit(
  form: HTMLFormElement,
  context: UIContext,
  action: () => Promise<void>,
  label = 'Save',
): HTMLButtonElement {
  form.addEventListener('submit', (event) => {
    event.preventDefault();
    void context.run(action, form);
  });
  const save = button(label);
  save.type = 'submit';
  save.className = 'primary';
  form.append(save);
  return save;
}
export function confirmAction(
  text: string,
  action: () => Promise<void>,
  context: UIContext,
  consequence = `${text}?`,
) {
  const node = button(text, () => {
    if (window.confirm(consequence)) void context.run(action, node);
  });
  node.className = 'danger';
  return node;
}
export function dateTime(value: string | number | undefined, empty = 'Never'): string {
  if (!value) return empty;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? 'Unknown' : date.toLocaleString();
}
export function disclosure(title: string, ...children: Child[]): HTMLDetailsElement {
  return el('details', el('summary', title), ...children);
}
export function keyedCard(key: string, title: string, ...children: Child[]): HTMLElement {
  const node = card(title, ...children);
  node.dataset.focus = key;
  return node;
}
export function shellQuote(value: string): string {
  return `'${value.replaceAll("'", "'\\''")}'`;
}

export type Child = Node | string;
export function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  ...children: Child[]
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  node.append(...children);
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
let fieldSequence = 0;
export function field(label: string, control: HTMLElement, hint = ''): HTMLDivElement {
  const id = control.id || `proxy-field-${++fieldSequence}`;
  control.id = id;
  const caption = el('label', label);
  caption.htmlFor = id;
  const wrapper = el('div', caption, control);
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
  run: (action: () => Promise<void>, source?: HTMLElement) => Promise<boolean>;
  refresh: (focusKey?: string) => Promise<void>;
  announce: (message: string) => void;
  navigate: (
    tab: 'Routes' | 'Secrets' | 'Callers' | 'Requests' | 'Passkeys',
    origin?: string,
  ) => void;
  protectToken: (shown: boolean, pending?: boolean) => void;
}
export function submit(
  form: HTMLFormElement,
  context: UIContext,
  action: () => Promise<void>,
  label = 'Save',
): void {
  form.addEventListener('submit', (event) => {
    event.preventDefault();
    void context.run(action, form);
  });
  const save = button(label);
  save.type = 'submit';
  save.className = 'primary';
  form.append(save);
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
export function requireOrigin(value: string): string {
  const url = new URL(value);
  if (
    !['https:', 'http:'].includes(url.protocol) ||
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    url.pathname !== '/'
  )
    throw new Error(
      'Use an origin only, such as https://api.example.com, without a path or credentials.',
    );
  return url.origin;
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
export function sameOrigin(a: string, b: string): boolean {
  try {
    return new URL(a).origin === new URL(b).origin;
  } catch {
    return false;
  }
}

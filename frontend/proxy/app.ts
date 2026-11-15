import { ProxyAPIError, ProxyClient, type ProxyState } from '../src/api/proxy-client';
import { actions, button, card, el, field, input, submit, type UIContext } from './dom';
import { routesView } from './routes';
import { callersView, secretsView } from './security';
import { requestsView } from './requests';

type Tab = 'Routes' | 'Secrets' | 'Callers' | 'Requests';
export function mountProxy(root: HTMLElement, api = new ProxyClient()): () => void {
  let state: ProxyState | undefined;
  let tab: Tab = 'Routes';
  let secretOrigin: string | undefined;
  let tokenProtected = false;
  let tokenPending = false;
  let disposed = false;
  let epoch = 0;
  const status = el('p');
  status.role = 'status';
  status.tabIndex = -1;
  status.setAttribute('aria-live', 'polite');
  const content = el('main');
  const mode = el('small');
  const tools = actions();
  const header = el('header', el('div', el('h1', 'Proxy'), mode), tools);
  root.replaceChildren(header, status, content);
  function confirmLeave(): boolean {
    if (tokenPending) {
      status.textContent = 'Creating a caller. Wait for its one-time token before leaving.';
      return false;
    }
    return (
      !tokenProtected ||
      window.confirm(
        'This caller token is shown once. Leave without saving it? You cannot retrieve it later.',
      )
    );
  }
  const beforeUnload = (event: BeforeUnloadEvent) => {
    if (tokenProtected) {
      event.preventDefault();
      event.returnValue = '';
    }
  };
  window.addEventListener('beforeunload', beforeUnload);
  const pending = new WeakSet<HTMLElement>();
  const context: UIContext = {
    announce(message) {
      status.textContent = message;
    },
    protectToken(shown, pending = false) {
      tokenProtected = shown;
      tokenPending = pending;
    },
    navigate(next, origin) {
      if (!confirmLeave()) return;
      tokenProtected = false;
      tab = next;
      secretOrigin = origin;
      status.textContent = '';
      render();
      focus();
    },
    async refresh(key) {
      const current = ++epoch;
      const snapshot = await api.state();
      if (disposed || current !== epoch) return;
      state = snapshot;
      tokenProtected = false;
      tokenPending = false;
      render();
      focus(key);
    },
    async run(action, source) {
      if (source && pending.has(source)) return false;
      status.textContent = '';
      const controls = source
        ? [
            ...source.querySelectorAll<
              HTMLInputElement | HTMLButtonElement | HTMLSelectElement | HTMLTextAreaElement
            >('input,button,select,textarea'),
          ]
        : [];
      if (source instanceof HTMLButtonElement) controls.push(source);
      const focused = document.activeElement;
      const enabled = controls.filter((c) => !c.disabled);
      if (source) {
        pending.add(source);
        source.setAttribute('aria-busy', 'true');
      }
      enabled.forEach((c) => {
        c.disabled = true;
      });
      try {
        await action();
        return true;
      } catch (error) {
        if (disposed) return false;
        if (error instanceof ProxyAPIError && error.status === 401) {
          ++epoch;
          state = undefined;
          tokenProtected = false;
          login();
        }
        status.textContent =
          error instanceof Error ? error.message : 'The action failed. Please try again.';
        if (!(error instanceof ProxyAPIError && error.status === 401)) status.focus();
        return false;
      } finally {
        enabled.forEach((c) => {
          c.disabled = false;
        });
        if (source) {
          pending.delete(source);
          source.removeAttribute('aria-busy');
          if (
            document.activeElement === document.body &&
            source.isConnected &&
            focused instanceof HTMLElement &&
            focused.isConnected
          )
            focused.focus();
        }
      }
    },
  };
  function focus(key?: string) {
    const row = key
      ? [...content.querySelectorAll<HTMLElement>('[data-focus]')].find(
          (node) => node.dataset.focus === key,
        )
      : undefined;
    (row?.querySelector('h2') ?? content.querySelector('h2'))?.focus();
  }
  function login() {
    tools.replaceChildren();
    mode.textContent = 'Administration';
    const token = input('', 'password');
    token.required = true;
    token.autocomplete = 'off';
    const form = el(
      'form',
      field(
        'Administrator token',
        token,
        'Paste the contents of --admin-token-file. Used only for this session; never saved in browser storage.',
      ),
    );
    submit(
      form,
      context,
      async () => {
        const credential = token.value;
        token.value = '';
        await api.login(credential);
        await context.refresh();
        context.announce('Signed in.');
      },
      'Sign in',
    );
    content.replaceChildren(card('Sign in', form));
    token.focus();
  }
  function render() {
    if (!state || disposed) return;
    const snapshot = state;
    mode.textContent = `${snapshot.mode === 'file' ? 'File-managed' : 'UI-managed'} · Revision ${snapshot.revision}`;
    const nav = el('nav');
    nav.setAttribute('aria-label', 'Proxy administration');
    for (const name of ['Routes', 'Secrets', 'Callers', 'Requests'] as const) {
      const item = button(name, () => context.navigate(name));
      if (name === tab) item.setAttribute('aria-current', 'page');
      nav.append(item);
    }
    const refresh = button('Refresh', () => {
      if (confirmLeave()) {
        secretOrigin = undefined;
        void context.run(() => context.refresh(), refresh);
      }
    });
    refresh.setAttribute('aria-label', 'Refresh state');
    const logout = button('Sign out', () => {
      if (!confirmLeave()) return;
      void context.run(async () => {
        await api.logout();
        ++epoch;
        state = undefined;
        tokenProtected = false;
        login();
        context.announce('Signed out.');
      }, logout);
    });
    tools.replaceChildren(refresh, logout);
    if (snapshot.mode === 'file') {
      const reload = button('Reload file', () => {
        if (
          !confirmLeave() ||
          !window.confirm(
            'Reload file configuration? Incompatible caller grants are dropped fail-closed. Dispatched requests may continue.',
          )
        )
          return;
        void context.run(async () => {
          await api.reload();
          await context.refresh();
          context.announce(
            'File configuration reloaded. Review caller grants; incompatible grants were dropped.',
          );
        }, reload);
      });
      tools.append(reload);
    }
    const origin = secretOrigin;
    secretOrigin = undefined;
    const views: Record<Tab, () => HTMLElement> = {
      Routes: () => routesView(snapshot, api, context),
      Secrets: () => secretsView(snapshot, api, context, origin),
      Callers: () => callersView(snapshot, api, context),
      Requests: () => requestsView(snapshot, api, context),
    };
    content.replaceChildren(nav, views[tab]());
  }
  content.append(el('p', 'Loading proxy state…'));
  void context.run(() => context.refresh());
  return () => {
    disposed = true;
    ++epoch;
    state = undefined;
    tokenProtected = false;
    window.removeEventListener('beforeunload', beforeUnload);
    root.replaceChildren();
  };
}

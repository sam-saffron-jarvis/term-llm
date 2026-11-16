import { browserPasskeyPlatform, type PasskeyPlatform } from '../src/hub/platform/passkeys';
import { passkeyLogin, passkeysView } from './passkeys';
import {
  ProxyAPIError,
  ProxyClient,
  type ProxyState,
  type ProxyAuthStatus,
} from '../src/api/proxy-client';
import { actions, button, card, el, field, input, submit, type UIContext } from './dom';
import { routesView } from './routes';
import { callersView, secretsView } from './security';
import { requestsView } from './requests';
import { homeView } from './home';
import { connectView, type ClaudeConnectView } from './connect';

type Tab = 'Home' | 'Routes' | 'Secrets' | 'Clients' | 'Requests' | 'Passkeys';
const tabs: Tab[] = ['Home', 'Routes', 'Secrets', 'Clients', 'Requests', 'Passkeys'];
function tabFromURL(): Tab {
  const value = new URL(window.location.href).searchParams.get('tab');
  // Preserve bookmarks from before the user-facing Clients rename.
  if (value === 'callers') return 'Clients';
  return tabs.find((name) => name.toLowerCase() === value) ?? 'Home';
}
export function mountProxy(
  root: HTMLElement,
  api = new ProxyClient(),
  platform: PasskeyPlatform = browserPasskeyPlatform(),
): () => void {
  let state: ProxyState | undefined;
  let authStatus: ProxyAuthStatus | undefined;
  let tab = tabFromURL();
  let connecting = new URL(window.location.href).searchParams.has('connect');
  let connector: ClaudeConnectView | undefined;
  function closeConnector() {
    connector?.dispose();
    connector = undefined;
  }
  const savedIndex: unknown = window.history.state?.proxyNavigationIndex;
  let historyIndex = typeof savedIndex === 'number' ? savedIndex : 0;
  let currentURL = window.location.href;
  function writeTab(push = false) {
    const url = new URL(window.location.href);
    if (!connecting && url.searchParams.has('connect')) {
      url.searchParams.delete('connect');
      url.searchParams.delete('code');
    }
    if (tab === 'Home') url.searchParams.delete('tab');
    else url.searchParams.set('tab', tab.toLowerCase());
    if (push) historyIndex++;
    window.history[push ? 'pushState' : 'replaceState'](
      { ...window.history.state, proxyNavigationIndex: historyIndex },
      '',
      url,
    );
    currentURL = url.href;
  }
  window.history.replaceState({ ...window.history.state, proxyNavigationIndex: historyIndex }, '');
  let tokenProtected = false;
  let tokenPending = false;
  let disposed = false;
  let epoch = 0;
  const status = el('p');
  status.role = 'status';
  status.tabIndex = -1;
  status.setAttribute('aria-live', 'polite');
  const content = el('main');
  const tools = actions();
  const brandName = el('span', 'Proxy');
  brandName.className = 'proxy-brand-accent';
  const brand = el('div', el('h1', 'term-llm ', brandName));
  brand.className = 'proxy-brand';
  const header = el('header', brand, tools);
  root.replaceChildren(header, status, content);
  function confirmLeave(): boolean {
    if (tokenPending) {
      status.textContent = 'Creating a client. Wait for its one-time token before leaving.';
      return false;
    }
    return (
      !tokenProtected ||
      window.confirm(
        'This client token is shown once. Leave without saving it? You cannot retrieve it later.',
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
  const popstate = (event: PopStateEvent) => {
    // A rejected traversal returns to the original entry, without creating another one.
    if (window.location.href === currentURL) return;
    const nextIndex: unknown = event.state?.proxyNavigationIndex;
    if (!confirmLeave()) {
      if (typeof nextIndex === 'number' && nextIndex !== historyIndex) {
        window.history.go(historyIndex - nextIndex);
      } else {
        window.history.replaceState(
          { ...window.history.state, proxyNavigationIndex: historyIndex },
          '',
          currentURL,
        );
      }
      return;
    }
    if (typeof nextIndex === 'number') historyIndex = nextIndex;
    closeConnector();
    connecting = new URL(window.location.href).searchParams.has('connect');
    tab = tabFromURL();
    currentURL = window.location.href;
    tokenProtected = false;
    status.textContent = '';
    render();
    focus();
  };
  window.addEventListener('popstate', popstate);
  const pending = new WeakSet<HTMLElement>();
  const context: UIContext = {
    announce(message) {
      status.dataset.quiet = String(message === 'Signed in.');
      status.textContent = message;
    },
    protectToken(shown, pending = false) {
      tokenProtected = shown;
      tokenPending = pending;
    },
    navigate(next) {
      if ((next === tab && !connecting) || !confirmLeave()) return;
      closeConnector();
      connecting = false;
      tokenProtected = false;
      tab = next;
      writeTab(true);
      status.textContent = '';
      render();
      focus();
    },
    async refresh(key, preserveView = false) {
      const current = ++epoch;
      const snapshot = await api.state();
      if (disposed || current !== epoch) return;
      state = snapshot;
      if (!preserveView) {
        tokenProtected = false;
        tokenPending = false;
        render();
        focus(key);
      }
      return snapshot;
    },
    async run(action, source) {
      status.dataset.quiet = 'false';
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
          tokenPending = false;
          authStatus = undefined;
          tools.replaceChildren();
          content.replaceChildren(el('p', 'Loading sign in…'));
          try {
            authStatus = await api.authStatus();
          } catch {
            // No fallback when authentication status is unavailable.
          }
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
    if (disposed) return;
    tools.replaceChildren();
    if (authStatus?.auth === 'passkey') {
      content.replaceChildren(
        passkeyLogin(
          authStatus,
          api,
          context,
          async () => {
            await context.refresh();
            context.announce('Signed in.');
          },
          async () => {
            authStatus = await api.authStatus();
            login();
          },
          platform,
        ),
      );
      focus();
      return;
    }
    if (authStatus?.auth !== 'token') {
      content.replaceChildren(
        card(
          'Sign in unavailable',
          button('Retry', () => {
            void context.run(start);
          }),
        ),
      );
      return;
    }
    const token = input('', 'password');
    token.required = true;
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
    const nav = el('nav');
    nav.setAttribute('aria-label', 'Proxy administration');
    const tabs: Tab[] = ['Home', 'Routes', 'Secrets', 'Clients', 'Requests'];
    if (snapshot.auth === 'passkey') tabs.push('Passkeys');
    if (!tabs.includes(tab)) tab = 'Home';
    writeTab();
    for (const name of tabs) {
      const item = button(name, () => context.navigate(name));
      if (name === tab && !connecting) item.setAttribute('aria-current', 'page');
      nav.append(item);
    }
    const refresh = button('Refresh', () => {
      if (confirmLeave()) {
        void context.run(() => context.refresh(), refresh);
      }
    });
    refresh.setAttribute('aria-label', 'Refresh state');
    const logout = button('Sign out', () => {
      if (!confirmLeave()) return;
      void context.run(async () => {
        await api.logout();
        closeConnector();
        ++epoch;
        state = undefined;
        tokenProtected = false;
        authStatus = undefined;
        tools.replaceChildren();
        try {
          authStatus = await api.authStatus();
        } finally {
          login();
        }
        context.announce('Signed out.');
      }, logout);
    });
    const connect = button('Connect Claude', () => {
      if (connecting || !confirmLeave()) return;
      connecting = true;
      status.textContent = '';
      render();
      focus();
    });
    if (connecting) connect.setAttribute('aria-current', 'page');
    tools.replaceChildren(connect, refresh, logout);
    if (snapshot.mode === 'file' && tab !== 'Home') {
      const reload = button('Reload file', () => {
        if (
          !confirmLeave() ||
          !window.confirm(
            'Reload file configuration? Incompatible client grants are dropped fail-closed. Dispatched requests may continue.',
          )
        )
          return;
        void context.run(async () => {
          await api.reload();
          await context.refresh();
          context.announce(
            'File configuration reloaded. Review client grants; incompatible grants were dropped.',
          );
        }, reload);
      });
      tools.append(reload);
    }
    const views: Record<Tab, () => HTMLElement> = {
      Home: () => homeView(snapshot, api, context),
      Routes: () => routesView(snapshot, api, context, platform),
      Secrets: () => secretsView(snapshot, api, context),
      Clients: () => callersView(snapshot, api, context),
      Requests: () => requestsView(snapshot, api, context),
      Passkeys: () => passkeysView(api, context, platform),
    };
    if (connecting) {
      if (!connector) {
        const query = new URL(window.location.href).searchParams;
        const id = query.get('connect');
        connector = connectView(snapshot, api, context, platform, {
          ...(id === null ? {} : { link: { id } }),
        });
      }
      content.replaceChildren(nav, connector.element);
      connector.resume();
    } else content.replaceChildren(nav, views[tab]());
  }
  content.append(el('p', 'Loading proxy state…'));
  async function start() {
    try {
      authStatus = await api.authStatus();
    } catch (error) {
      authStatus = undefined;
      login();
      throw error;
    }
    if (!disposed) await context.refresh();
  }
  void context.run(start);
  return () => {
    disposed = true;
    closeConnector();
    ++epoch;
    state = undefined;
    tokenProtected = false;
    window.removeEventListener('beforeunload', beforeUnload);
    window.removeEventListener('popstate', popstate);
    root.replaceChildren();
  };
}

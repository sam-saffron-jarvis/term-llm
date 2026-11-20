import { browserPasskeyPlatform, type PasskeyPlatform } from '../src/hub/platform/passkeys';
import { passkeyLogin, passkeysView } from './passkeys';
import {
  ProxyAPIError,
  ProxyClient,
  type ProxyState,
  type ProxyAuthStatus,
} from '../src/api/proxy-client';
import {
  actions,
  button,
  clearPassword,
  card,
  el,
  field,
  input,
  submit,
  type UIContext,
} from './dom';
import { routesView } from './routes';
import { adminView } from './admin';
import { callersView, secretsView } from './security';
import { requestsView } from './requests';
import { homeView } from './home';
import { timelineQueryKeys } from './timeline-prefs';
import { debugView } from './debug';

type Tab = 'Home' | 'Routes' | 'Secrets' | 'Clients' | 'Requests' | 'Debug' | 'Passkeys' | 'Admin';
const tabs: Tab[] = [
  'Home',
  'Routes',
  'Secrets',
  'Clients',
  'Requests',
  'Debug',
  'Passkeys',
  'Admin',
];
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
  let debug: ReturnType<typeof debugView> | undefined;
  let requests: ReturnType<typeof requestsView> | undefined;
  function closeView() {
    requests?.dispose();
    requests = undefined;
    debug?.dispose();
    debug = undefined;
  }
  const savedIndex: unknown = window.history.state?.proxyNavigationIndex;
  let historyIndex = typeof savedIndex === 'number' ? savedIndex : 0;
  let currentURL = window.location.href;
  function writeTab(push = false) {
    const url = new URL(window.location.href);
    if (tab === 'Home') url.searchParams.delete('tab');
    else {
      url.searchParams.set('tab', tab.toLowerCase());
      // Home restores its chart view from storage when navigated back to.
      for (const key of timelineQueryKeys) url.searchParams.delete(key);
    }
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
  function showStatus(message = '', tone: '' | 'quiet' | 'error' = '') {
    status.dataset.tone = tone;
    status.textContent = message;
  }
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
      showStatus('Creating a credential. Wait for its one-time token before leaving.');
      return false;
    }
    return (
      !tokenProtected ||
      window.confirm(
        'This token is shown once. Leave without saving it? You cannot retrieve it later.',
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
    tab = tabFromURL();
    currentURL = window.location.href;
    tokenProtected = false;
    showStatus();
    render();
    focus();
  };
  window.addEventListener('popstate', popstate);
  const pending = new WeakSet<HTMLElement>();
  const context: UIContext = {
    announce(message) {
      showStatus(message, message === 'Signed in.' ? 'quiet' : '');
    },
    hasProtectedToken() {
      return tokenProtected;
    },
    protectToken(shown, pending = false) {
      tokenProtected = shown;
      tokenPending = pending;
    },
    navigate(next) {
      if (next === tab || !confirmLeave()) return;
      tokenProtected = false;
      tab = next;
      writeTab(true);
      showStatus();
      render();
      focus();
    },
    async refresh(key, preserveView = false) {
      const current = ++epoch;
      const canDiscardToken = tokenProtected && !tokenPending;
      const snapshot = await api.state();
      if (disposed || current !== epoch) return;
      state = snapshot;
      // A read started before issuance is not permission to discard a new token.
      if (!preserveView && !tokenPending && (!tokenProtected || canDiscardToken)) {
        tokenProtected = false;
        tokenPending = false;
        render();
        focus(key);
      }
      return snapshot;
    },
    async run(action, source) {
      if (source && pending.has(source)) return false;
      showStatus();
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
          closeView();
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
        showStatus(
          error instanceof Error ? error.message : 'The action failed. Please try again.',
          'error',
        );
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
    (
      row?.querySelector('h2') ??
      (row?.hasAttribute('tabindex') ? row : content.querySelector('h2'))
    )?.focus();
  }
  function login() {
    if (disposed) return;
    closeView();
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
        clearPassword(token);
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
    closeView();
    const snapshot = state;
    const nav = el('nav');
    nav.setAttribute('aria-label', 'Proxy administration');
    const tabs: Tab[] = ['Home', 'Routes', 'Secrets', 'Clients', 'Requests', 'Debug'];
    if (snapshot.auth === 'passkey') tabs.push('Passkeys');
    tabs.push('Admin');
    if (!tabs.includes(tab)) tab = 'Home';
    writeTab();
    for (const name of tabs) {
      const item = button(name === 'Routes' ? 'Providers' : name, () => context.navigate(name));
      if (name === tab) item.setAttribute('aria-current', 'page');
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
        closeView();
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
    tools.replaceChildren(logout);
    if (tab !== 'Requests') tools.prepend(refresh);
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
      Home: () =>
        homeView(snapshot, api, context, () => {
          currentURL = window.location.href;
        }),
      Routes: () => routesView(snapshot, api, context),
      Secrets: () => secretsView(snapshot, api, context),
      Clients: () => callersView(snapshot, api, context),
      Requests: () => {
        requests = requestsView(snapshot, api, context);
        return requests.element;
      },
      Debug: () => {
        debug = debugView(api, context);
        return debug.element;
      },
      Passkeys: () => passkeysView(api, context, platform),
      Admin: () => adminView(snapshot, api, context),
    };
    content.replaceChildren(nav, views[tab]());
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
    closeView();
    ++epoch;
    state = undefined;
    tokenProtected = false;
    window.removeEventListener('beforeunload', beforeUnload);
    window.removeEventListener('popstate', popstate);
    root.replaceChildren();
  };
}

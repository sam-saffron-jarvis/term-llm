import {
  ProxyAPIError,
  type ProxyAccount,
  type ProxyClient,
  type ProxyPreset,
  type ProxyRoute,
  type ProxyState,
} from '../src/api/proxy-client';
import { accountControls, connectAccount } from './accounts';
import {
  nativeProvider,
  routeNativeProvider,
  routeSharedNativeAccount,
  type NativeProvider,
} from './native';
import { actions, badge, button, el, field, input, submit, type UIContext } from './dom';
import {
  providerFamilies,
  providerName,
  providerTitle,
  type ProviderGroup,
} from './provider-groups';
import { approvalCounts, providerDot } from './provider-list';
import { providerRouteAction } from './provider-onboarding';
import { providerAccess } from './provider-access';
import { providerConfigSection } from './provider-config';
import { providerTabs, type ProviderTab } from './provider-tabs';
import { routeForm } from './route-form';
import { missingCompanions } from './route-companions';
import { matchRoutePreset } from './route-presets';
import { routeSummary } from './route-summary';
import { routeNeedsSecret, routeRules } from './route-rules';

/** One open route editor per page; opening another closes the first. */
export interface RouteEditorSlot {
  current?: { owner: HTMLElement; form: HTMLElement };
  close(): void;
}

export interface ProviderDetailDeps {
  state: ProxyState;
  api: ProxyClient;
  context: UIContext;
  readonly: boolean;
  /** A context whose operations are serialized with the rest of the page. */
  componentContext: () => UIContext;
  blocked: () => boolean;
  editor: RouteEditorSlot;
  tab: string;
  onTab: (id: string) => boolean;
  onDeleted: (id: string) => void;
}

function routeUsesSecret(route: ProxyRoute): boolean {
  const rules = routeRules(route);
  const destinations = rules.length ? rules : [route];
  return destinations.some((dest) =>
    Object.values(dest.set_headers).some((header) => header.secret !== undefined),
  );
}

/** One-line summary beside the button; the confirm dialog has the details. */
function deleteSummary(group: ProviderGroup, state: ProxyState): string {
  const routeNames = new Set(group.routes.map((route) => route.name));
  const clients = state.callers.filter(
    (caller) => !caller.revoked && caller.grants.some((grant) => routeNames.has(grant)),
  ).length;
  const routes = `${group.routes.length} ${group.routes.length === 1 ? 'route' : 'routes'}`;
  return `Removes ${routes}${clients ? ` and access for ${clients} ${clients === 1 ? 'client' : 'clients'}` : ''}. Can’t be undone.`;
}

/** What deleting the provider does, one consequence per entry. */
function deleteProviderConsequences(group: ProviderGroup, state: ProxyState): string[] {
  const routeNames = new Set(group.routes.map((route) => route.name));
  const affected = state.callers
    .filter((caller) => !caller.revoked && caller.grants.some((grant) => routeNames.has(grant)))
    .map((caller) => caller.name)
    .join(', ');
  const routesPart =
    group.routes.length === 1
      ? `Removes route ${group.routes[0].name}`
      : `Removes ${group.routes.length} routes (${group.routes.map((route) => route.name).join(', ')})`;
  const notes = [
    `${routesPart} and permanently revokes client access${affected ? ` for ${affected}` : ''}.`,
  ];
  if (group.routes.some((route) => route.account)) {
    notes.push('Connected accounts stay until removed separately.');
  }
  if (group.routes.some(routeUsesSecret)) {
    notes.push('Keys no other route uses are removed from Secrets.');
  }
  notes.push('Re-adding this provider will not restore grants.');
  notes.push('Only new requests are blocked; dispatched requests may continue.');
  return notes;
}

function deleteProviderConsequence(group: ProviderGroup, state: ProxyState): string {
  return `Delete provider ${group.id}? ${deleteProviderConsequences(group, state).join(' ')}`;
}

/** Plain summary of how the proxy authenticates upstream for this provider. */
export function credentialSummary(group: ProviderGroup, state: ProxyState): string {
  const native = group.routes.map((route) => routeNativeProvider(route, state)).find(Boolean);
  if (native) return `${native.shortName} shared sign-in`;
  const account = group.routes.find((route) => route.account)?.account;
  if (account) return `Account ${account}`;
  const keys = new Set<string>();
  for (const route of group.routes) {
    const rules = routeRules(route);
    for (const dest of rules.length ? rules : [route]) {
      for (const header of Object.values(dest.set_headers)) {
        if (header.secret !== undefined) keys.add(header.secret);
      }
    }
  }
  if (!keys.size) return 'No upstream credential';
  const missing = [...keys].some(
    (name) => !name || !state.secrets.some((secret) => secret.name === name),
  );
  if (missing) return 'API key missing';
  return `${keys.size === 1 ? 'API key' : 'API keys'} ${[...keys].join(', ')}`;
}

function section(title: string, ...children: (Node | string)[]): HTMLElement {
  const node = el('div', el('h3', title), ...children);
  node.className = 'provider-section';
  return node;
}

/** The detail pane for one provider: header, then Access/Connection/Config/Settings tabs. */
export function providerDetail(group: ProviderGroup, deps: ProviderDetailDeps): HTMLElement {
  const { state, api, context, readonly, componentContext, blocked, editor } = deps;
  const routes = group.routes.map((route) => ({
    route,
    preset: matchRoutePreset(route, state),
    display: routeSummary(route, state),
  }));

  // ------------------------------------------------------------- header
  const heading = el('h2', group.id);
  heading.tabIndex = -1;
  const title = el('div', providerDot(group), heading);
  title.className = 'provider-detail-title';
  if (group.label !== group.id) {
    const label = el('span', group.label);
    label.className = 'provider-detail-label';
    title.append(label);
  }
  if (group.reason) {
    const status = el('span', group.reason);
    status.className = `provider-status provider-status-${group.status}`;
    title.append(status);
  }
  const family = providerFamilies.find(([id]) => id === group.family)?.[1] ?? group.family;
  const meta = el('p', el('span', family), el('span', credentialSummary(group, state)));
  meta.className = 'provider-detail-meta';
  const prefixes = el('span');
  prefixes.className = 'provider-detail-routes';
  for (const route of group.routes) {
    // Refreshes focus a route by name; the prefix is always on screen.
    const code = el('code', route.local_prefix);
    code.tabIndex = -1;
    code.dataset.focus = `route:${route.name}`;
    if (!route.enabled) code.title = 'Disabled';
    prefixes.append(code);
  }
  meta.append(prefixes);

  // ------------------------------------------------------------- access
  const access = el('div', providerAccess(group, state, api, componentContext()));
  const seenNative = new Set<NativeProvider>();
  if (!readonly) {
    for (const { route } of routes) {
      const native = routeNativeProvider(route, state);
      if (!native || seenNative.has(native)) continue;
      const setup = providerRouteAction(route, state, api, componentContext());
      if (!setup) continue;
      seenNative.add(native);
      const shared = section('Shared subscription', setup);
      shared.classList.add('provider-account-section');
      access.append(shared);
    }
  }

  // ------------------------------------------------------------- connection
  const connection = el('div');
  const accountDeps = () => ({ state, api, context: componentContext() });
  // Credentials are maintained beside the routes that use them. Native
  // sign-ins are held by the proxy and managed on the Access tab.
  function routeAccountTools(route: ProxyRoute, kind?: ProxyAccount['kind']) {
    if (
      !route.account ||
      state.accounts === undefined ||
      routeNativeProvider(route, state) ||
      nativeProvider(kind)
    )
      return;
    const account = state.accounts.find((a) => a.name === route.account);
    if (!account) return connectAccount(accountDeps(), route.account, kind);
    const usedBy = state.routes.filter((r) => r.account === account.name).map((r) => r.name);
    return accountControls(accountDeps(), account, usedBy, `route:${route.name}`);
  }
  const seenAccounts = new Set<string>();
  for (const { route, preset } of routes) {
    if (!route.account || seenAccounts.has(route.account) || routeNativeProvider(route, state))
      continue;
    seenAccounts.add(route.account);
    // Legacy Grok CLI routes no longer have a picker preset.
    const kind = preset?.account_kind ?? (route.profile === 'grok-bin' ? 'grok' : undefined);
    const account = routeAccountTools(route, kind);
    const signIn = providerRouteAction(route, state, api, componentContext());
    if (!signIn && !account) continue;
    const settings = section('Account');
    settings.classList.add('provider-account-section');
    if (signIn) settings.append(signIn);
    if (account) settings.append(account);
    // Account controls belong to the provider, not to each child route.
    connection.append(settings);
  }

  function routeAdminActions(route: ProxyRoute, panel: HTMLElement, preset?: ProxyPreset) {
    const rowContext = componentContext();
    const edit = (focusAccount = false) => {
      if (blocked()) return;
      if (editor.current?.owner === panel) {
        editor.current.form.querySelector<HTMLElement>('input, select, button')?.focus();
        return;
      }
      editor.close();
      const form = routeForm(
        state,
        api,
        rowContext,
        () => {
          if (blocked()) return;
          editor.close();
          editButton.focus();
        },
        route,
        preset,
      );
      panel.append(form);
      editor.current = { owner: panel, form };
      const target = focusAccount ? form.querySelector<HTMLSelectElement>('select') : undefined;
      (target ?? form.querySelector('input'))?.focus();
    };
    const editButton = button('Edit', () => edit());
    editButton.setAttribute('aria-label', `Edit ${route.name}`);
    const toggle = button(route.enabled ? 'Disable' : 'Enable', () => {
      if (blocked()) return;
      if (
        route.enabled &&
        !window.confirm(
          `Disable route ${route.name}? New requests will be blocked. Client grants are kept; dispatched requests may continue until finish or deadline.`,
        )
      )
        return;
      void rowContext.run(async () => {
        await api.config(
          state,
          state.routes.map((r) => (r === route ? { ...r, enabled: !r.enabled } : r)),
        );
        await context.refresh(`route:${route.name}`);
        context.announce(
          `${route.enabled ? 'Disabled' : 'Enabled'} route ${route.name}.${route.enabled ? ' New requests are blocked; dispatched requests may continue.' : ''}`,
        );
      }, toggle);
    });
    toggle.setAttribute('aria-label', `${route.enabled ? 'Disable' : 'Enable'} ${route.name}`);
    const controls = actions(editButton, toggle);
    if ((route.account && !routeNativeProvider(route, state)) || routeNeedsSecret(route)) {
      controls.append(
        button(route.account ? 'Change linked account' : 'Configure API key', () =>
          edit(!!route.account),
        ),
      );
    }
    return controls;
  }

  const routesSection = section(routes.length === 1 ? 'Route' : 'Routes');
  routesSection.classList.add('provider-routes-section');
  for (const { route, preset, display } of routes) {
    const panel = el('div');
    panel.className = 'provider-route-management';
    panel.dataset.route = route.name;
    const routeHeader = el('div', el('code', route.local_prefix));
    routeHeader.className = 'provider-route-header';
    if (!route.enabled) {
      routeHeader.append(badge('Disabled', 'muted'));
    }
    if (!readonly) routeHeader.append(routeAdminActions(route, panel, preset));
    panel.append(routeHeader, display.advanced);
    routesSection.append(panel);
  }
  connection.append(routesSection);
  if (!readonly) {
    const additions = missingCompanions(group, state).map((preset) => {
      const addRoute = button(`Add ${preset.label} route`, () => {
        if (blocked()) return;
        editor.close();
        const form = routeForm(
          state,
          api,
          componentContext(),
          () => {
            if (blocked()) return;
            editor.close();
            addRoute.focus();
          },
          undefined,
          preset,
          group,
        );
        routesSection.append(form);
        editor.current = { owner: routesSection, form };
        form.querySelector('input')?.focus();
      });
      return addRoute;
    });
    if (additions.length) routesSection.append(actions(...additions));
  }

  // ------------------------------------------------------------- settings
  function settingsPanel(): HTMLElement {
    const label = input(group.label);
    const labelForm = el(
      'form',
      field(
        'Provider label',
        label,
        `Describes the provider beside its name, ${group.id}, which is fixed and is what lists show.`,
      ),
    );
    labelForm.className = 'provider-label-form';
    submit(
      labelForm,
      componentContext(),
      async () => {
        await api.renameProviderLabel(state.revision, group.id, label.value.trim());
        await context.refresh(`route:${group.routes[0].name}`);
        context.announce(`Saved label for ${group.id}.`);
      },
      'Save label',
    );
    const removeContext = componentContext();
    const remove = button(`Delete provider`, () => {
      if (blocked()) return;
      if (!window.confirm(deleteProviderConsequence(group, state))) return;
      void removeContext.run(async () => {
        try {
          await api.deleteProvider(state.revision, group.id);
        } catch (error) {
          if (error instanceof ProxyAPIError && error.status === 409) {
            try {
              await context.refresh();
            } catch {
              // Preserve the conflict message.
            }
          }
          throw error;
        }
        deps.onDeleted(group.id);
        await context.refresh();
        context.announce(
          `Deleted provider ${group.id}. Its client grants were removed; dispatched requests may continue.`,
        );
      }, remove);
    });
    remove.className = 'danger';
    remove.setAttribute('aria-label', `Delete provider ${providerName(group)}`);
    const danger = el('div', el('p', deleteSummary(group, state)), remove);
    danger.className = 'provider-danger-zone';
    const labelSection = el('div', labelForm);
    labelSection.className = 'provider-section';
    return el('div', labelSection, danger);
  }

  // ------------------------------------------------------------- tabs
  // Mark the tab that holds the fix: shared native sign-in lives on Access.
  const tone = group.status === 'red' ? 'down' : group.status === 'orange' ? 'warn' : undefined;
  const nativeSignIn = group.routes.some((route) => {
    if (!routeNativeProvider(route, state)) return false;
    return routeSharedNativeAccount(route, state)?.state !== 'ready';
  });
  const tabs: ProviderTab[] = [
    {
      id: 'access',
      label: 'Access',
      count: approvalCounts(group, state).access,
      tone: nativeSignIn ? tone : undefined,
      panel: access,
    },
    {
      id: 'connection',
      label: 'Connection',
      tone: nativeSignIn ? undefined : tone,
      panel: connection,
    },
  ];
  if (group.family === 'inference') {
    tabs.push({
      id: 'config',
      label: 'Client config',
      panel: el('div', providerConfigSection(group, state, api, componentContext(), readonly)),
    });
  }
  if (!readonly) tabs.push({ id: 'settings', label: 'Settings', panel: settingsPanel() });

  const head = el('div', title, meta);
  head.className = 'provider-detail-head';
  const view = el(
    'section',
    head,
    providerTabs(`${providerName(group)} settings`, tabs, deps.tab, deps.onTab),
  );
  view.className = 'provider-detail';
  view.dataset.provider = group.id;
  // Shared native setup refreshes with this key; focus lands on the heading.
  const native = group.routes.map((route) => routeNativeProvider(route, state)).find(Boolean);
  if (native) view.dataset.focus = `native:${native.launcher}`;
  view.setAttribute('aria-label', providerTitle(group));
  return view;
}

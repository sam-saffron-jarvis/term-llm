import { accountsView } from './accounts';
import { browserPasskeyPlatform, type PasskeyPlatform } from '../src/hub/platform/passkeys';
import { secretForm } from './security';
import {
  ProxyAPIError,
  type ProxyClient,
  type ProxyBilling,
  type ProxyPreset,
  type ProxyRoute,
  type ProxyState,
} from '../src/api/proxy-client';
import {
  actions,
  button,
  card,
  check,
  confirmAction,
  disclosure,
  download,
  el,
  field,
  input,
  sameOrigin,
  select,
  submit,
  type UIContext,
} from './dom';

// Compare only authority-bearing settings; local auth/prefix/enabled changes keep grants.
function authority(route: ProxyRoute): string {
  const url = new URL(route.upstream);
  const headers = Object.entries(route.set_headers)
    .map(([name, rule]) => [
      name.toLowerCase(),
      rule.secret ?? null,
      rule.prefix ?? '',
      rule.value ?? null,
    ])
    .sort((a, b) => String(a[0]).localeCompare(String(b[0])));
  return JSON.stringify([
    url.origin,
    url.pathname.replace(/\/$/, ''),
    headers,
    route.account || '',
    !!route.websocket,
    [...(route.allow ?? [])].sort(),
    route.redirects || 'passthrough',
  ]);
}
export function routesView(
  state: ProxyState,
  api: ProxyClient,
  context: UIContext,
  platform: PasskeyPlatform = browserPasskeyPlatform(),
): HTMLElement {
  const view = el('div');
  const readonly = state.mode === 'file';
  const addSlot = el('div');
  const editor = el('div');
  view.append(
    card(
      'Routes',
      el(
        'p',
        'A route delegates its entire upstream base authority. Use trusted upstreams; saving never contacts or tests them.',
      ),
    ),
  );
  view.firstElementChild?.classList.add('page-intro');
  const steps = el('ol');
  const step = (done: boolean, text: string, jump: HTMLElement) => {
    const item = el('li', el('span', text), jump);
    if (done) item.classList.add('completed');
    return item;
  };
  const routeJump = button(readonly ? 'Review file routes' : 'Set up route', () => {
    if (readonly) view.querySelector('h2')?.focus();
    else edit();
  });
  steps.append(
    step(
      !!state.secrets.length,
      'Store an origin-bound secret, if needed',
      button('Go to secrets', () => context.navigate('Secrets')),
    ),
    step(!!state.routes.length, 'Add a route to a trusted upstream', routeJump),
    step(
      state.callers.some((c) => !c.revoked && c.grants.length > 0),
      'Create a client and grant routes',
      button('Go to clients', () => context.navigate('Clients')),
    ),
    step(
      false,
      'Send your first request with the client token',
      button('View requests', () => context.navigate('Requests')),
    ),
  );
  const onboarding = disclosure(
    state.routes.length ? 'Setup checklist' : 'Start with one route',
    steps,
  );
  onboarding.open = !state.routes.length;
  onboarding.className = 'onboarding';
  view.append(onboarding);
  let addButton: HTMLButtonElement | undefined;
  if (readonly)
    view.append(
      el(
        'p',
        'Routes are file-managed and read-only. Reloading drops incompatible client grants fail-closed.',
      ),
    );
  else {
    const cmdBar = el('div');
    cmdBar.className = 'route-cmd-bar';
    const presetsWrap = el('div');
    presetsWrap.className = 'route-presets';
    if (state.presets?.length) {
      presetsWrap.append(el('span', 'Quick add:'));
      for (const p of state.presets) {
        const pill = button(p.label, () => edit(undefined, addSlot, p));
        pill.className = 'preset-pill';
        presetsWrap.append(pill);
      }
      const custom = button('Custom', () => edit(undefined, addSlot));
      custom.className = 'preset-pill';
      presetsWrap.append(custom);
    }
    addButton = button('Add route', () => edit());
    addButton.className = 'primary';
    cmdBar.append(presetsWrap, addButton);
    view.append(cmdBar, addSlot);
  }
  const PAGE_SIZE = 10;
  let currentPage = 0;
  const routesSection = el('div');
  const routeList = el('div');
  routeList.className = 'route-list';
  const paginationNav = el('nav');
  paginationNav.className = 'route-pagination';
  paginationNav.setAttribute('aria-label', 'Route pagination');

  function pageNumbers(current: number, total: number): (number | 'ellipsis')[] {
    if (total <= 7) return Array.from({ length: total }, (_, i) => i);
    const pages: (number | 'ellipsis')[] = [0];
    const start = Math.max(1, current - 1);
    const end = Math.min(total - 2, current + 1);
    if (start > 1) pages.push('ellipsis');
    for (let i = start; i <= end; i++) pages.push(i);
    if (end < total - 2) pages.push('ellipsis');
    pages.push(total - 1);
    return pages;
  }

  function renderRoutes() {
    routeList.replaceChildren();
    paginationNav.replaceChildren();
    if (!state.routes.length) {
      routesSection.replaceChildren();
      return;
    }
    const totalPages = Math.max(1, Math.ceil(state.routes.length / PAGE_SIZE));
    currentPage = Math.max(0, Math.min(currentPage, totalPages - 1));
    const start = currentPage * PAGE_SIZE;
    const end = Math.min(start + PAGE_SIZE, state.routes.length);
    for (const route of state.routes.slice(start, end)) {
      const badge = (text: string, kind = '') => {
        const chip = el('span', text);
        chip.className = `badge ${kind}`;
        return chip;
      };
      const path = el('code', `${route.local_prefix} → ${route.upstream}`);
      const relationships = el('div');
      relationships.className = 'route-badges';
      relationships.append(
        badge(route.enabled ? 'Enabled' : 'Disabled', route.enabled ? 'enabled' : 'muted'),
        badge(
          (
            {
              pay_as_you_go: 'Pay as you go',
              plan: 'Included in a plan',
              local: 'Runs at home',
              unspecified: 'Billing not specified',
            } as const
          )[route.billing || 'unspecified'],
        ),
      );
      if (route.upstream.startsWith('http:'))
        relationships.append(badge('HTTP · unencrypted', 'caution'));
      for (const [header, rule] of Object.entries(route.set_headers))
        relationships.append(badge(`${rule.secret || 'Public literal'} → ${header}`, 'credential'));
      const callers = state.callers.filter(
        (caller) => !caller.revoked && caller.grants.includes(route.name),
      ).length;
      relationships.append(badge(`${callers} ${callers === 1 ? 'client' : 'clients'}`));
      const dot = el('span');
      dot.className = `route-dot${route.enabled ? '' : ' off'}`;
      dot.setAttribute('aria-hidden', 'true');
      const title = el('h2', route.name);
      title.tabIndex = -1;
      const summary = el('div', dot, title, path, relationships);
      summary.className = 'route-summary';
      const head = el('div', summary);
      head.className = 'route-row-head';
      const row = el('article', head);
      row.dataset.focus = `route:${route.name}`;
      row.className = `route-card route-accordion-item${route.enabled ? '' : ' disabled-route'}`;
      if (!readonly) {
        const editButton = button('Edit', () => edit(route, row));
        editButton.setAttribute('aria-label', `Edit ${route.name}`);
        const toggle = button(route.enabled ? 'Disable' : 'Enable', () => {
          if (
            route.enabled &&
            !window.confirm(
              `Disable route ${route.name}? New requests will be blocked. Client grants are kept; dispatched requests may continue until finish or deadline.`,
            )
          )
            return;
          void context.run(async () => {
            await api.config(
              state.revision,
              state.routes.map((r) => (r === route ? { ...r, enabled: !r.enabled } : r)),
            );
            await context.refresh(`route:${route.name}`);
            context.announce(
              `${route.enabled ? 'Disabled' : 'Enabled'} route ${route.name}.${route.enabled ? ' New requests are blocked; dispatched requests may continue.' : ''}`,
            );
          }, row);
        });
        toggle.setAttribute('aria-label', `${route.enabled ? 'Disable' : 'Enable'} ${route.name}`);
        head.append(actions(editButton, toggle));
      }
      routeList.append(row);
    }
    routesSection.replaceChildren(routeList);
    if (totalPages > 1) {
      const goTo = (page: number) => {
        editor.replaceChildren();
        currentPage = page;
        renderRoutes();
        routeList.querySelector<HTMLElement>('h2')?.focus();
      };
      const info = el('small', `Showing ${start + 1}–${end} of ${state.routes.length} routes`);
      const controls = el('div');
      controls.className = 'route-pagination-controls';
      const prev = button('Previous', () => goTo(currentPage - 1));
      prev.disabled = currentPage === 0;
      controls.append(prev);
      for (const item of pageNumbers(currentPage, totalPages)) {
        if (item === 'ellipsis') {
          const dots = el('span', '…');
          dots.className = 'route-pagination-ellipsis';
          dots.setAttribute('aria-hidden', 'true');
          controls.append(dots);
        } else {
          const pageBtn = button(String(item + 1), () => goTo(item));
          pageBtn.setAttribute('aria-label', `Page ${item + 1}`);
          if (item === currentPage) pageBtn.setAttribute('aria-current', 'page');
          controls.append(pageBtn);
        }
      }
      const next = button('Next', () => goTo(currentPage + 1));
      next.disabled = currentPage >= totalPages - 1;
      controls.append(next);
      paginationNav.append(info, controls);
      routesSection.append(paginationNav);
    }
  }

  renderRoutes();
  view.append(routesSection);
  if (state.accounts !== undefined) view.append(accountsView(state, api, context, platform));
  view.append(yamlView());
  function edit(route?: ProxyRoute, host: HTMLElement = addSlot, preset?: ProxyPreset) {
    view
      .querySelectorAll('.route-accordion-item.expanded')
      .forEach((n) => n.classList.remove('expanded'));
    if (host.classList.contains('route-accordion-item')) host.classList.add('expanded');
    let suggestedName = preset?.id ?? '';
    for (
      let n = 2;
      preset &&
      state.routes.some(
        (r) =>
          r.name === suggestedName ||
          r.local_prefix.startsWith(`/${suggestedName}/`) ||
          `/${suggestedName}/`.startsWith(r.local_prefix),
      );
      n++
    )
      suggestedName = `${preset.id}-${n}`;
    const initial = route ?? {
      name: suggestedName,
      local_prefix: suggestedName ? `/${suggestedName}/` : '',
      upstream: preset?.upstream ?? '',
      caller_auth: preset?.auth.header
        ? preset.auth
        : { header: 'Authorization', prefix: 'Bearer ' },
      // A trusted account supplies the upstream credential itself; never invent
      // a static secret substitution beside it.
      set_headers:
        preset?.auth.header && !preset.account_kind
          ? { [preset.auth.header]: { secret: '', prefix: preset.auth.prefix } }
          : {},
    };
    const form = el('form');
    form.className = 'route-form';
    if (!route) {
      const provider = select(
        [
          ['', 'Custom HTTP'],
          ...(state.presets ?? []).map((p) => [p.id, p.label] as [string, string]),
        ],
        preset?.id,
      );
      let dirty = false;
      const changed = (event: Event) => {
        if (event.target !== provider) dirty = true;
      };
      form.addEventListener('input', changed);
      form.addEventListener('change', changed);
      form.addEventListener('click', (event) => {
        if (event.target instanceof HTMLButtonElement) dirty = true;
      });
      provider.addEventListener('change', () => {
        if (dirty && !window.confirm('Change provider? This replaces the unsaved route fields.')) {
          provider.value = preset?.id ?? '';
          return;
        }
        edit(
          undefined,
          host,
          state.presets?.find((p) => p.id === provider.value),
        );
      });
      form.append(
        field(
          'Provider',
          provider,
          'Editable API-key defaults: add a key here or choose a stored secret. Plan and Bedrock providers use a trusted account from Accounts instead. Local CLI integrations that are not verified through the proxy are not offered.',
        ),
      );
      if (preset?.note) form.append(el('small', preset.note));
    }
    const name = input(initial.name);
    name.required = true;
    name.readOnly = !!route;
    name.pattern = '[A-Za-z0-9][A-Za-z0-9_-]{0,63}';
    const local = input(initial.local_prefix);
    local.required = true;
    let prefixEdited = !!route;
    local.addEventListener('input', () => {
      prefixEdited = true;
    });
    name.addEventListener('input', () => {
      if (!prefixEdited) local.value = name.value.trim() ? `/${name.value.trim()}/` : '';
    });
    const upstream = input(initial.upstream, 'url');
    upstream.required = true;
    const header = input(initial.caller_auth.header);
    header.required = true;
    const prefix = input(initial.caller_auth.prefix);
    const requestID = input(route?.request_id_header);
    const billing = select(
      [
        ['unspecified', 'Not sure yet — exclude from spending'],
        ['pay_as_you_go', 'Pay as you go — API usage charges'],
        ['plan', 'Plan — allowance and possible extra usage unknown'],
        ['local', 'Runs at home — no provider charge'],
      ],
      route?.billing || preset?.billing || 'unspecified',
    );
    // Offered only where the route needs custody: a new account-kind provider,
    // or an existing route that already names an account.
    const currentAccount = (state.accounts ?? []).find((a) => a.name === route?.account);
    const accountKind = route ? currentAccount?.kind : preset?.account_kind;
    const needsAccount = !!(route?.account || preset?.account_kind);
    const candidates = (state.accounts ?? []).filter((a) => a.kind === accountKind);
    const accountOptions: [string, string][] = candidates.map((a) => [
      a.name,
      `${a.name} · ${a.state.replaceAll('_', ' ')}`,
    ]);
    if (route?.account && !currentAccount)
      accountOptions.unshift([route.account, `${route.account} · unavailable`]);
    const account = select([['', 'Choose account'], ...accountOptions], route?.account ?? '');
    account.required = needsAccount;
    const websocket = check(
      'Forward WebSocket upgrades',
      route ? !!route.websocket : !!preset?.websocket,
    );
    const usage = select(
      [
        ['off', 'Off — forward responses without reading them'],
        ['auto', 'Automatic — read token counts from responses'],
      ],
      (route ? route.usage : preset?.usage) || 'off',
    );
    const [enabledLabel, enabled] = check('Enabled', route?.enabled ?? true);
    const [httpLabel, http] = check(
      'Allow insecure HTTP upstream (credentials may travel unencrypted)',
      route?.allow_insecure_http ?? false,
    );
    httpLabel.classList.add('insecure-option');
    const advanced = disclosure(
      'Advanced · local auth and request ID',
      websocket[0],
      field(
        'Usage measurement',
        usage,
        'Automatic asks the upstream for uncompressed responses and reads only reported token counts and model names; request and response content is never stored. Needed for spending estimates.',
      ),
      field('Local client authentication header', header),
      field(
        'Local client authentication prefix',
        prefix,
        'Exact prefix, including the trailing space. Default: "Bearer " (with a trailing space). This authenticates proxy clients, not the upstream.',
      ),
      field('Upstream request ID response header (optional)', requestID),
    );
    const coreGrid = el(
      'div',
      field(
        'Name',
        name,
        'Immutable identity: letters, numbers, - or _. A new identity needs new grants.',
      ),
      field(
        'Local prefix',
        local,
        'Suggested from the name; editable. A unique non-root path ending in /.',
      ),
      field('Upstream base URL', upstream, 'HTTPS recommended. No credentials, query or fragment.'),
      field(
        'How do you pay for this provider?',
        billing,
        'Used for future spending records only; never changes your provider bill or sets a spending limit. Choose Not sure if you do not know. Price cards on Home match this exact route name as their provider.',
      ),
    );
    if (needsAccount)
      coreGrid.append(
        field(
          'Trusted account',
          account,
          candidates.length
            ? `The proxy signs in upstream with this ${accountKind} account. No API key is stored on the route.`
            : accountKind
              ? `No ${accountKind} account exists yet. Add one under Accounts below, then return here.`
              : 'This account is unavailable. Restore it under Accounts before choosing another.',
        ),
      );
    coreGrid.className = 'route-form-grid';
    form.append(coreGrid, httpLabel);
    if (route) form.append(enabledLabel);
    form.append(advanced);
    const substitutions = el('div');
    const preview = el('p');
    preview.className = 'preview';
    type HeaderRow = {
      node: HTMLElement;
      header: HTMLInputElement;
      kind: HTMLSelectElement;
      value: HTMLInputElement;
      secret: HTMLSelectElement;
      prefix: HTMLInputElement;
      update: () => void;
    };
    const rows: HeaderRow[] = [];
    function upstreamOrigin() {
      try {
        return new URL(upstream.value).origin;
      } catch {
        return '';
      }
    }
    function previewRoute() {
      httpLabel.hidden = !upstream.value.toLowerCase().startsWith('http:');
      preview.textContent = `Client → ${location.origin}${local.value || '/…/'}… · ${header.value}: ${JSON.stringify(prefix.value)} + <token>\nUpstream ← ${upstream.value || 'choose a base URL'}${needsAccount ? ` · account ${account.value || '(choose one)'}` : ''}${rows.length ? ' · ' + rows.map((r) => `${r.header.value || 'Header'}: ${r.kind.value === 'secret' ? JSON.stringify(r.prefix.value) + ' + ••••' : '(public literal)'}`).join(', ') : needsAccount ? '' : ' · no substitutions'}`;
    }
    function addHeader(key = '', config: ProxyRoute['set_headers'][string] = { secret: '' }) {
      const keyInput = input(key);
      keyInput.required = true;
      const kind = select(
        [
          ['secret', 'Stored secret'],
          ['value', 'Literal (not secret)'],
        ],
        config.value !== undefined ? 'value' : 'secret',
      );
      const literal = input(config.value);
      const secret = select([['', 'Choose secret']]);
      let selected = config.secret ?? '';
      const secretPrefix = input(config.prefix);
      const literalField = field(
        'Literal value',
        literal,
        'Public configuration, returned in state and YAML. Never paste a credential here.',
      );
      const secretField = field('Secret name', secret);
      const prefixField = field(
        'Secret prefix',
        secretPrefix,
        'For example "Bearer " (with a trailing space).',
      );
      const warning = el('p');
      warning.className = 'warning';
      const missing = el('div');
      const node = el(
        'fieldset',
        el('legend', 'Upstream header substitution'),
        field('Header name', keyInput),
        field('Source', kind),
        literalField,
        secretField,
        prefixField,
        warning,
        missing,
      );
      node.className = 'header-substitution';
      const update = () => {
        const origin = upstreamOrigin();
        const matching = state.secrets.filter((s) => sameOrigin(s.origin, origin));
        selected = secret.value || selected;
        secret.replaceChildren(
          ...[['', 'Choose secret'], ...matching.map((s) => [s.name, s.name])].map(
            ([value, text]) => {
              const option = el('option', text);
              option.value = value;
              return option;
            },
          ),
        );
        secret.value = matching.some((s) => s.name === selected) ? selected : '';
        const useSecret = kind.value === 'secret';
        literalField.hidden = useSecret;
        secretField.hidden = !useSecret;
        prefixField.hidden = !useSecret;
        secret.required = useSecret;
        warning.textContent =
          !useSecret && /authorization|key|token/i.test(keyInput.value)
            ? 'This looks like a credential header. Use a stored secret; literal values are public configuration.'
            : '';
        warning.hidden = !warning.textContent;
        missing.replaceChildren();
        if (useSecret && origin && !matching.length)
          missing.append(
            el('p', `No secret is bound to ${origin} yet.`),
            button('Add API key', () => {
              const dialog = el('dialog', el('h2', 'Add API key'));
              dialog.setAttribute('aria-label', 'Add API key');
              const notice = el('p');
              notice.role = 'alert';
              notice.tabIndex = -1;
              const localContext: UIContext = {
                ...context,
                async run(action, source) {
                  notice.textContent = '';
                  const ok = await context.run(async () => {
                    try {
                      await action();
                    } catch (error) {
                      notice.textContent =
                        error instanceof Error ? error.message : 'Could not store secret.';
                      throw error;
                    }
                  }, source);
                  if (!ok && dialog.isConnected) notice.focus();
                  return ok;
                },
              };
              const base = `${(name.value || 'upstream').slice(0, 56)}-key`;
              let suggestion = base;
              for (let n = 2; state.secrets.some((s) => s.name === suggestion); n++)
                suggestion = `${base}-${n}`;
              const form = secretForm(state, api, localContext, {
                name: suggestion,
                origin,
                cancel: () => dialog.close(),
                stored: async (storedName) => {
                  let latest: ProxyState;
                  try {
                    const refreshed = await context.refresh(undefined, true);
                    if (!refreshed) {
                      dialog.close();
                      return;
                    }
                    latest = refreshed;
                  } catch (error) {
                    dialog.close();
                    if (error instanceof ProxyAPIError && error.status === 401) throw error;
                    context.announce(
                      'Secret stored; route not saved. Refresh to reload secrets before saving the route.',
                    );
                    return;
                  }
                  const unchanged = JSON.stringify(latest.routes) === JSON.stringify(state.routes);
                  state = {
                    ...state,
                    secrets: latest.secrets,
                    revision: unchanged ? latest.revision : state.revision,
                  };
                  selected = storedName;
                  rows.forEach((row) => row.update());
                  dialog.close();
                  secret.focus();
                  context.announce(
                    unchanged
                      ? 'API key stored and selected. Your route is not saved yet.'
                      : 'API key stored. Routes changed elsewhere; refresh before saving this route.',
                  );
                },
              });
              dialog.append(form, notice);
              dialog.addEventListener('cancel', (event) => {
                if (form.getAttribute('aria-busy') === 'true') event.preventDefault();
              });
              dialog.addEventListener('close', () => {
                form.reset();
                dialog.remove();
              });
              view.append(dialog);
              dialog.showModal();
              form.querySelector<HTMLInputElement>('input[type=password]')?.focus();
            }),
          );
        previewRoute();
      };
      secret.addEventListener('change', () => {
        selected = secret.value;
      });
      kind.addEventListener('change', update);
      keyInput.addEventListener('input', update);
      const row: HeaderRow = {
        node,
        header: keyInput,
        kind,
        value: literal,
        secret,
        prefix: secretPrefix,
        update,
      };
      rows.push(row);
      update();
      node.append(
        button('Remove substitution', () => {
          rows.splice(rows.indexOf(row), 1);
          node.remove();
          previewRoute();
        }),
      );
      substitutions.append(node);
    }
    for (const [key, value] of Object.entries(initial.set_headers)) addHeader(key, value);
    upstream.addEventListener('input', () => {
      rows.forEach((r) => r.update());
      previewRoute();
    });
    form.addEventListener('input', previewRoute);
    form.append(
      el('h3', 'Upstream header substitutions'),
      el('small', 'Secrets are write-only and must match the upstream origin.'),
      substitutions,
      button('Add substitution', () => addHeader()),
      preview,
    );
    if (route) {
      const affected = state.callers
        .filter((c) => !c.revoked && c.grants.includes(route.name))
        .map((c) => c.name);
      form.append(
        el(
          'p',
          `Changing the upstream origin/base path or header substitutions can invalidate grants${affected.length ? ` for ${affected.join(', ')}` : ''}. Same-name secret-byte rotation keeps grants. Dispatched requests may continue.`,
        ),
      );
    }
    submit(
      form,
      context,
      async () => {
        const url = new URL(upstream.value);
        if (
          !['https:', 'http:'].includes(url.protocol) ||
          url.username ||
          url.password ||
          url.hash ||
          url.search
        )
          throw new Error(
            'Use an HTTP(S) upstream URL without credentials, a query or a fragment.',
          );
        if (url.protocol === 'http:' && !http.checked)
          throw new Error('Explicitly allow insecure HTTP, or use HTTPS.');
        if (
          !local.value.startsWith('/') ||
          !local.value.endsWith('/') ||
          local.value === '/' ||
          local.value.includes('//') ||
          /[%\\\s]/.test(local.value) ||
          local.value.split('/').some((s) => s === '.' || s === '..')
        )
          throw new Error(
            'Use a canonical non-root local path ending in /, without escapes or dot segments.',
          );
        if (
          ['/_proxy/', '/healthz/', '/assets/'].some(
            (p) => local.value.startsWith(p) || p.startsWith(local.value),
          )
        )
          throw new Error('That local prefix is reserved for proxy administration or health.');
        if (
          state.routes.some(
            (r) =>
              r !== route &&
              (r.local_prefix.startsWith(local.value) || local.value.startsWith(r.local_prefix)),
          )
        )
          throw new Error('Local prefixes cannot overlap another route. Choose a distinct path.');
        if (state.routes.some((r) => r !== route && r.name === name.value.trim()))
          throw new Error('Route names must be unique.');
        const setHeaders: ProxyRoute['set_headers'] = Object.create(null);
        for (const row of rows) {
          if (
            Object.keys(setHeaders).some(
              (key) => key.toLowerCase() === row.header.value.trim().toLowerCase(),
            )
          )
            throw new Error('Header substitutions must have unique names.');
          if (row.kind.value === 'secret') {
            const secret = state.secrets.find((s) => s.name === row.secret.value);
            if (!secret || !sameOrigin(secret.origin, url.origin))
              throw new Error('Select a secret bound to this exact upstream origin.');
            setHeaders[row.header.value.trim()] = { secret: secret.name, prefix: row.prefix.value };
          } else setHeaders[row.header.value.trim()] = { value: row.value.value };
        }
        // An untouched account keeps its saved value even when it is unavailable;
        // any new choice must be an existing account of the required kind.
        if (needsAccount && account.value !== route?.account) {
          const chosen = (state.accounts ?? []).find((a) => a.name === account.value);
          if (!chosen || chosen.kind !== accountKind)
            throw new Error(
              `Choose an existing ${accountKind ?? 'trusted'} account. Add one under Accounts first.`,
            );
        }
        const next: ProxyRoute = {
          // Preserve server-owned admission policy (allow rules, redirects) and
          // future opaque metadata that this editor does not expose.
          ...route,
          billing: billing.value as ProxyBilling,
          ...(needsAccount ? { account: account.value } : {}),
          // Untouched controls keep the saved representation exactly.
          websocket: websocket[1].checked || (route?.websocket === false ? false : undefined),
          usage: usage.value === 'auto' ? 'auto' : route?.usage === 'off' ? 'off' : undefined,
          name: name.value.trim(),
          local_prefix: local.value,
          upstream: upstream.value,
          enabled: route ? enabled.checked : true,
          allow_insecure_http: http.checked,
          caller_auth: { header: header.value, prefix: prefix.value },
          set_headers: setHeaders,
          request_id_header: requestID.value || undefined,
        };
        if (
          route &&
          authority(route) !== authority(next) &&
          !window.confirm(
            `Save authority changes to ${route.name}? Affected client grants will be dropped and must be explicitly granted again. Dispatched requests may continue.`,
          )
        )
          return;
        await api.config(
          state.revision,
          route ? state.routes.map((r) => (r === route ? next : r)) : [...state.routes, next],
        );
        await context.refresh(`route:${next.name}`);
        context.announce(`Saved route ${next.name}. No upstream request was made.`);
      },
      'Save route',
    );
    form.append(
      button('Cancel', () => {
        editor.replaceChildren();
        host.classList.remove('expanded');
        (host.querySelector<HTMLElement>('h2') ?? addButton)?.focus();
      }),
    );
    if (route) {
      const affected =
        state.callers
          .filter((c) => !c.revoked && c.grants.includes(route.name))
          .map((c) => c.name)
          .join(', ') || 'Any client with this grant';
      const remove = confirmAction(
        `Delete route ${route.name}`,
        async () => {
          await api.config(
            state.revision,
            state.routes.filter((r) => r !== route),
          );
          await context.refresh();
          context.announce(
            `Deleted route ${route.name}. Its grants were removed; dispatched requests may continue.`,
          );
        },
        context,
        `Delete route ${route.name}? ${affected} will lose access. Re-adding this name will not restore grants. Disable instead to keep grants. Only new requests are blocked; dispatched requests may continue.`,
      );
      form.append(
        disclosure('Danger zone', el('p', 'Deletion removes client grants permanently.'), remove),
      );
    }
    editor.replaceChildren(card(route ? `Edit ${route.name}` : 'Add route', form));
    host.append(editor);
    previewRoute();
    name.focus();
  }
  function yamlView() {
    const section = disclosure(
      'Configuration file',
      el(
        'p',
        'Transfer route configuration in the same YAML format as --config. Includes route definitions and secret references, not secret values, client tokens or passkeys. This is not a full backup; back up the state directory for recovery.',
      ),
    );
    section.classList.add('config-disclosure');
    const exportButton = button('Download routes.yaml', () => {
      void context.run(async () => {
        download('routes.yaml', await api.exportYAML(), 'text/yaml');
        context.announce('Route configuration downloaded. Secret values are not included.');
      }, exportButton);
    });
    section.append(
      card(
        'Download configuration',
        el('p', 'Save route definitions for transfer or use with --config.'),
        exportButton,
      ),
    );
    if (readonly) {
      section.append(
        el(
          'p',
          'Routes are managed by --config. Edit that file and use Reload file to apply changes.',
        ),
      );
      return section;
    }
    const file = input('', 'file');
    file.accept = '.yaml,.yml,application/yaml,text/yaml';
    const fileField = field('YAML file', file, 'Choose a configuration file, up to 1 MiB.');
    const yaml = el('textarea');
    yaml.rows = 6;
    yaml.spellcheck = false;
    const paste = disclosure('Paste YAML instead', field('Route configuration YAML', yaml));
    paste.addEventListener('toggle', () => {
      fileField.hidden = paste.open;
    });
    const form = el('form', fileField, paste);
    submit(
      form,
      context,
      async () => {
        let contents: string;
        if (paste.open) {
          contents = yaml.value;
        } else {
          const selected = file.files?.[0];
          if (!selected) throw new Error('Choose a YAML file, or open Paste YAML instead.');
          if (selected.size > 1 << 20)
            throw new Error('Configuration files must be 1 MiB or smaller.');
          try {
            contents = await selected.text();
          } catch {
            throw new Error(
              'Could not read the configuration file. Choose it again or paste the YAML instead.',
            );
          }
        }
        if (!contents.trim())
          throw new Error(
            'The configuration is empty. Choose a file or paste YAML containing route definitions.',
          );
        if (new TextEncoder().encode(contents).length > 1 << 20)
          throw new Error('Configuration must be 1 MiB or smaller.');
        if (
          !window.confirm(
            'Replace route configuration with this YAML? Removed or authority-changed routes lose client grants. Dispatched requests may continue. Secret values, client tokens and passkeys are not restored by this file.',
          )
        )
          return;
        await api.importYAML(state.revision, contents);
        await context.refresh();
        context.announce('Route configuration replaced. Review client grants.');
      },
      'Replace routes from YAML',
    );
    section.append(
      card(
        'Replace route configuration',
        el(
          'p',
          'Replaces the current routes, not the whole proxy. Removed routes and changes to upstream authority remove affected client grants.',
        ),
        form,
      ),
    );
    return section;
  }
  return view;
}

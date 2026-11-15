import type { ProxyClient, ProxyRoute, ProxyState } from '../src/api/proxy-client';
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
  keyedCard,
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
  return JSON.stringify([url.origin, url.pathname.replace(/\/$/, ''), headers]);
}
export function routesView(state: ProxyState, api: ProxyClient, context: UIContext): HTMLElement {
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
  const step = (done: boolean, text: string, jump: HTMLElement) =>
    el('li', el('span', `${done ? '✓ ' : ''}${text}`), jump);
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
      'Create a caller and grant routes',
      button('Go to callers', () => context.navigate('Callers')),
    ),
    step(
      false,
      'Send your first request with the caller token',
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
  if (readonly)
    view.append(
      el(
        'p',
        'Routes are file-managed and read-only. Reloading drops incompatible caller grants fail-closed.',
      ),
    );
  else {
    const add = button('Add route', () => edit());
    add.className = 'primary';
    view.append(add, addSlot);
  }
  for (const route of state.routes) {
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
    );
    if (route.upstream.startsWith('http:'))
      relationships.append(badge('HTTP · unencrypted', 'caution'));
    for (const [header, rule] of Object.entries(route.set_headers))
      relationships.append(badge(`${rule.secret || 'Public literal'} → ${header}`, 'credential'));
    const callers = state.callers.filter(
      (caller) => !caller.revoked && caller.grants.includes(route.name),
    ).length;
    relationships.append(badge(`${callers} ${callers === 1 ? 'caller' : 'callers'}`));
    const row = keyedCard(`route:${route.name}`, route.name, path, relationships);
    row.className = `route-card${route.enabled ? '' : ' disabled-route'}`;
    if (!readonly) {
      const editButton = button('Edit', () => edit(route, row));
      editButton.setAttribute('aria-label', `Edit ${route.name}`);
      const toggle = button(route.enabled ? 'Disable' : 'Enable', () => {
        if (
          route.enabled &&
          !window.confirm(
            `Disable route ${route.name}? New requests will be blocked. Caller grants are kept; dispatched requests may continue until finish or deadline.`,
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
      row.append(actions(editButton, toggle));
    }
    view.append(row);
  }
  view.append(yamlView());
  function edit(route?: ProxyRoute, host: HTMLElement = addSlot) {
    const form = el('form');
    form.className = 'route-form';
    const name = input(route?.name);
    name.required = true;
    name.readOnly = !!route;
    name.pattern = '[A-Za-z0-9][A-Za-z0-9_-]{0,63}';
    const local = input(route?.local_prefix);
    local.required = true;
    let prefixEdited = !!route;
    local.addEventListener('input', () => {
      prefixEdited = true;
    });
    name.addEventListener('input', () => {
      if (!prefixEdited) local.value = name.value.trim() ? `/${name.value.trim()}/` : '';
    });
    const upstream = input(route?.upstream ?? '', 'url');
    upstream.required = true;
    const header = input(route?.caller_auth.header ?? 'Authorization');
    header.required = true;
    const prefix = input(route?.caller_auth.prefix ?? 'Bearer ');
    const requestID = input(route?.request_id_header);
    const [enabledLabel, enabled] = check('Enabled', route?.enabled ?? true);
    const [httpLabel, http] = check(
      'Allow insecure HTTP upstream (credentials may travel unencrypted)',
      route?.allow_insecure_http ?? false,
    );
    httpLabel.classList.add('insecure-option');
    const advanced = disclosure(
      'Advanced · local auth and request ID',
      field('Local caller authentication header', header),
      field(
        'Local caller authentication prefix',
        prefix,
        'Exact prefix, including the trailing space. Default: "Bearer " (with a trailing space). This authenticates proxy callers, not the upstream.',
      ),
      field('Upstream request ID response header (optional)', requestID),
    );
    form.append(
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
      httpLabel,
    );
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
      preview.textContent = `Caller → ${location.origin}${local.value || '/…/'}… · ${header.value}: ${JSON.stringify(prefix.value)} + <token>\nUpstream ← ${upstream.value || 'choose a base URL'}${rows.length ? ' · ' + rows.map((r) => `${r.header.value || 'Header'}: ${r.kind.value === 'secret' ? JSON.stringify(r.prefix.value) + ' + ••••' : '(public literal)'}`).join(', ') : ' · no substitutions'}`;
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
            button('Store one for this origin', () => {
              if (
                window.confirm(
                  'Leave this unsaved route to store a secret? Return to Routes afterward to finish configuring it.',
                )
              )
                context.navigate('Secrets', origin);
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
    for (const [key, value] of Object.entries(route?.set_headers ?? {})) addHeader(key, value);
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
        const next: ProxyRoute = {
          name: name.value.trim(),
          local_prefix: local.value,
          upstream: upstream.value,
          enabled: route ? enabled.checked : true,
          allow_insecure_http: http.checked,
          caller_auth: { header: header.value, prefix: prefix.value },
          set_headers: setHeaders,
          ...(requestID.value ? { request_id_header: requestID.value } : {}),
        };
        if (
          route &&
          authority(route) !== authority(next) &&
          !window.confirm(
            `Save authority changes to ${route.name}? Affected caller grants will be dropped and must be explicitly granted again. Dispatched requests may continue.`,
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
        (
          host.querySelector<HTMLElement>('h2') ??
          view.querySelector<HTMLButtonElement>(':scope > button')
        )?.focus();
      }),
    );
    if (route) {
      const affected =
        state.callers
          .filter((c) => !c.revoked && c.grants.includes(route.name))
          .map((c) => c.name)
          .join(', ') || 'Any caller with this grant';
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
        disclosure('Danger zone', el('p', 'Deletion removes caller grants permanently.'), remove),
      );
    }
    editor.replaceChildren(card(route ? `Edit ${route.name}` : 'Add route', form));
    host.append(editor);
    previewRoute();
    name.focus();
  }
  function yamlView() {
    const section = disclosure(
      'Import / export YAML',
      el(
        'p',
        'Secret-free YAML: names and origin bindings, never credential values. Import replaces route configuration and can invalidate grants. File references are operator-only.',
      ),
    );
    section.append(
      button('Export YAML', () => {
        void context.run(async () => {
          download('proxy.yaml', await api.exportYAML(), 'text/yaml');
          context.announce('Secret-free YAML exported.');
        });
      }),
    );
    if (!readonly) {
      const yaml = el('textarea');
      yaml.rows = 8;
      yaml.required = true;
      yaml.spellcheck = false;
      const form = el('form', field('Secret-free YAML', yaml));
      submit(
        form,
        context,
        async () => {
          if (
            !window.confirm(
              'Replace route configuration with this YAML? Removed or authority-changed routes lose caller grants. Dispatched requests may continue.',
            )
          )
            return;
          await api.importYAML(state.revision, yaml.value);
          await context.refresh();
          context.announce('YAML imported. Review caller grants.');
        },
        'Import YAML',
      );
      section.append(form);
    }
    return section;
  }
  return view;
}

import type { ProxyCaller, ProxyClient, ProxyRoute, ProxyState } from '../src/api/proxy-client';
import {
  actions,
  button,
  card,
  check,
  confirmAction,
  dateTime,
  el,
  field,
  input,
  keyedCard,
  requireOrigin,
  select,
  submit,
  type UIContext,
} from './dom';

export function secretsView(
  state: ProxyState,
  api: ProxyClient,
  context: UIContext,
  prefillOrigin?: string,
): HTMLElement {
  const view = el(
    'div',
    card(
      'Secrets',
      el(
        'p',
        'Write-only credentials, bound to an exact scheme, host and port. File-sourced secrets are read-only.',
      ),
    ),
  );
  view.firstElementChild?.classList.add('page-intro');
  const editor = el('div');
  function edit(existing?: { name: string; origin: string }) {
    const name = input(existing?.name);
    name.required = true;
    name.readOnly = !!existing;
    name.pattern = '[A-Za-z0-9][A-Za-z0-9_-]{0,63}';
    const origin = input(existing?.origin ?? prefillOrigin ?? '', 'url');
    origin.required = true;
    origin.readOnly = !!existing;
    const value = input('', 'password');
    value.required = true;
    value.autocomplete = 'new-password';
    const form = el(
      'form',
      field('Secret name', name),
      field(
        'Bound origin',
        origin,
        'For example https://api.example.com. A stored secret cannot change origin.',
      ),
      field('New secret value', value, 'Write-only: replace it any time, never view it.'),
    );
    if (!existing && state.routes.length) {
      const origins = [...new Set(state.routes.map((route) => new URL(route.upstream).origin))];
      const suggested = select([
        ['', 'Choose a route origin'],
        ...origins.map((url) => [url, url] as [string, string]),
      ]);
      suggested.addEventListener('change', () => {
        if (suggested.value) origin.value = suggested.value;
      });
      form.append(field('Use route origin (optional)', suggested));
    }
    submit(
      form,
      context,
      async () => {
        if (!existing && state.secrets.some((s) => s.name === name.value.trim()))
          throw new Error(
            'A secret with this name already exists. Use its Replace action to rotate it explicitly.',
          );
        const boundOrigin = requireOrigin(origin.value);
        if (
          existing &&
          !window.confirm(
            `Replace secret ${existing.name}? Its old value cannot be recovered. Same-name rotation keeps caller grants. Dispatched requests may still use the old value.`,
          )
        )
          return;
        const credential = value.value;
        value.value = '';
        await api.secret(name.value.trim(), boundOrigin, credential, !!existing);
        await context.refresh(`secret:${name.value.trim()}`);
        context.announce(
          `${existing ? 'Replaced' : 'Stored'} secret ${name.value.trim()}. The value cannot be read back.`,
        );
      },
      existing ? 'Replace secret' : 'Store secret',
    );
    form.append(
      button('Cancel', () => {
        value.value = '';
        editor.replaceChildren();
        view.querySelector('h2')?.focus();
      }),
    );
    editor.replaceChildren(card(existing ? 'Replace secret' : 'Store secret', form));
    (existing ? value : name).focus();
  }
  view.append(
    button('Add secret', () => {
      view.insertBefore(editor, view.children[2] ?? null);
      edit();
    }),
    editor,
  );
  for (const secret of state.secrets) {
    const used = state.routes.some((r) =>
      Object.values(r.set_headers).some((h) => h.secret === secret.name),
    );
    const row = keyedCard(
      `secret:${secret.name}`,
      secret.name,
      el(
        'p',
        `${secret.origin || 'Operator-managed origin'} · ${secret.source}${used ? ' · in use' : ''}`,
      ),
    );
    if (secret.source === 'stored') {
      const remove = confirmAction(
        `Delete secret ${secret.name}`,
        async () => {
          await api.deleteSecret(secret.name);
          await context.refresh();
          context.announce(`Deleted secret ${secret.name}. Its value cannot be recovered.`);
        },
        context,
        `Delete secret ${secret.name}? Its value cannot be recovered. Referenced secrets cannot be deleted.`,
      );
      remove.disabled = used;
      if (used) remove.title = 'Remove route references before deleting this secret.';
      row.append(
        actions(
          button(`Replace ${secret.name}`, () => {
            row.append(editor);
            edit(secret);
          }),
          remove,
        ),
      );
    }
    view.append(row);
  }
  if (prefillOrigin)
    queueMicrotask(() => {
      if (view.isConnected) edit();
    });
  return view;
}

export function shellQuote(value: string): string {
  return `'${value.replaceAll("'", "'\\''")}'`;
}
export function callerCurl(route: ProxyRoute, origin: string): string {
  return `curl -H ${shellQuote(`${route.caller_auth.header}: ${route.caller_auth.prefix}`)}"$TOKEN" -- ${shellQuote(origin + route.local_prefix)}`;
}

export function callersView(state: ProxyState, api: ProxyClient, context: UIContext): HTMLElement {
  const intro = card(
    'Callers',
    el(
      'p',
      'Separate tokens with explicit route grants. Each grant delegates that route’s upstream base authority.',
    ),
  );
  intro.classList.add('page-intro');
  const view = el('div', intro);
  function grantControls(selected: string[]) {
    const choices = state.routes.map((route) => ({
      route,
      check: check(
        route.name + (route.enabled ? '' : ' (disabled)'),
        selected.includes(route.name),
      ),
    }));
    const fields = el(
      'fieldset',
      el('legend', selected.length ? 'Route grants' : 'Route grants (none selected by default)'),
      ...choices.map((g) => g.check[0]),
    );
    return {
      fields,
      selected: () => choices.filter((g) => g.check[1].checked).map((g) => g.route.name),
    };
  }
  const name = input();
  name.required = true;
  name.maxLength = 80;
  const grants = grantControls([]);
  const form = el('form', el('h2', 'Create caller'), field('Caller name', name), grants.fields);
  submit(
    form,
    context,
    async () => {
      const selected = grants.selected();
      if (!selected.length) throw new Error('Select at least one explicit route grant.');
      context.protectToken(true, true);
      context.announce('Creating caller… wait for the one-time token.');
      view.inert = true;
      let issued = false;
      try {
        const result = await api.createCaller(name.value.trim(), selected, state.revision);
        issued = true;
        context.protectToken(true);
        const token = input(result.token);
        token.readOnly = true;
        token.className = 'caller-token';
        token.spellcheck = false;
        const copy = button('Copy token', () => {
          void context.run(async () => {
            if (!navigator.clipboard) {
              token.focus();
              token.select();
              context.announce('Clipboard unavailable. Copy the selected token manually.');
              return;
            }
            try {
              await navigator.clipboard.writeText(token.value);
              copy.textContent = 'Copied ✓';
              setTimeout(() => {
                if (copy.isConnected) copy.textContent = 'Copy token';
              }, 2000);
              context.announce('Caller token copied. Store it safely before dismissing.');
            } catch {
              token.focus();
              token.select();
              context.announce('Copy unavailable. Copy the selected token manually.');
            }
          }, copy);
        });
        copy.setAttribute('aria-label', 'Copy token');
        const panel = keyedCard(
          `caller:${result.caller.id}`,
          'Copy this token now',
          el('p', 'Shown once. Save it securely before dismissing. It cannot be retrieved later.'),
          field('Caller token', token),
          copy,
          el(
            'p',
            'Set TOKEN in your shell to this token. Examples below use $TOKEN, not the administrator token.',
          ),
        );
        for (const route of state.routes.filter((r) => selected.includes(r.name)))
          panel.append(
            el('h3', route.name),
            el(
              'p',
              `Base URL: ${location.origin}${route.local_prefix} · Header: ${route.caller_auth.header}: ${JSON.stringify(route.caller_auth.prefix)} + <token>`,
            ),
            el('pre', callerCurl(route, location.origin)),
          );
        panel.append(
          button('I saved it — dismiss', () => {
            token.value = '';
            context.protectToken(false);
            panel.remove();
            void context.run(async () => {
              await context.refresh(`caller:${result.caller.id}`);
              context.announce('Caller created. The one-time token has been dismissed.');
            });
          }),
        );
        // Only token actions remain reachable until explicit dismissal/navigation.
        view.inert = false;
        view.replaceChildren(intro, panel);
        token.focus();
        token.select();
        context.announce(
          `Created caller ${result.caller.name}. Copy the token now; it is shown once.`,
        );
      } finally {
        view.inert = false;
        if (!issued) context.protectToken(false);
      }
    },
    'Create caller',
  );
  if (!state.routes.length)
    form.append(
      el('p', 'Add a route before creating a caller.'),
      button('Go to routes', () => context.navigate('Routes')),
    );
  view.append(form);
  function editCaller(caller: ProxyCaller, row: HTMLElement) {
    const chosen = grantControls(caller.grants);
    const editor = el(
      'form',
      el('h3', `Edit grants for ${caller.name}`),
      chosen.fields,
      el(
        'p',
        'The existing token stays valid. Removed grants block new requests only; dispatched requests may continue.',
      ),
    );
    submit(
      editor,
      context,
      async () => {
        const selected = chosen.selected();
        const removed = caller.grants.filter((grant) => !selected.includes(grant));
        if (
          removed.length &&
          !window.confirm(
            `Remove ${caller.name}’s grants for ${removed.join(', ')}? New requests to those routes will be blocked; dispatched requests may continue. The token is not reissued.`,
          )
        )
          return;
        await api.updateCaller(caller.id, selected, state.revision);
        await context.refresh(`caller:${caller.id}`);
        context.announce(`Saved grants for ${caller.name}. The token was not changed.`);
      },
      'Save grants',
    );
    editor.append(
      button('Cancel', () => {
        editor.remove();
        row.querySelector('h2')?.focus();
      }),
    );
    row.querySelector('form')?.remove();
    row.append(editor);
    editor.querySelector('input')?.focus();
  }
  for (const caller of state.callers) {
    const row = keyedCard(
      `caller:${caller.id}`,
      caller.name,
      el(
        'p',
        `${caller.revoked ? 'Revoked' : 'Active'} · Created ${dateTime(caller.created, 'Unknown')} · Last used ${dateTime(caller.last_used)}`,
      ),
      el('p', `Grants: ${caller.grants.join(', ') || 'none'}`),
    );
    if (caller.revoked) row.classList.add('revoked');
    else
      row.append(
        actions(
          button(`Edit grants for ${caller.name}`, () => editCaller(caller, row)),
          confirmAction(
            `Revoke ${caller.name}`,
            async () => {
              await api.revokeCaller(caller.id);
              await context.refresh(`caller:${caller.id}`);
              context.announce(
                `Revoked ${caller.name}. New requests are blocked; dispatched requests may continue.`,
              );
            },
            context,
            `Revoke ${caller.name}? This token can never make new requests again. Dispatched requests may continue until finish or deadline. Create a new caller to restore access.`,
          ),
        ),
      );
    view.append(row);
  }
  return view;
}

import { nativeConnectionDetails, routeNativeProvider, routeAllowsClientApproval } from './native';
import { termLLMConfigSection, tokenCopyRow } from './client-config-snippet';
import {
  clientLabel,
  clientListHeader,
  clientRow,
  duplicateSuffixes,
  providerChips,
  revokedClients,
  setRowExpanded,
  sortCallers,
  type ProviderApprovalState,
} from './client-list';
import type { ProxyCaller, ProxyClient, ProxyRoute, ProxyState } from '../src/api/proxy-client';
import { providerApproval, providerGroups } from './provider-groups';
import { slugPattern } from './provider-name';
import { authLabel, joinLocalPrefix, routeRules } from './route-rules';
import {
  actions,
  button,
  card,
  clearPassword,
  confirmAction,
  dateTime,
  el,
  field,
  input,
  keyedCard,
  shellQuote,
  submit,
  type UIContext,
} from './dom';

export function secretForm(
  state: ProxyState,
  api: ProxyClient,
  context: UIContext,
  options: {
    existing?: { name: string };
    name?: string;
    stored?: (name: string) => Promise<void>;
    cancel: () => void;
  },
): HTMLFormElement {
  const existing = options.existing;
  const name = input(existing?.name ?? options.name);
  name.required = true;
  name.readOnly = !!existing;
  name.pattern = slugPattern;
  name.maxLength = 64;
  const value = input('', 'password');
  value.required = true;
  value.autofocus = !!options.name;
  const form = el(
    'form',
    field('Secret name', name),
    field('New secret value', value, 'Write-only: the stored value cannot be viewed.'),
  );
  submit(
    form,
    context,
    async () => {
      if (!existing && state.secrets.some((s) => s.name === name.value.trim()))
        throw new Error(
          'A secret with this name already exists. Use its Replace action to rotate it explicitly.',
        );
      if (
        existing &&
        !window.confirm(
          `Replace secret ${existing.name}? Its old value cannot be recovered. Same-name rotation keeps client grants. Dispatched requests may still use the old value.`,
        )
      )
        return;
      const credential = value.value;
      clearPassword(value);
      await api.secret(name.value.trim(), credential, !!existing);
      if (options.stored) await options.stored(name.value.trim());
      else {
        await context.refresh(`secret:${name.value.trim()}`);
        context.announce(
          `${existing ? 'Replaced' : 'Stored'} secret ${name.value.trim()}. The value cannot be read back.`,
        );
      }
    },
    existing ? 'Replace secret' : 'Store secret',
  );
  form.append(
    button('Cancel', () => {
      clearPassword(value);
      options.cancel();
    }),
  );
  return form;
}

export function secretsView(state: ProxyState, api: ProxyClient, context: UIContext): HTMLElement {
  const view = el(
    'div',
    card(
      'Secrets',
      el(
        'p',
        'Write-only credentials referenced by name. Routes can share a secret. File-sourced secrets are read-only.',
      ),
    ),
  );
  view.firstElementChild?.classList.add('page-intro');
  const editor = el('div');
  function edit(existing?: { name: string }) {
    const form = secretForm(state, api, context, {
      existing,
      cancel: () => {
        editor.replaceChildren();
        view.querySelector('h2')?.focus();
      },
    });
    editor.replaceChildren(card(existing ? 'Replace secret' : 'Store secret', form));
    form.querySelector<HTMLInputElement>(existing ? 'input[type=password]' : 'input')?.focus();
  }
  view.append(
    button('Add secret', () => {
      view.insertBefore(editor, view.children[2] ?? null);
      edit();
    }),
    editor,
  );
  for (const secret of state.secrets) {
    const used = !!secret.routes?.length;
    const row = keyedCard(
      `secret:${secret.name}`,
      secret.name,
      el(
        'p',
        `${secret.source} · ${used ? `used by ${secret.routes!.join(', ')}` : 'not used by any route'}`,
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
  return view;
}

export function callerCurl(route: ProxyRoute, origin: string): string {
  return `curl -H ${shellQuote(`${route.caller_auth.header}: ${route.caller_auth.prefix}`)}"$TOKEN" -- ${shellQuote(origin + route.local_prefix)}`;
}

export function callersView(state: ProxyState, api: ProxyClient, context: UIContext): HTMLElement {
  const intro = card(
    'Clients',
    el(
      'p',
      'Each client gets its own token and can only use the providers you approve. Approval covers every current route in that provider and delegates its upstream access.',
    ),
  );
  intro.classList.add('page-intro');
  const view = el('div', intro);
  const active = sortCallers(state.callers.filter((caller) => !caller.revoked));
  const revoked = sortCallers(state.callers.filter((caller) => caller.revoked));
  const suffixes = duplicateSuffixes(state.callers);
  const keyNames = new Map(state.admin_keys.map(({ id, name }) => [id, name]));
  const name = input();
  name.required = true;
  name.maxLength = 80;
  name.placeholder = 'e.g. laptop, workstation';
  // Block additions until every native route in a provider is shared and ready.
  const groups = providerGroups(state);
  const unavailable = new Map<string, string[]>();
  for (const group of groups) {
    const blocked = group.routes
      .filter((route) => !routeAllowsClientApproval(route, state))
      .map((route) => route.name);
    if (blocked.length) unavailable.set(group.id, blocked);
  }
  const approvable = groups.some((group) => !unavailable.has(group.id));
  const providers = providerChips(
    groups,
    new Map<string, ProviderApprovalState>(),
    'Providers',
    unavailable,
  );
  const form = el('form', field('Client name', name));
  if (groups.length) {
    const approvalNote = el(
      'p',
      'Approval covers every current route in a provider. A route added later is not granted automatically.',
    );
    approvalNote.className = 'client-composer-note';
    const providerSection = el('div', providers.fields, approvalNote);
    providerSection.className = 'client-composer-providers';
    if (unavailable.size) {
      const nativeNote = el(
        'p',
        'Native providers can be approved once their shared routes are enabled and signed in.',
      );
      nativeNote.className = 'client-composer-note';
      providerSection.append(nativeNote);
    }
    form.append(providerSection);
  }
  form.className = 'client-composer';
  form.id = 'client-composer';
  const create = submit(
    form,
    context,
    async () => {
      const selected = providers.selected();
      if (!selected.length) throw new Error('Select at least one provider to approve.');
      context.protectToken(true, true);
      context.announce('Creating client… wait for the one-time token.');
      view.inert = true;
      let issued = false;
      try {
        const result = await api.createProviderCaller(name.value.trim(), selected, state.revision);
        issued = true;
        context.protectToken(true);
        const token = input(result.token);
        token.readOnly = true;
        token.className = 'caller-token';
        token.spellcheck = false;
        const lead = el(
          'p',
          'Shown once. Save it securely before dismissing. It cannot be retrieved later.',
        );
        lead.className = 'client-token-lead';
        // Show the routes the server actually approved, not the ticked provider
        // IDs, so the examples always match the new token's access.
        const granted = new Set(
          result.caller.grants?.length
            ? result.caller.grants
            : groups
                .filter((group) => selected.includes(group.id))
                .flatMap((group) => group.routes.map((route) => route.name)),
        );
        const approvedGroups = groups.filter(
          (group) =>
            group.routes.length > 0 && group.routes.some((route) => granted.has(route.name)),
        );
        const panel = keyedCard(
          `caller:${result.caller.id}`,
          'Copy this token now',
          lead,
          tokenCopyRow(token, context),
        );
        panel.classList.add('client-token-panel');
        const configSnippet = termLLMConfigSection(location.origin, approvedGroups, context, {
          heading: 'h3',
          getToken: () => token.value,
          onTokenFallback: () => {
            token.focus();
            token.select();
          },
        });
        if (configSnippet) panel.append(configSnippet);
        const routeNote = el(
          'p',
          'Approval covers every current route in each approved provider. For curl examples, set TOKEN in your shell to this client token, not the administrator token.',
        );
        routeNote.className = 'client-token-note';
        panel.append(routeNote);
        for (const route of state.routes.filter((candidate) => granted.has(candidate.name))) {
          const provider = routeNativeProvider(route, state);
          if (provider) {
            if (routeAllowsClientApproval(route, state)) {
              panel.append(
                el('h3', route.name),
                nativeConnectionDetails(
                  provider,
                  { url: location.origin + route.local_prefix, token: result.token },
                  context,
                  'shared-worker',
                ),
              );
            }
            continue;
          }
          const rules = routeRules(route);
          panel.append(el('h3', route.name));
          if (rules.length) {
            // A unified route has no parent client auth; each subpath declares
            // which header carries the same client token.
            const base = rules.find((rule) => rule.path_prefix === '/') ?? rules[0];
            panel.append(
              el(
                'p',
                `Base URL: ${location.origin}${route.local_prefix} · one client token and one grant cover every subpath.`,
              ),
              el(
                'ul',
                ...rules.map((rule) =>
                  el(
                    'li',
                    `${joinLocalPrefix(route.local_prefix, rule.path_prefix)} · client ${authLabel(rule.caller_auth)}`,
                  ),
                ),
              ),
              el(
                'pre',
                callerCurl(
                  {
                    ...route,
                    caller_auth: base.caller_auth,
                    local_prefix: joinLocalPrefix(route.local_prefix, base.path_prefix),
                  },
                  location.origin,
                ),
              ),
            );
          } else
            panel.append(
              el(
                'p',
                `Base URL: ${location.origin}${route.local_prefix} · Header: ${route.caller_auth.header}: ${JSON.stringify(route.caller_auth.prefix)} + <token>`,
              ),
              el('pre', callerCurl(route, location.origin)),
            );
        }
        const dismiss = button('I saved it — dismiss', () => {
          token.value = '';
          panel.querySelectorAll('textarea').forEach((text) => {
            text.value = '';
          });
          context.protectToken(false);
          panel.remove();
          void context.run(async () => {
            await context.refresh(`caller:${result.caller.id}`);
            context.announce('Client created. The one-time token has been dismissed.');
          });
        });
        const footer = actions(dismiss);
        footer.classList.add('client-token-footer');
        panel.append(footer);
        // Only token actions remain reachable until explicit dismissal/navigation.
        view.inert = false;
        view.replaceChildren(intro, panel);
        token.focus();
        token.select();
        context.announce(
          `Created client ${result.caller.name}. Copy the token now; it is shown once.`,
        );
      } finally {
        view.inert = false;
        if (!issued) context.protectToken(false);
      }
    },
    'Create client',
  );
  if (!approvable && state.routes.length) create.disabled = true;
  const cancel = button('Cancel', () => setComposer(false, true));
  const composerFooter = actions(create, cancel);
  composerFooter.classList.add('client-composer-actions');
  form.append(composerFooter);
  if (!state.routes.length) {
    const note = el(
      'p',
      'Add a route before creating a client. ',
      button('Go to routes', () => context.navigate('Routes')),
    );
    note.className = 'client-composer-note';
    form.prepend(note);
  }
  const newClient = button('New client', () =>
    setComposer(newClient.getAttribute('aria-expanded') !== 'true', true),
  );
  newClient.className = 'primary';
  newClient.setAttribute('aria-controls', form.id);
  const counts = el(
    'span',
    `${active.length} active${revoked.length ? ` · ${revoked.length} revoked` : ''}`,
  );
  counts.className = 'client-counts';
  const toolbar = el('div', newClient, counts);
  toolbar.className = 'route-cmd-bar';
  function setComposer(open: boolean, moveFocus: boolean) {
    form.hidden = !open;
    newClient.setAttribute('aria-expanded', String(open));
    if (!open) {
      name.value = '';
      providers.reset();
    }
    if (moveFocus) (open ? name : newClient).focus();
  }
  // With nothing to manage yet, creating a client is the only useful action.
  setComposer(!active.length, false);
  view.append(toolbar, form);

  // The editor is built on expand so collapsed rows hold no form controls.
  function approvalEditor(caller: ProxyCaller, label: string, collapse: () => void): HTMLElement[] {
    const approvals = new Map(
      groups.map((group) => [group.id, providerApproval(group, caller)] as const),
    );
    const chosen = providerChips(groups, approvals, 'Provider approvals', unavailable);
    const editor = el(
      'form',
      chosen.fields,
      el(
        'p',
        'Approval covers every current route in the provider. The token stays valid when approvals change. Unchecking revokes access to every route in that provider; dispatched requests may continue. Existing partial approvals stay unchanged unless you change them.',
      ),
    );
    submit(
      editor,
      context,
      async () => {
        const changes = chosen.changes();
        if (!changes.length) {
          context.announce(
            'No provider approval changes to save. Partial approvals are unchanged.',
          );
          return;
        }
        const removed = changes
          .filter((change) => !change.approved)
          .flatMap((change) => groups.filter((group) => group.id === change.id));
        if (removed.length) {
          const providers = removed.map((group) => group.label).join(', ');
          const routes = removed.flatMap((group) => group.routes.map((route) => route.name));
          if (
            !window.confirm(
              `Remove ${label}’s approval for ${providers}? ${removed.length === 1 ? 'All of its' : 'All of their'} routes (${routes.join(', ')}) are revoked for new requests; dispatched requests may continue. The token is not reissued.`,
            )
          )
            return;
        }
        await api.updateProviderApprovals(caller.id, changes, state.revision);
        await context.refresh(`caller:${caller.id}`);
        context.announce(`Saved provider approvals for ${label}. The token was not changed.`);
      },
      'Save approvals',
    );
    const revoke = confirmAction(
      'Revoke…',
      async () => {
        await api.revokeCaller(caller.id);
        await context.refresh();
        context.announce(
          `Revoked ${label}. New requests are blocked; dispatched requests may continue.`,
        );
      },
      context,
      `Revoke ${label}? This token can never make new requests again. Dispatched requests may continue until finish or deadline. Create a new client to restore access.`,
    );
    revoke.setAttribute('aria-label', `Revoke ${label}`);
    revoke.classList.add('client-revoke');
    editor.append(button('Cancel', collapse), revoke);
    const meta = el(
      'p',
      `Created ${dateTime(caller.created, 'Unknown')} · Last used ${dateTime(caller.last_used)} · ID ${caller.id}`,
    );
    meta.className = 'client-meta';
    return [meta, editor];
  }
  let closeOpen: (() => void) | undefined;
  function activeRow(caller: ProxyCaller): HTMLElement {
    const label = clientLabel(caller, suffixes);
    const panel = el('div');
    panel.className = 'client-detail';
    const row = clientRow(caller, suffixes, keyNames, groups, {
      panel,
      toggle: (open) => setOpen(open),
    });
    const toggle = () => row.querySelector<HTMLElement>('.client-toggle');
    function setOpen(open: boolean) {
      if (open && closeOpen) closeOpen();
      panel.replaceChildren(
        ...(open
          ? approvalEditor(caller, label, () => {
              setOpen(false);
              toggle()?.focus();
            })
          : []),
      );
      setRowExpanded(row, open);
      closeOpen = open ? () => setOpen(false) : undefined;
    }
    return row;
  }
  if (active.length) {
    const list = el('div', clientListHeader(), ...active.map(activeRow));
    list.className = 'client-list';
    view.append(list);
  }
  if (revoked.length)
    view.append(revokedClients(revoked, suffixes, keyNames, groups, api, context));
  return view;
}

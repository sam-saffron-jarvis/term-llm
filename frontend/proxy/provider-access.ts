import {
  ProxyAPIError,
  type ProxyCaller,
  type ProxyClient,
  type ProxyRoute,
  type ProxyState,
} from '../src/api/proxy-client';
import { selectCopyField } from './clipboard';
import { routeAllowsClientApproval, routeNativeProvider, routeSharedNativeAccount } from './native';
import { actions, button, check, dateTime, el, field, input, submit, type UIContext } from './dom';
import { clientLabel, duplicateSuffixes, lastSeen, sortCallers } from './client-list';
import { termLLMConfigSection, tokenCopyRow } from './client-config-snippet';
import { authLabel, joinLocalPrefix, routeRules } from './route-rules';
import { providerApproval, providerName, type ProviderGroup } from './provider-groups';
import { callerCurl } from './security';

// Keep endpoint instructions separate from approval: the token covers the
// provider, while individual integrations still need their own base URL.
function connectionDetails(route: ProxyRoute): HTMLElement {
  const rules = routeRules(route);
  if (!rules.length) {
    const details = el(
      'div',
      el(
        'p',
        `Base URL: ${location.origin}${route.local_prefix} · Header: ${route.caller_auth.header}: ${JSON.stringify(route.caller_auth.prefix)} + <token>`,
      ),
      el('pre', callerCurl(route, location.origin)),
    );
    return details;
  }
  // A unified route has no parent client auth; each subpath declares which
  // header carries the same client token.
  const base = rules.find((rule) => rule.path_prefix === '/') ?? rules[0];
  return el(
    'div',
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
}

/** Client approval is managed once for the whole configured provider. */
export function providerAccess(
  group: ProviderGroup,
  state: ProxyState,
  api: ProxyClient,
  context: UIContext,
): HTMLElement {
  let current = state;
  const focusKey = `route:${group.routes[0].name}`;
  const native = group.routes.map((route) => routeNativeProvider(route, state)).find(Boolean);
  const canApprove = () => group.routes.every((route) => routeAllowsClientApproval(route, current));
  let tokenShown = false;

  const panel = el('div');
  panel.className = 'route-access-panel';
  const view = el('div', panel);
  view.className = 'route-access provider-section';

  function clients(): ProxyCaller[] {
    return current.callers.filter(
      (caller) => !caller.revoked && providerApproval(group, caller) !== 'none',
    );
  }

  // A refresh would drop another row's show-once token, so refuse to start.
  function tokenGuard(message: string): boolean {
    if (!context.hasProtectedToken?.()) return false;
    context.announce(message);
    return true;
  }

  async function mutate<T>(operation: () => Promise<T>): Promise<T> {
    try {
      return await operation();
    } catch (error) {
      if (error instanceof ProxyAPIError && error.status === 409) {
        // Never replay a stale write. Reload the authoritative revision so the
        // reopened panel shows the grants that actually exist now.
        try {
          await context.refresh(focusKey);
        } catch {
          // The conflict message still tells the operator to refresh.
        }
      }
      throw error;
    }
  }

  function removeAccess(caller: ProxyCaller, name: string): HTMLButtonElement {
    const remove = button('Remove', () => {
      if (tokenGuard('Save or dismiss the open one-time client token before removing access.'))
        return;
      const sharedNote = group.routes.some(
        (route) => caller.grants.includes(route.name) && routeSharedNativeAccount(route, current),
      )
        ? ` The shared ${native?.shortName ?? 'subscription'} sign-in and other clients’ access are unchanged.`
        : '';
      if (
        !window.confirm(
          `Remove ${name}’s access to ${providerName(group)}? Access to other providers and its token stay unchanged. New requests to ${providerName(group)} will be blocked; dispatched requests may continue.${sharedNote}`,
        )
      )
        return;
      void context.run(async () => {
        await mutate(() =>
          api.updateProviderApprovals(
            caller.id,
            [{ id: group.id, approved: false }],
            current.revision,
          ),
        );
        await context.refresh(focusKey);
        context.announce(`Removed ${name}’s access to ${providerName(group)}.`);
      }, remove);
    });
    remove.className = 'danger route-access-remove';
    remove.setAttribute('aria-label', `Remove ${name}’s access to ${providerName(group)}`);
    return remove;
  }

  function currentAccess(): HTMLElement {
    const granted = sortCallers(clients());
    // Includes clients still needing approval; each is marked below.
    const heading = el('h3', 'Clients with access');
    const section = el('div', heading);
    section.className = 'route-access-current';
    if (!granted.length) {
      const empty = el('p', 'No clients approved yet.');
      empty.className = 'route-access-empty';
      section.append(empty);
      return section;
    }
    const count = el('span', String(granted.length));
    count.className = 'route-access-count';
    heading.append(' ', count);
    const suffixes = duplicateSuffixes(current.callers);
    const list = el('ul');
    list.className = 'route-access-clients';
    for (const caller of granted) {
      const label = clientLabel(caller, suffixes);
      const name = el('span', el('strong', label));
      name.className = 'route-access-name';
      if (providerApproval(group, caller) === 'partial') {
        const pending = el('span', 'Needs approval');
        pending.className = 'route-access-pending';
        pending.title = 'Approved before this provider gained a route; approve again to cover it.';
        name.append(pending);
      }
      // Client-wide activity: the token's last use on any provider.
      const seen = el('small', `Last seen ${lastSeen(caller.last_used)}`);
      seen.title = caller.last_used ? dateTime(caller.last_used) : 'Never used';
      const item = el('li', name, seen, actions(removeAccess(caller, label)));
      item.className = 'route-access-client';
      list.append(item);
    }
    section.append(list);
    return section;
  }

  // Approval writes are per client and bump the revision, so a batch re-reads
  // the revision between writes without re-rendering the open panel.
  async function approveAll(chosen: ProxyCaller[]) {
    let approved = 0;
    try {
      for (const caller of chosen) {
        if (approved) {
          const latest = await context.refresh(undefined, true);
          if (!latest) throw new Error('Proxy state changed; review approvals and try again.');
          current = latest;
        }
        await mutate(() =>
          api.updateProviderApprovals(
            caller.id,
            [{ id: group.id, approved: true }],
            current.revision,
          ),
        );
        approved++;
      }
    } catch (error) {
      // Show what was approved before the failure instead of a stale list.
      if (approved) await context.refresh(focusKey).catch(() => undefined);
      throw error;
    }
    await context.refresh(focusKey);
    const names = chosen.map((caller) => caller.name).join(', ');
    context.announce(
      `Approved ${names} for ${providerName(group)}. ${chosen.length === 1 ? 'Its token and other approvals are' : 'Their tokens and other approvals are'} unchanged.`,
    );
  }

  function grantExisting(): HTMLElement | undefined {
    const eligible = sortCallers(
      current.callers.filter(
        (caller) => !caller.revoked && providerApproval(group, caller) !== 'approved',
      ),
    );
    if (!eligible.length) return undefined;
    const suffixes = duplicateSuffixes(current.callers);
    const choices = eligible.map((caller) => {
      const label = clientLabel(caller, suffixes);
      const [chip, control] = check(
        providerApproval(group, caller) === 'partial' ? `${label} (needs approval)` : label,
        false,
      );
      chip.classList.add('grant-chip');
      chip.title = caller.last_used ? `Last used ${dateTime(caller.last_used)}` : 'Never used';
      return { caller, control, chip };
    });
    const chips = el('div', ...choices.map((choice) => choice.chip));
    chips.className = 'grant-chips';
    const fields = el('fieldset', el('legend', 'Approve clients'), chips);
    fields.className = 'client-grant-fields';
    const form = el('form', fields);
    form.className = 'route-access-grant';
    submit(
      form,
      context,
      async () => {
        if (
          tokenGuard(
            'Save or dismiss the open one-time client token before approving provider access.',
          )
        )
          return;
        // The submit guard disables controls but leaves their checked state.
        const chosen = choices.filter((choice) => choice.control.checked);
        if (!chosen.length) throw new Error('Select at least one client to approve.');
        await approveAll(chosen.map((choice) => choice.caller));
      },
      'Approve selected',
    );
    return form;
  }

  function showToken(value: string, caller: ProxyCaller) {
    tokenShown = true;
    const token = input(value);
    token.readOnly = true;
    token.className = 'caller-token';
    token.spellcheck = false;
    const dismiss = button('I saved it — dismiss', () => {
      void context.run(async () => {
        // The token is unrecoverable after this point: clear every copy in the
        // DOM before attempting the authoritative read.
        token.value = '';
        panel.querySelectorAll('textarea').forEach((text) => {
          text.value = '';
        });
        context.protectToken(false);
        tokenShown = false;
        renderBody();
        try {
          await context.refresh(focusKey);
          context.announce(`Client ${caller.name} created. The one-time token has been dismissed.`);
        } catch (error) {
          if (error instanceof ProxyAPIError && error.status === 401) throw error;
          context.announce(
            'Client created. The one-time token was cleared; refresh state before changing access.',
          );
        }
      }, dismiss);
    });
    const heading = el('h4', 'Copy this token now');
    heading.tabIndex = -1;
    const configSnippet = termLLMConfigSection(location.origin, [group], context, {
      heading: 'h4',
      getToken: () => token.value,
      onTokenFallback: () => selectCopyField(token),
    });
    const footer = actions(dismiss);
    footer.classList.add('client-token-footer');
    panel.replaceChildren(
      heading,
      el(
        'p',
        `Shown once. Save it securely before dismissing; it cannot be retrieved later. Access is limited to the current routes for ${providerName(group)}.`,
      ),
      tokenCopyRow(token, context),
      ...(configSnippet ? [configSnippet] : []),
      ...group.routes.map(connectionDetails),
      el(
        'p',
        'For curl examples, set TOKEN in your shell to this client token, not the administrator token.',
      ),
      el(
        'p',
        'For apps configured manually, point only this provider’s base URL at the proxy. Do not set a global HTTP_PROXY or HTTPS_PROXY; a global proxy variable would reroute other providers.',
      ),
      footer,
    );
    token.focus();
    token.select();
    context.announce(`Created client ${caller.name}. Copy the token now; it is shown once.`);
  }

  // Creating a client is rare once any exist, so it stays behind New client
  // unless there is nothing to approve yet.
  function createClient(open: boolean): HTMLElement {
    const name = input();
    name.required = true;
    name.maxLength = 80;
    const form = el('form', field('Client name', name));
    form.id = `route-access-create-${group.id}`;
    let ready = false;
    const toggle = button('New client', () => setOpen(true));
    toggle.setAttribute('aria-controls', form.id);
    const cancel = button('Cancel', () => {
      setOpen(false);
      toggle.focus();
    });
    function setOpen(next: boolean) {
      form.hidden = !next;
      toggle.hidden = next;
      toggle.setAttribute('aria-expanded', String(next));
      if (!next) name.value = '';
      else if (ready) name.focus();
    }
    submit(
      form,
      context,
      async () => {
        if (
          tokenGuard(
            'Save or dismiss the open one-time client token before creating another client.',
          )
        )
          return;
        const requested = name.value.trim();
        if (!requested) throw new Error('Enter a client name.');
        context.protectToken(true, true);
        let issued = false;
        try {
          context.announce('Creating client… wait for the one-time token.');
          const result = await mutate(() =>
            api.createProviderCaller(requested, [group.id], current.revision),
          );
          issued = true;
          context.protectToken(true);
          showToken(result.token, result.caller);
          try {
            // Reconcile revision/CSRF without discarding the only copy of the
            // token: a normal refresh would replace this whole view.
            const latest = await context.refresh(undefined, true);
            if (latest) current = latest;
            context.announce(
              `Created client ${result.caller.name}. Copy the token now; it is shown once.`,
            );
          } catch (error) {
            if (error instanceof ProxyAPIError && error.status === 401) throw error;
            context.announce(
              'Client created. Save or dismiss the token now; state refresh is unavailable.',
            );
          }
        } finally {
          if (!issued) context.protectToken(false);
        }
      },
      'Create client',
    );
    // Only the initially open form (no clients yet) has nothing to cancel to.
    if (!open) form.append(cancel);
    setOpen(open);
    ready = true;
    const section = el('div', toggle, form);
    section.className = 'route-access-create';
    return section;
  }

  function renderBody() {
    if (tokenShown) return;
    const parts: HTMLElement[] = [];
    if (canApprove()) {
      // Adding is the action; it stays above a list that can grow long.
      const add = el('div');
      add.className = 'route-access-add';
      const existing = grantExisting();
      if (existing) add.append(existing);
      if (!native) add.append(createClient(!current.callers.some((caller) => !caller.revoked)));
      if (add.childElementCount) parts.push(add);
    }
    parts.push(currentAccess());
    const scope = el(
      'p',
      'Approval covers every current route of this provider. Tokens and access to other providers are unchanged.',
    );
    scope.className = 'provider-note';
    parts.push(scope);
    if (current.mode === 'file') {
      const note = el(
        'p',
        'Routes are file-managed. Approvals are editable, but a configuration reload may drop incompatible grants.',
      );
      note.className = 'provider-note';
      parts.push(note);
    }
    if (!canApprove()) {
      const note = el(
        'p',
        'Native routes must use an enabled, signed-in shared subscription before clients can be approved.',
      );
      note.className = 'provider-note';
      parts.push(note);
    }
    if (native) {
      const hasShared = group.routes.some((route) => routeSharedNativeAccount(route, current));
      if (hasShared) {
        const note = el(
          'p',
          `Workers use the shared ${native.shortName} sign-in. Removing a client’s access does not sign out ${native.shortName} or affect other clients.`,
        );
        note.className = 'provider-note';
        parts.push(note);
      }
      if (current.mode === 'ui' && canApprove()) {
        const note = el(
          'p',
          `${native.label} uses a device launcher instead of a generic API URL. Use Add device under Shared subscription to connect a new device.`,
        );
        note.className = 'provider-note';
        parts.push(note);
      }
    }
    panel.replaceChildren(...parts);
  }

  renderBody();
  return view;
}

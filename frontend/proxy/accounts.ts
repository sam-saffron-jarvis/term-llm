import { nativeProvider } from './native';
import type {
  ProxyAccount,
  ProxyAccountInput,
  ProxyClient,
  ProxyState,
} from '../src/api/proxy-client';
import {
  actions,
  button,
  card,
  clearPassword,
  dateTime,
  el,
  field,
  input,
  select,
  submit,
  type UIContext,
} from './dom';

// Accept credentials only, never operator-only issuer/origin/client overrides.
// Errors deliberately omit pasted content and JSON parser exception messages.
export function parseAccountCredentials(raw: string): ProxyAccountInput['credentials'] {
  if (raw.length > 65536) throw new Error('Credential paste is too large.');
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    throw new Error('Paste a valid credential JSON object.');
  }
  if (!value || typeof value !== 'object' || Array.isArray(value))
    throw new Error('Paste a credential JSON object.');
  const strings = [
    'access_token',
    'refresh_token',
    'github_token',
    'account_id',
    'access_key_id',
    'secret_access_key',
    'session_token',
  ];
  for (const [key, v] of Object.entries(value)) {
    const valid =
      key === 'mode'
        ? v === 'token_only' || v === 'rotating'
        : strings.includes(key)
          ? typeof v === 'string' && v.length <= 16384 && !/[\r\n\0]/.test(v)
          : key === 'expires_at'
            ? typeof v === 'number' && Number.isSafeInteger(v) && v >= 0 && v <= 253402300799
            : key === 'scopes' &&
              Array.isArray(v) &&
              v.length <= 32 &&
              v.every(
                (scope) =>
                  typeof scope === 'string' &&
                  scope.length > 0 &&
                  scope.length <= 128 &&
                  !/\s/.test(scope),
              );
    if (!valid)
      throw new Error(
        'Credentials contain unsupported fields or invalid values. Only credential fields are accepted; issuer and origin overrides are operator-only.',
      );
  }
  if (!Object.keys(value).length) throw new Error('Paste credentials before saving.');
  return value as ProxyAccountInput['credentials'];
}

export function accountStateLabel(account: ProxyAccount): string {
  return account.state.replaceAll('_', ' ');
}

export const accountKinds: [ProxyAccount['kind'], string][] = [
  ['chatgpt', 'ChatGPT'],
  ['grok', 'Grok'],
  ['copilot', 'GitHub Copilot'],
  ['aws-sigv4', 'AWS signing'],
];

export interface AccountDeps {
  state: ProxyState;
  api: ProxyClient;
  context: UIContext;
}

export interface AccountFormOptions {
  /** Replace this account's credentials. */
  account?: ProxyAccount;
  /** Fix the kind of a new account; otherwise the operator chooses. */
  kind?: ProxyAccount['kind'];
  /** Suggested name for a new account. */
  name?: string;
  /** The name is fixed, e.g. a route already references it. */
  fixedName?: boolean;
  stored: (account: ProxyAccount) => void | Promise<void>;
  cancel: () => void;
}

// Write-only credential paste for one trusted account. The paste is cleared
// before fresh passkey authentication and is never echoed back.
export function accountForm(deps: AccountDeps, options: AccountFormOptions): HTMLElement {
  const { api, context } = deps;
  const { account } = options;
  const name = input(account?.name ?? options.name);
  name.required = true;
  name.readOnly = !!account || !!options.fixedName;
  name.pattern = '[A-Za-z0-9][A-Za-z0-9_-]{0,63}';
  const kind = select(accountKinds, account?.kind ?? options.kind ?? 'chatgpt');
  kind.disabled = !!account || !!options.kind;
  const region = input();
  const regionField = field('AWS region', region, 'For AWS signing only, for example us-east-1.');
  const credentials = input('', 'password');
  credentials.required = true;
  credentials.maxLength = 65536;
  credentials.spellcheck = false;
  credentials.setAttribute('autocapitalize', 'off');
  credentials.setAttribute('autocomplete', 'off');
  credentials.className = 'account-credential-paste';
  const requirements = el('p');
  function updateKind() {
    const aws = kind.value === 'aws-sigv4';
    regionField.hidden = !aws;
    region.required = aws && !account;
    requirements.textContent =
      kind.value === 'copilot'
        ? 'JSON fields: github_token.'
        : aws
          ? 'JSON fields: access_key_id, secret_access_key, optional session_token.'
          : 'JSON fields: access_token, refresh_token, expires_at (Unix seconds, not milliseconds), account_id.';
  }
  kind.addEventListener('change', updateKind);
  updateKind();
  const form = el(
    'form',
    field('Account name', name),
    field('Account kind', kind),
    regionField,
    requirements,
    field(
      'Paste credential JSON (write-only)',
      credentials,
      'Credential fields only. Never returned or saved in browser storage.',
    ),
    el('p', 'Stored and refreshed only on this proxy.'),
  );
  submit(
    form,
    context,
    async () => {
      const raw = credentials.value;
      clearPassword(credentials);
      const parsed = parseAccountCredentials(raw);
      const payload: ProxyAccountInput = {
        name: name.value.trim(),
        kind: kind.value as ProxyAccount['kind'],
        credentials: parsed,
        ...(account ? { replace: true } : {}),
        ...(region.value ? { region: region.value.trim() } : {}),
      };
      if (
        account &&
        !window.confirm(
          `Replace credentials for ${account.name}? Open account connections will close.`,
        )
      )
        return;
      const result = await api.saveAccount(payload);
      await options.stored(result.account);
      context.announce(
        'Account credentials stored. They are not returned or saved in browser storage.',
      );
    },
    account ? 'Replace account credentials' : 'Store account credentials',
  );
  form.append(
    button('Cancel account edit', () => {
      clearPassword(credentials);
      options.cancel();
    }),
  );
  return card(account ? `Reconnect ${account.name}` : 'Connect a trusted account', form);
}

function mergeAccount(state: ProxyState, account: ProxyAccount) {
  state.accounts = [...(state.accounts ?? []).filter((a) => a.name !== account.name), account].sort(
    (a, b) => a.name.localeCompare(b.name),
  );
}

// Credential maintenance for one account, mounted wherever the account is used.
// Routes sharing the account each show it, and every action names them all.
export function accountControls(
  deps: AccountDeps,
  account: ProxyAccount,
  usedBy: string[] = [],
  focusKey?: string,
): HTMLElement {
  const { api, context } = deps;
  let current = account;
  const status = el('p');
  status.className = 'account-controls-status';
  const metadata = el('small');
  metadata.className = 'account-controls-meta';
  function renderStatus() {
    status.replaceChildren(
      el('strong', current.name),
      ` · ${current.kind} · ${accountStateLabel(current)}`,
    );
    metadata.textContent = `Updated ${dateTime(current.updated)}${current.expires_at ? ` · Expires ${dateTime(current.expires_at * 1000)}` : ''}`;
  }
  renderStatus();
  const shared = usedBy.length > 1 ? el('p', `Shared by routes ${usedBy.join(', ')}.`) : '';
  const affected = usedBy.length ? ` Routes ${usedBy.join(', ')} use it.` : '';
  const editor = el('div');
  const container = el('div');
  container.className = 'account-controls';
  const stored = async (next: ProxyAccount) => {
    current = next;
    mergeAccount(deps.state, next);
    editor.replaceChildren();
    renderStatus();
    replace.focus();
    // Route summaries derive readiness from account state.
    await context.refresh(focusKey);
  };
  const replace = button('Replace credentials', () => {
    editor.replaceChildren(
      accountForm(deps, {
        account: current,
        stored,
        cancel: () => {
          editor.replaceChildren();
          replace.focus();
        },
      }),
    );
    editor.querySelector<HTMLInputElement>('input[type=password]')?.focus();
  });
  const signOut = button('Sign out account', () => {
    if (
      !window.confirm(
        `Sign out ${current.name}?${affected} New account-backed requests will fail until you reconnect it. Open account connections will close.`,
      )
    )
      return;
    void context.run(async () => {
      const result = await api.signOutAccount(current.name);
      await stored(result.account);
      context.announce('Account signed out. No credentials were returned.');
    }, signOut);
  });
  const buttons = [replace, signOut];
  // Only an account no route uses can be removed; that frees its name.
  if (!usedBy.length) {
    const remove = button('Remove account', () => {
      if (
        !window.confirm(
          `Remove ${current.name}? Its stored sign-in is deleted and the name becomes free to reuse.`,
        )
      )
        return;
      void context.run(async () => {
        await api.removeAccount(current.name);
        deps.state.accounts = (deps.state.accounts ?? []).filter((a) => a.name !== current.name);
        container.remove();
        await context.refresh();
        context.announce(`Account ${current.name} removed.`);
      }, remove);
    });
    remove.className = 'danger';
    buttons.push(remove);
  }
  container.append(status, metadata, shared, actions(...buttons), editor);
  return container;
}

// A route names an account that does not exist yet (file-managed routes, or
// a sign-out followed by removal). Connect it under that exact name.
export function connectAccount(
  deps: AccountDeps,
  name: string,
  kind: ProxyAccount['kind'] | undefined,
): HTMLElement {
  const container = el('div');
  container.className = 'account-controls';
  const editor = el('div');
  const connect = button(`Connect account ${name}`, () => {
    if (editor.childElementCount) {
      editor.querySelector<HTMLInputElement>('input[type=password]')?.focus();
      return;
    }
    editor.replaceChildren(
      accountForm(deps, {
        name,
        kind,
        fixedName: true,
        stored: async (account) => {
          mergeAccount(deps.state, account);
          await deps.context.refresh();
        },
        cancel: () => {
          editor.replaceChildren();
          connect.focus();
        },
      }),
    );
    editor.querySelector<HTMLInputElement>('input[type=password]')?.focus();
  });
  container.append(el('p', `Account ${name} is not connected.`), actions(connect), editor);
  return container;
}

// Opens the credential form in a modal so an unsaved route draft survives.
export function accountDialog(
  deps: AccountDeps,
  host: HTMLElement,
  kind: ProxyAccount['kind'],
  stored: (account: ProxyAccount) => void,
) {
  const dialog = el('dialog');
  dialog.setAttribute('aria-label', 'Connect account');
  const taken = new Set((deps.state.accounts ?? []).map((a) => a.name));
  let name: string = kind;
  for (let n = 2; taken.has(name); n++) name = `${kind}-${n}`;
  dialog.append(
    accountForm(deps, {
      name,
      kind,
      stored: (account) => {
        mergeAccount(deps.state, account);
        stored(account);
        dialog.close();
      },
      cancel: () => dialog.close(),
    }),
  );
  dialog.addEventListener('close', () => dialog.remove());
  host.append(dialog);
  dialog.showModal();
  dialog.querySelector<HTMLInputElement>('input[type=password]')?.focus();
}

// Accounts no route references. Native sign-ins are managed with their routes.
export function unusedAccountsView(deps: AccountDeps): HTMLElement | undefined {
  const used = new Set(deps.state.routes.map((route) => route.account).filter(Boolean));
  const unused = (deps.state.accounts ?? []).filter(
    (account) => !nativeProvider(account.kind) && !used.has(account.name),
  );
  if (!unused.length) return undefined;
  const view = card(
    'Unused accounts',
    el('p', 'No route uses these accounts. Remove any you no longer need.'),
    ...unused.map((account) => accountControls(deps, account)),
  );
  view.classList.add('unused-accounts');
  return view;
}

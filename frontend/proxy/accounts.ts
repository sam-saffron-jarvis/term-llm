import type {
  ProxyAccount,
  ProxyAccountInput,
  ProxyClient,
  ProxyState,
} from '../src/api/proxy-client';
import type { PasskeyPlatform } from '../src/hub/platform/passkeys';
import {
  actions,
  button,
  card,
  dateTime,
  disclosure,
  el,
  field,
  input,
  select,
  submit,
  type UIContext,
} from './dom';
import { assertPasskey } from './passkeys';

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

// Kept under a disclosure on Providers/Routes, not on Home or a new top-level tab.
export function accountsView(
  state: ProxyState,
  api: ProxyClient,
  context: UIContext,
  platform: PasskeyPlatform,
): HTMLElement {
  const list = el('div');
  list.className = 'account-list';
  const editor = el('div');
  const view = disclosure(
    'Trusted provider accounts',
    el(
      'p',
      'Keep renewable credentials on this box, not on your devices. Account metadata below contains no credentials. Advanced setup; API keys use the normal Add API key form.',
    ),
    list,
    editor,
  );
  let accounts = state.accounts ?? [];
  async function fresh() {
    if (state.auth === 'passkey') await assertPasskey(api, platform, 'reauth');
  }
  function render() {
    list.replaceChildren();
    if (!accounts.length) list.append(el('p', 'No trusted accounts connected yet.'));
    for (const account of accounts) {
      const controls = actions(button('Replace credentials', () => edit(account)));
      const signOut = button('Sign out account', () => {
        if (
          !window.confirm(
            `Sign out ${account.name}? New account-backed requests will fail until you reconnect it. Open account connections will close.`,
          )
        )
          return;
        void context.run(async () => {
          await fresh();
          const result = await api.signOutAccount(account.name);
          accounts = accounts.map((a) => (a.name === account.name ? result.account : a));
          render();
          list.querySelector('h2')?.focus();
          context.announce('Account signed out. No credentials were returned.');
        }, signOut);
      });
      controls.append(signOut);
      const row = card(
        account.name,
        el('p', `${account.kind} · ${account.state.replaceAll('_', ' ')}`),
        el(
          'small',
          `Updated ${dateTime(account.updated)}${account.expires_at ? ` · Credential expires ${dateTime(account.expires_at * 1000)}` : ''}`,
        ),
        controls,
      );
      list.append(row);
    }
    list.append(button('Add trusted account', () => edit()));
  }
  function edit(account?: ProxyAccount) {
    const name = input(account?.name);
    name.required = true;
    name.readOnly = !!account;
    name.pattern = '[A-Za-z0-9][A-Za-z0-9_-]{0,63}';
    const kind = select(
      [
        ['chatgpt', 'ChatGPT'],
        ['claude-code', 'Claude Code'],
        ['grok', 'Grok'],
        ['copilot', 'GitHub Copilot'],
        ['aws-sigv4', 'AWS signing'],
      ],
      account?.kind ?? 'chatgpt',
    );
    kind.disabled = !!account;
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
            : `JSON fields: access_token, refresh_token, expires_at (Unix seconds, not milliseconds)${kind.value === 'claude-code' ? ', optional scopes' : ', account_id'}.`;
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
        'Never prefilled, saved in browser storage or returned by this API. Paste only credential fields, not a whole settings file. Cleared before the passkey prompt or any network operation.',
      ),
      el(
        'p',
        'Provider sign-in and refresh happen on this trusted box. This form does not guess subscription fees or plan allowances.',
      ),
    );
    submit(
      form,
      context,
      async () => {
        const raw = credentials.value;
        credentials.value = '';
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
        await fresh();
        const result = await api.saveAccount(payload);
        accounts = [...accounts.filter((a) => a.name !== result.account.name), result.account].sort(
          (a, b) => a.name.localeCompare(b.name),
        );
        editor.replaceChildren();
        render();
        context.announce(
          'Account credentials stored. They are not returned or saved in browser storage.',
        );
      },
      account ? 'Replace account credentials' : 'Store account credentials',
    );
    form.append(
      button('Cancel account edit', () => {
        credentials.value = '';
        editor.replaceChildren();
      }),
    );
    editor.replaceChildren(
      card(account ? `Reconnect ${account.name}` : 'Connect a trusted account', form),
    );
    name.focus();
  }
  render();
  return view;
}

import {
  ProxyClient,
  type Assertion,
  type Enrollment,
  type ProxyAuthStatus,
} from '../src/api/proxy-client';
import { browserPasskeyPlatform, type PasskeyPlatform } from '../src/hub/platform/passkeys';
import {
  actions,
  button,
  clearPassword,
  card,
  dateTime,
  el,
  field,
  input,
  submit,
  type UIContext,
} from './dom';

// Browser exceptions may contain arbitrary credential data. Never display their message.
async function browserOperation<T>(operation: () => Promise<T>): Promise<T> {
  try {
    return await operation();
  } catch (error) {
    // Do not retain browser exception data: it may contain serialized credentials.
    // eslint-disable-next-line preserve-caught-error
    throw new Error(
      error instanceof DOMException && error.name === 'NotAllowedError'
        ? 'The passkey prompt was cancelled or timed out.'
        : 'The browser could not complete the passkey operation. Try again with a supported browser and passkey.',
    );
  }
}
export async function assertPasskey(api: ProxyClient, platform: PasskeyPlatform, kind: Assertion) {
  if (!platform.available())
    throw new Error(
      'Passkeys are not available in this browser. Use a supported browser on the configured HTTPS origin.',
    );
  const options = await api.beginAssertion(kind);
  const credential = await browserOperation(() => platform.get(options));
  await api.finishPasskey(kind, credential);
}
async function enroll(api: ProxyClient, platform: PasskeyPlatform, kind: Enrollment, name: string) {
  const options = await api.beginEnrollment(kind, name);
  const credential = await browserOperation(() => platform.create(options));
  await api.finishPasskey(kind, credential);
}

export function passkeyLogin(
  status: ProxyAuthStatus,
  api: ProxyClient,
  context: UIContext,
  signedIn: () => Promise<void>,
  recovered: () => Promise<void>,
  platform: PasskeyPlatform = browserPasskeyPlatform(),
  resumeRecovery = true,
): HTMLElement {
  const view = card(status.setup_required ? 'Set up passkey' : 'Sign in with a passkey');
  view.classList.add('passkey-auth');
  view.append(el('p', `Passkeys for ${status.origin}`));
  if (status.setup_required) {
    view.append(
      el('p', 'Welcome! Create a passkey to protect your proxy settings and upstream credentials.'),
    );
  }
  if (!platform.available()) {
    view.append(
      el(
        'p',
        'Passkeys are not available in this browser. Use a supported browser on the configured HTTPS origin.',
      ),
    );
    return view;
  }
  if (!status.setup_required) {
    const login = button('Sign in with passkey', () => {
      void context.run(async () => {
        await assertPasskey(api, platform, 'login');
        await signedIn();
      }, view);
    });
    login.className = 'primary';
    view.append(login);
  }
  function secretForm(kind: 'bootstrap' | 'recovery') {
    const verifiedKey = kind === 'bootstrap' ? 'bootstrap_verified' : 'recovery_verified';
    let verified = status[verifiedKey];
    const grantNotice = () =>
      el(
        'p',
        'Secret verified. Create your passkey within five minutes; if the prompt is cancelled, retry without re-entering the secret.',
      );
    const token = input('', 'password');
    token.required = true;
    const name = input();
    name.required = true;
    name.maxLength = 80;
    const tokenField = field(
      kind === 'bootstrap' ? 'Bootstrap token' : 'Recovery token',
      token,
      kind === 'bootstrap'
        ? 'Run the cat command shown in the proxy terminal and paste its output here. This one-use secret expires 10 minutes after startup and is not saved in your browser.'
        : 'Paste the private recovery-file secret. It is not saved in browser storage.',
    );
    const form = el(
      'form',
      verified ? grantNotice() : tokenField,
      field(
        'Passkey name',
        name,
        'Choose a name you will recognize later, such as My laptop or My phone.',
      ),
    );
    submit(
      form,
      context,
      async () => {
        if (!verified) {
          const secret = token.value;
          clearPassword(token);
          await api.verifySecret(kind, secret);
          verified = true;
          status[verifiedKey] = true;
          tokenField.replaceWith(grantNotice());
        }
        await enroll(api, platform, kind, name.value.trim());
        if (kind === 'bootstrap') await signedIn();
        else {
          await recovered();
          context.announce(
            'Recovery passkey enrolled. All sessions were invalidated. Sign in with your new passkey, then review and revoke lost passkeys.',
          );
        }
      },
      kind === 'bootstrap' ? 'Create first passkey' : 'Recover with new passkey',
    );
    return form;
  }
  if (status.setup_required) {
    view.append(
      status.bootstrap_available
        ? secretForm('bootstrap')
        : el(
            'p',
            'The setup secret has expired or is unavailable. For automatic local setup, restart the proxy for a fresh secret. For a remote deployment, ask the administrator to provision a bootstrap secret file.',
          ),
    );
  } else if (status.recovery_available) {
    const showRecovery = () => {
      view.replaceChildren(
        el('h2', 'Recover access'),
        el(
          'p',
          'Recovery enrolls a new passkey and invalidates all sessions. The secret expires ten minutes after startup. Then sign in and review lost passkeys.',
        ),
        secretForm('recovery'),
        button('Back to sign in', () => {
          view.replaceWith(
            passkeyLogin(status, api, context, signedIn, recovered, platform, false),
          );
        }),
      );
    };
    if (status.recovery_verified && resumeRecovery) showRecovery();
    else view.append(button('Recover access', showRecovery));
  }
  return view;
}

export function passkeysView(
  api: ProxyClient,
  context: UIContext,
  platform = browserPasskeyPlatform(),
): HTMLElement {
  const view = card('Passkeys');
  const list = el('div', el('p', 'Loading passkeys…'));
  const name = input();
  name.required = true;
  name.maxLength = 80;
  const form = el('form', field('Passkey name', name));
  async function load() {
    const result = await api.credentials();
    list.replaceChildren(
      el('p', `${result.sessions} active ${result.sessions === 1 ? 'session' : 'sessions'}`),
    );
    for (const credential of result.credentials ?? []) {
      const remove = button(`Delete ${credential.name}`, () => {
        if (!window.confirm(`Delete passkey ${credential.name} and sign out its browsers?`)) return;
        void context.run(async () => {
          await assertPasskey(api, platform, 'reauth');
          await api.deleteCredential(credential.id);
          await load();
          context.announce('Passkey deleted.');
        }, view);
      });
      remove.className = 'danger';
      remove.disabled = result.credentials.length <= 1;
      list.append(
        el(
          'article',
          el('h3', credential.name),
          el(
            'p',
            `Created: ${dateTime(credential.created_at)} · Last used: ${dateTime(credential.last_used_at)}`,
          ),
          remove,
        ),
      );
    }
    list.append(el('small', 'The last passkey cannot be deleted.'));
  }
  submit(
    form,
    context,
    async () => {
      await assertPasskey(api, platform, 'reauth');
      await enroll(api, platform, 'enroll', name.value.trim());
      name.value = '';
      await load();
      context.announce('Passkey added.');
    },
    'Add passkey',
  );
  const revoke = button('Revoke other sessions', () => {
    if (!window.confirm('Revoke all other administrator sessions? This session stays signed in.'))
      return;
    void context.run(async () => {
      await assertPasskey(api, platform, 'reauth');
      await api.revokeSessions();
      await load();
      context.announce('Other sessions revoked.');
    }, view);
  });
  view.append(
    el(
      'p',
      'Review and delete any passkey you no longer control. Recovery adds a key; it does not revoke older keys.',
    ),
    list,
    form,
    actions(revoke),
  );
  void context.run(load, view);
  return view;
}

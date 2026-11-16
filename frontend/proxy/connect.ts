import { ProxyAPIError, type ProxyClient, type ProxyState } from '../src/api/proxy-client';
import {
  ClaudeConnectClient,
  ClaudeConnectError,
  createClaudeConnectAdmin,
  newClaudeConnectCapability,
  parseClaudeConnectRequest,
  parseClaudeConnectTicket,
  type ClaudeConnectAdmin,
  type ClaudeConnectCapability,
  type ClaudeConnectReceipt,
  type ClaudeConnectRequest,
  type ClaudeConnectResult,
  type ClaudeConnectTicket,
} from '../src/api/proxy-connect';
import type { PasskeyPlatform } from '../src/hub/platform/passkeys';
import { button, card, check, disclosure, el, field, input, select, type UIContext } from './dom';
import { assertPasskey } from './passkeys';

export interface ClaudeConnectView {
  element: HTMLElement;
  resume(): void;
  dispose(): void;
}
export function connectView(
  state: ProxyState,
  api: ProxyClient & Partial<ClaudeConnectAdmin>,
  context: UIContext,
  platform: PasskeyPlatform,
  options: { client?: ClaudeConnectClient; link?: { id: string; code?: string } } = {},
): ClaudeConnectView {
  const client = options.client ?? new ClaudeConnectClient();
  const admin =
    api.connectRequest && api.connectApprove
      ? (api as ProxyClient & ClaudeConnectAdmin)
      : createClaudeConnectAdmin(api, client);
  const element = card('Connect Claude');
  const notice = el('p');
  notice.role = 'status';
  notice.setAttribute('aria-live', 'polite');
  const body = el('div');
  element.append(notice, body);
  let phase: 'form' | 'preview' | 'approved' | 'uncertain' | 'result' | 'done' | 'terminal' =
    'form';
  let cap: ClaudeConnectCapability | undefined;
  let ticket: ClaudeConnectTicket | undefined;
  let request: ClaudeConnectRequest | undefined;
  let result: ClaudeConnectResult | undefined;
  let submitted: { account: string; client_name: string; action: 'auto' | 'reconnect' } | undefined;
  let busy = false;
  let disposed = false;
  const linked = options.link !== undefined;
  function alive() {
    return !disposed && element.isConnected;
  }
  function guard() {
    context.protectToken(
      !!cap && !['done', 'terminal'].includes(phase) && (phase !== 'result' || !!result?.token),
      busy,
    );
  }
  function terminal(message: string) {
    phase = 'terminal';
    result = undefined;
    notice.textContent = message;
  }
  function bound(
    raw: unknown,
    expected: { id: string; code?: string; origin?: string },
  ): ClaudeConnectRequest {
    const r = parseClaudeConnectRequest(raw);
    if (
      r.id !== expected.id ||
      (expected.code !== undefined && r.code !== expected.code) ||
      r.origin !== window.location.origin ||
      (expected.origin !== undefined && expected.origin !== r.origin)
    )
      throw new ClaudeConnectError();
    if (
      submitted &&
      (r.account !== submitted.account ||
        r.client_name !== submitted.client_name ||
        r.action !== submitted.action ||
        (submitted.action === 'auto' && r.resolved_action === 'reconnect') ||
        (submitted.action === 'reconnect' &&
          !['reconnect', 'collision'].includes(r.resolved_action)))
    )
      throw new ClaudeConnectError();
    return r;
  }
  async function preview(id: string, expected: { id: string; code?: string; origin?: string }) {
    if (!admin.connectRequest) throw new ClaudeConnectError(503);
    const r = bound(await admin.connectRequest(id), expected);
    if (!alive()) return;
    request = r;
    ticket = r;
    if (
      r.status === 'expired' ||
      (['pending', 'approved'].includes(r.status) && r.expires <= Date.now() / 1000)
    )
      terminal(
        'This connection request expired. Start again explicitly from your computer; nothing will be retried automatically.',
      );
    else if (['canceled', 'acknowledged', 'failed'].includes(r.status))
      terminal(`This connection request is ${r.status}. It cannot be approved again.`);
    else if (r.status === 'complete') {
      if (cap) await recover();
      else {
        phase = 'done';
        notice.textContent = 'Connection completed. Return to the requesting computer.';
      }
    } else if (linked && r.status === 'approved') {
      phase = 'approved';
      notice.textContent =
        'This exact request was already approved. Return to the requesting computer; approval is not replayed.';
    } else if (r.resolved_action === 'collision')
      terminal(
        'Existing account or route settings prevent this connection. Nothing was replaced. Review Settings before making another request.',
      );
    else phase = 'preview';
  }
  function frozen(r: ClaudeConnectRequest, previous: ClaudeConnectRequest) {
    return [
      'action',
      'resolved_action',
      'account',
      'source_account',
      'client_name',
      'route',
      'prefix',
    ].every(
      (key) => r[key as keyof ClaudeConnectRequest] === previous[key as keyof ClaudeConnectRequest],
    );
  }
  function acceptReceipt(receipt: ClaudeConnectReceipt) {
    if (receipt.request) {
      const t = parseClaudeConnectTicket(receipt.request);
      if (request && 'resolved_action' in receipt.request && !frozen(receipt.request, request))
        throw new ClaudeConnectError();
      if (
        t.id !== cap?.id ||
        t.origin !== window.location.origin ||
        (ticket && t.code !== ticket.code)
      )
        throw new ClaudeConnectError();
    }
    if (receipt.status === 'complete') {
      if (!request || !receipt.result) throw new ClaudeConnectError();
      const r = receipt.result;
      const gateway = new URL(r.gateway_url);
      const prefix = request.prefix;
      if (
        gateway.origin !== request.origin ||
        gateway.username ||
        gateway.password ||
        gateway.search ||
        gateway.hash ||
        !prefix.startsWith('/') ||
        prefix === '/' ||
        !prefix.endsWith('/') ||
        ![prefix, prefix.slice(0, -1)].includes(gateway.pathname)
      )
        throw new ClaudeConnectError();
      const accountOnly = request.resolved_action === 'reconnect' && request.client_name === '';
      if (accountOnly && r.token !== undefined) throw new ClaudeConnectError();
      if (!accountOnly && !r.token) {
        result = undefined;
        phase = 'done';
        notice.textContent =
          'This connection completed, but its scoped token was already saved or expired. It will not be minted again. Manage the device in Clients if needed.';
        return;
      }
      result = r;
      phase = 'result';
      notice.textContent = accountOnly
        ? 'Claude account reconnected. Existing devices keep their proxy credentials.'
        : 'Connected. Save this scoped proxy token before leaving.';
    } else if (receipt.status === 'acknowledged') {
      result = undefined;
      phase = 'done';
      notice.textContent = 'Saved. The retained device token has been cleared.';
    } else if (
      receipt.status === 'expired' ||
      receipt.status === 'canceled' ||
      receipt.status === 'failed'
    )
      terminal(`This connection request is ${receipt.status}. No new request was created.`);
    else {
      phase = 'uncertain';
      notice.textContent =
        'No completed receipt yet. Check this same request; do not start another connection or retry the commit blindly.';
    }
  }
  async function recover() {
    if (!cap) return;
    phase = 'uncertain';
    try {
      const receipt = await client.status(cap);
      if (!alive()) return;
      if (!request) await previewForRecovery();
      if (!alive()) return;
      acceptReceipt(receipt);
    } catch (error) {
      if (alive() && error instanceof ClaudeConnectError && error.status === 410) {
        terminal('This connection request expired. It cannot be approved or committed again.');
        return;
      }
      if (alive())
        notice.textContent =
          'Could not read the saved receipt. Check this same request again; no connection was restarted and no new token was requested.';
    }
  }
  async function previewForRecovery() {
    if (!cap || !admin.connectRequest) throw new ClaudeConnectError();
    const raw = await admin.connectRequest(cap.id);
    if (!alive()) return;
    request = bound(raw, ticket ?? { id: cap.id, code: parseClaudeConnectTicket(raw).code });
    ticket = request;
  }
  async function run(action: () => Promise<void>) {
    if (busy || disposed) return;
    busy = true;
    guard();
    notice.textContent = '';
    try {
      await context.run(async () => {
        try {
          await action();
        } catch (error) {
          if (!alive()) return;
          if (error instanceof ProxyAPIError && error.status === 401)
            throw new ProxyAPIError(401, 'Please sign in again to approve this connection.');
          notice.textContent =
            error instanceof ClaudeConnectError
              ? error.message
              : 'Could not confirm this connection. No automatic replacement or new request was made.';
        }
      }, element);
    } finally {
      busy = false;
      if (alive()) {
        render();
        guard();
      }
    }
  }
  async function authorizeAndCommit(token = '', terminalCode = '') {
    let approvalSent = false;
    let resolutionChanged = false;
    try {
      if (!request || !ticket) throw new ClaudeConnectError();
      const before = request;
      if (linked && (!/^[a-fA-F0-9]{8}$/.test(terminalCode) || terminalCode !== before.code))
        throw new ClaudeConnectError(400);
      if (before.expires <= Date.now() / 1000) {
        terminal('This connection request expired. It cannot be approved again.');
        return;
      }
      if (before.resolved_action === 'device' && token) throw new ClaudeConnectError(409);
      if (!linked && before.resolved_action !== 'device' && !token)
        throw new ClaudeConnectError(400);
      if (before.status !== 'approved') {
        if (state.auth === 'passkey') await assertPasskey(api, platform, 'reauth');
        if (!alive()) return;
        if (!admin.connectApprove) throw new ClaudeConnectError(503);
        approvalSent = true;
        const approved = bound(
          await admin.connectApprove(before.id, linked ? terminalCode : before.code),
          before,
        );
        if (!alive()) return;
        if (!frozen(approved, before) || approved.status !== 'approved') {
          resolutionChanged = true;
          throw new ClaudeConnectError(409);
        }
        request = approved;
        ticket = approved;
      }
      if (linked) {
        phase = 'approved';
        notice.textContent =
          'Approved for this request and code. Return to the requesting computer to finish.';
        return;
      }
      if (!cap) throw new ClaudeConnectError();
      phase = 'uncertain';
      const committed = client.commit(cap, token || undefined);
      try {
        const receipt = await committed;
        if (alive()) acceptReceipt(receipt);
      } catch {
        if (alive()) await recover();
      }
    } catch (error) {
      if (error instanceof ProxyAPIError && error.status === 401) throw error;
      if (approvalSent && alive()) {
        phase = 'uncertain';
        if (cap) await recover();
        if (resolutionChanged)
          notice.textContent =
            'The connection changed. Your pasted subscription token was not sent. Review this same connection before continuing.';
        else if (linked)
          notice.textContent =
            'Approval could not be confirmed. Check this same request; do not approve a different code.';
      } else throw error;
    }
  }
  function render() {
    body.replaceChildren();
    if (phase === 'form') {
      const name = input('Claude computer');
      const account = input('claude');
      account.required = true;
      account.pattern = '[A-Za-z0-9][A-Za-z0-9_-]{0,63}';
      const action = select(
        [
          ['auto', 'Connect a computer'],
          ['reconnect', 'Reconnect account credentials'],
        ],
        'auto',
      );
      action.addEventListener('change', () => {
        name.value = action.value === 'reconnect' ? '' : 'Claude computer';
      });
      const form = el(
        'form',
        el(
          'p',
          'Connect standalone Claude through this gateway. We will first check whether Claude is already connected; working accounts only need device pairing.',
        ),
        field('Connection', action),
        disclosure(
          'Details',
          field('Account name', account),
          field(
            'Computer name',
            name,
            'Connecting a computer requires a name. Reconnect defaults to blank: existing device credentials stay unchanged. Enter a new name only if also adding a computer.',
          ),
        ),
      );
      form.addEventListener('submit', (event) => {
        event.preventDefault();
        const values = {
          account: account.value.trim(),
          client_name: name.value.trim(),
          action: action.value as 'auto' | 'reconnect',
        };
        if (values.action === 'auto' && !values.client_name) {
          notice.textContent = 'Enter a computer name to connect a device.';
          return;
        }
        void run(async () => {
          submitted = values;
          cap = newClaudeConnectCapability();
          phase = 'uncertain';
          guard();
          try {
            const started = await client.start(cap, values);
            if (!alive()) return;
            ticket = parseClaudeConnectTicket(started);
            if (ticket.id !== cap.id || ticket.origin !== window.location.origin)
              throw new ClaudeConnectError();
            await preview(cap.id, ticket);
          } catch (error) {
            if (alive()) {
              await recover();
              if (phase === 'uncertain' && !notice.textContent)
                notice.textContent =
                  'Start could not be confirmed. Check the same request; do not start another.';
            }
            if (error instanceof ProxyAPIError && error.status === 401) throw error;
          }
        });
      });
      const start = button('Continue');
      start.type = 'submit';
      start.className = 'primary';
      form.append(start);
      body.append(form);
    } else if (phase === 'preview' && request) {
      const r = request;
      body.append(
        el('p', `Server: ${r.origin}`),
        el(
          'p',
          `${r.resolved_action === 'device' ? 'Connect a device using the working Claude account' : r.resolved_action === 'reconnect' ? 'Reconnect Claude account credentials' : 'Connect a new Claude account'} · ${r.account}${r.client_name ? ` · ${r.client_name}` : ' · account only'}`,
        ),
      );
      if (!linked) body.append(el('p', 'Connection code: ', el('code', r.code)));
      if (r.source_account)
        body.append(el('p', `Using existing Claude account ${r.source_account}.`));
      if (r.resolved_action === 'device')
        body.append(
          el(
            'p',
            'Claude is already connected. Pairing this device does not ask for, replace or discard an upstream token.',
          ),
        );
      if (linked)
        body.append(
          el(
            'p',
            'Approve only a computer you are setting up; type the code from your own terminal, not a link/message.',
          ),
        );
      const form = el('form');
      let terminalCode: HTMLInputElement | undefined;
      if (linked) {
        terminalCode = input('');
        terminalCode.required = true;
        terminalCode.pattern = '[a-fA-F0-9]{8}';
        terminalCode.maxLength = 8;
        terminalCode.autocomplete = 'off';
        terminalCode.spellcheck = false;
        form.append(field('Code from your terminal', terminalCode));
      }
      let token: HTMLInputElement | undefined;
      if (!linked && r.resolved_action !== 'device') {
        token = input('', 'password');
        token.required = true;
        token.autocomplete = 'off';
        token.maxLength = 16384;
        form.append(
          el(
            'p',
            'Run claude setup-token on a trusted computer. Paste its token (not an API key). Expiry is unknown unless supplied by the issuer.',
          ),
          field('Private Claude subscription token', token),
        );
      }
      let reconnectConfirmed: HTMLInputElement | undefined;
      if (r.resolved_action === 'reconnect') {
        form.append(
          el(
            'p',
            'The replacement token-only credential has no automatic refresh, even when replacing an existing rotating credential.',
          ),
        );
        const [label, confirm] = check(
          'Reconnect this account explicitly; existing devices keep their proxy credentials.',
          false,
        );
        confirm.required = true;
        reconnectConfirmed = confirm;
        form.append(label);
      }
      form.addEventListener('submit', (event) => {
        event.preventDefault();
        const secret = token?.value ?? '';
        if (token) token.value = '';
        if (reconnectConfirmed && !reconnectConfirmed.checked) {
          notice.textContent =
            'Confirm the explicit account reconnect before continuing. Your pasted token was cleared and not sent.';
          return;
        }
        const typedCode = terminalCode?.value ?? '';
        if (linked && (!/^[a-fA-F0-9]{8}$/.test(typedCode) || typedCode !== r.code)) {
          notice.textContent =
            'Enter the eight-character code from your own terminal to approve this computer.';
          return;
        }
        void run(() => authorizeAndCommit(secret, typedCode));
      });
      const approve = button(
        linked
          ? `Approve ${r.resolved_action}`
          : r.resolved_action === 'device'
            ? 'Approve and connect this device'
            : 'Approve and connect Claude',
      );
      approve.type = 'submit';
      approve.className = 'primary';
      form.append(approve);
      body.append(form);
    } else if (phase === 'result' && result) {
      if (result.token) {
        const proxy = input(result.token);
        proxy.readOnly = true;
        proxy.autocomplete = 'off';
        const gateway = input(result.gateway_url);
        gateway.readOnly = true;
        body.append(
          field('Scoped proxy token — shown once', proxy),
          field('Claude gateway URL', gateway),
          el(
            'p',
            'Save these only for this Claude connector. Do not paste the subscription token into device settings or overwrite global term-llm settings.',
          ),
          button('Copy proxy token', () => {
            if (!navigator.clipboard) {
              notice.textContent = 'Select the scoped proxy token above to copy it.';
              return;
            }
            void navigator.clipboard.writeText(proxy.value).then(
              () => {
                notice.textContent = 'Scoped proxy token copied.';
              },
              () => {
                notice.textContent = 'Copy failed. Select the scoped proxy token above.';
              },
            );
          }),
        );
      } else
        body.append(
          el('p', 'Account credentials replaced only. No new device or proxy token was created.'),
          el('p', result.gateway_url),
        );
      body.append(
        button(result.token ? 'I saved it — finish' : 'Finish reconnect', () => {
          void run(async () => {
            if (!cap) return;
            try {
              await client.ack(cap);
            } catch {
              await recover();
              return;
            }
            if (alive()) {
              result = undefined;
              phase = 'done';
              notice.textContent = 'Saved. The retained device token has been cleared.';
              try {
                await context.refresh(undefined, true);
              } catch (error) {
                if (error instanceof ProxyAPIError && error.status === 401) throw error;
                notice.textContent =
                  'Saved. Refresh administration to update the connection details.';
              }
            }
          });
        }),
      );
    } else if (phase === 'uncertain') {
      body.append(
        el(
          'p',
          'The outcome is not confirmed. The original request is retained in memory; no new request or token will be minted automatically.',
        ),
        button(linked ? 'Check approval status' : 'Check saved result', () => {
          void run(
            linked ? () => preview(options.link!.id, ticket ?? { id: options.link!.id }) : recover,
          );
        }),
      );
      if (cap && ticket)
        body.append(
          button('Review this same connection', () => {
            void run(async () => {
              await preview(cap!.id, ticket!);
            });
          }),
        );
    } else
      body.append(
        el(
          'p',
          phase === 'approved'
            ? 'Return to your computer to finish the connection.'
            : phase === 'done'
              ? 'Connection finished.'
              : 'No automatic retry or replacement will be performed.',
        ),
      );
    if (cap && !['done', 'terminal', 'result'].includes(phase))
      body.append(
        button('Cancel this connection', () => {
          void run(async () => {
            try {
              await client.cancel(cap!);
              if (alive()) terminal('Connection canceled. No new request was created.');
            } catch {
              await recover();
            }
          });
        }),
      );
    body.append(
      disclosure(
        'Connect from your computer instead',
        el(
          'p',
          'Run this command on the Claude computer. It contains no credential and configures only the standalone Claude connector.',
        ),
        el('code', `term-llm proxy connect claude --url ${window.location.origin}`),
      ),
    );
  }
  render();
  if (options.link) {
    const link = options.link;
    queueMicrotask(() => {
      if (!alive()) return;
      if (!/^[a-f0-9]{32}$/.test(link.id)) {
        terminal(
          'This connection link is invalid. Check the exact request ID and code on the requesting computer.',
        );
        render();
        return;
      }
      void run(async () => {
        phase = 'uncertain';
        await preview(link.id, { id: link.id });
      });
    });
  }
  return {
    element,
    resume: guard,
    dispose() {
      disposed = true;
      cap = undefined;
      ticket = undefined;
      request = undefined;
      result = undefined;
      for (const control of element.querySelectorAll<HTMLInputElement>('input')) control.value = '';
      context.protectToken(false);
    },
  };
}

import type {
  ProxyClient,
  ProxyPreset,
  ProxyProviderCreate,
  ProxyState,
} from '../src/api/proxy-client';
import { accountDialog, accountStateLabel } from './accounts';
import {
  actions,
  button,
  clearPassword,
  check,
  el,
  field,
  input,
  select,
  submit,
  type UIContext,
} from './dom';
import { requireProviderName, slugPattern } from './provider-name';
import { presetUpstreamHint } from './route-presets';

export function providerForm(
  preset: ProxyPreset,
  state: ProxyState,
  api: ProxyClient,
  context: UIContext,
  close: () => void,
): HTMLElement {
  const name = input(preset.id);
  name.required = true;
  name.pattern = slugPattern;
  name.maxLength = 64;
  name.readOnly = preset.family !== 'inference';
  const label = input(preset.label);
  const form = el(
    'form',
    field(
      'Provider name',
      name,
      name.readOnly
        ? 'Clients use this fixed provider name.'
        : 'Clients use this name in proxy.providers. It cannot be renamed.',
    ),
    field('Label', label),
  );
  const view = el('div', form);
  view.className = 'provider-onboarding';
  const upstream = input(preset.upstream);
  const [allowLabel, allowHTTP] = check('Allow insecure HTTP upstream', preset.billing === 'local');
  if (!preset.rules?.length) {
    upstream.required = true;
    form.append(field('Upstream base URL', upstream, presetUpstreamHint(preset)));
    form.append(allowLabel);
  } else {
    form.append(
      el('ul', ...preset.rules.map((rule) => el('li', `${rule.path_prefix} → ${rule.upstream}`))),
    );
  }
  const kind = preset.account_kind;
  const account = select([
    ['', 'Choose account'],
    ...(state.accounts ?? [])
      .filter((candidate) => candidate.kind === kind)
      .map((candidate): [string, string] => [
        candidate.name,
        `${candidate.name} · ${accountStateLabel(candidate)}`,
      ]),
  ]);
  const optionalKey = ['vllm', 'openai-compatible'].includes(preset.id);
  const usesKey = !kind && (!!preset.auth.header || !!preset.rules?.length);
  const value = input('', 'password');
  const source = select(
    [
      ...(optionalKey ? [['', 'No upstream key'] as [string, string]] : []),
      ['new', 'Enter a new API key'],
      ['existing', 'Use an existing secret'],
    ],
    optionalKey ? '' : 'new',
  );
  // Option values are separate from the write mode and may be any stored slug.
  const secret = select(
    [['', 'Choose a secret'], ...state.secrets.map((s): [string, string] => [s.name, s.name])],
    state.secrets.some((s) => s.name === preset.id) ? preset.id : '',
  );
  if (kind) {
    account.required = true;
    form.append(
      field('Trusted account', account),
      button('Connect new account', () => {
        accountDialog({ state, api, context }, view, kind, (stored) => {
          const option = el('option', `${stored.name} · ${accountStateLabel(stored)}`);
          option.value = stored.name;
          account.append(option);
          account.value = stored.name;
        });
      }),
    );
  } else if (usesKey) {
    if (!optionalKey && state.secrets.some((s) => s.name === preset.id)) source.value = 'existing';
    const keyField = field('API key', value, 'Stored once under the provider name.');
    const secretField = field('Secret name', secret);
    const update = () => {
      value.required = source.value === 'new';
      keyField.hidden = !value.required;
      if (!value.required) clearPassword(value);
      secret.required = source.value === 'existing';
      secretField.hidden = !secret.required;
    };
    update();
    source.addEventListener('change', update);
    form.append(field('Credential source', source), keyField, secretField);
  }
  if (preset.companions?.length) {
    form.append(
      el(
        'p',
        `Related routes: ${preset.companions.join(', ')}. Routes in this provider share one credential.`,
      ),
    );
  }
  if (preset.note) form.append(el('p', preset.note));
  const save = submit(
    form,
    context,
    async () => {
      const id = name.readOnly ? preset.id : name.value.trim();
      requireProviderName(id, state);
      const request: ProxyProviderCreate = {
        name: id,
        label: label.value.trim(),
        preset: preset.id,
      };
      if (
        kind &&
        !state.accounts?.some(
          (candidate) => candidate.kind === kind && candidate.name === account.value,
        )
      )
        throw new Error(`Choose an existing ${kind} account, or connect a new one first.`);
      if (usesKey && source.value === 'new') {
        if (state.secrets.some((s) => s.name === id))
          throw new Error(
            `A secret named ${id} already exists. Choose an existing secret or a different provider name.`,
          );
        request.value = value.value;
        clearPassword(value);
      } else if (usesKey && source.value === 'existing') request.secret = secret.value;
      if (kind || upstream.value !== preset.upstream || allowHTTP.checked) {
        // The server picks companion routes in the common case. Explicit route
        // choices are required to attach an account or override an upstream.
        request.routes = [
          {
            name: id,
            profile: preset.id,
            ...(kind ? { account: account.value } : {}),
            ...(upstream.value ? { upstream: upstream.value.trim() } : {}),
            ...(allowHTTP.checked ? { allow_insecure_http: true } : {}),
          },
        ];
        for (const companion of preset.companions ?? []) {
          if (id !== preset.id && companion === 'grok-bin') continue; // A second inference route is canonical-only.
          request.routes.push({
            name: id === preset.id ? companion : `${id}-${companion.replace(`${preset.id}-`, '')}`,
            profile: companion,
            ...(kind ? { account: account.value } : {}),
          });
        }
      }
      await api.createProvider(state.revision, request);
      await context.refresh(`route:${id}`);
      context.announce(`Created provider ${id}. No upstream request was made.`);
    },
    'Create provider',
  );
  form.append(actions(save, button('Cancel', close)));
  return view;
}

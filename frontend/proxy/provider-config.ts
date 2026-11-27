import {
  ProxyAPIError,
  type ProxyClient,
  type ProxyProviderClientConfig,
  type ProxyState,
} from '../src/api/proxy-client';
import { actions, button, confirmAction, el, field, submit, type UIContext } from './dom';
import { providerName, type ProviderGroup } from './provider-groups';

export function providerConfigSummary(config?: ProxyProviderClientConfig): string {
  if (!config) return 'None configured.';
  const parts: string[] = [];
  if (config.type) parts.push(config.type);
  if (config.model) parts.push(config.model);
  if (config.models?.length) {
    parts.push(`${config.models.length} ${config.models.length === 1 ? 'model' : 'models'}`);
  }
  if (config.context_window) parts.push(`${config.context_window.toLocaleString()} ctx`);
  if (config.vision_via) parts.push(`vision via ${config.vision_via}`);
  return parts.length ? parts.join(' · ') : 'Configured.';
}

export function providerConfigSection(
  group: ProviderGroup,
  state: ProxyState,
  api: ProxyClient,
  context: UIContext,
  readonly: boolean,
): HTMLElement {
  const focusKey = `route:${group.routes[0].name}`;
  const providerID = group.id;
  const summary = el('p', providerConfigSummary(group.config));
  summary.className = 'provider-config-summary';
  const purpose = el('p', 'Model settings sent to approved term-llm clients through discovery.');
  purpose.className = 'provider-note';
  const section = el('div');
  section.className = 'provider-config provider-section';

  if (readonly) {
    section.append(el('h3', 'Client config (term-llm)'), summary, purpose);
    if (group.config_yaml) {
      section.append(el('pre', el('code', group.config_yaml)));
    }
    return section;
  }

  let expanded = false;
  const panel = el('div');
  panel.className = 'provider-config-panel';
  panel.hidden = true;

  const setExpanded = (next: boolean) => {
    expanded = next;
    panel.hidden = !expanded;
    toggle.setAttribute('aria-expanded', expanded ? 'true' : 'false');
  };

  const toggle = button(group.config ? 'Edit client config' : 'Add client config', () => {
    setExpanded(!expanded);
    if (expanded) textarea.focus();
  });
  toggle.setAttribute('aria-expanded', 'false');
  toggle.setAttribute(
    'aria-label',
    `${group.config ? 'Edit' : 'Add'} client config for ${providerName(group)}`,
  );

  const header = el('div', el('div', el('h3', 'Client config (term-llm)'), summary), toggle);
  header.className = 'provider-section-header';
  section.append(header, purpose);

  const textarea = el('textarea');
  textarea.className = 'provider-config-yaml';
  textarea.rows = 10;
  textarea.spellcheck = false;
  textarea.value = group.config_yaml ?? '';
  textarea.placeholder = [
    'type: vllm',
    'model: deepseek-ai/DeepSeek-V4-Flash',
    'context_window: 200000',
    'max_output_tokens: 50000',
    'vllm_thinking_param: thinking',
  ].join('\n');

  const notice = el('p');
  notice.className = 'provider-config-notice';
  notice.hidden = true;

  const clean = button('Clean pasted YAML', () => {
    void context.run(async () => {
      if (!textarea.value.trim()) throw new Error('Paste a term-llm provider YAML block first.');
      const parsed = await api.parseProviderConfig(textarea.value);
      textarea.value = parsed.config_yaml;
      const stripped = parsed.stripped?.length
        ? ` Stripped local-only fields: ${parsed.stripped.join(', ')}.`
        : '';
      notice.textContent = `Validated YAML${parsed.name ? ` for ${parsed.name}` : ''}.${stripped} Review and save below.`;
      notice.hidden = false;
      context.announce(notice.textContent);
    }, clean);
  });

  const form = el(
    'form',
    field(
      'Provider YAML',
      textarea,
      'Served by GET /_proxy/client/v2/providers to approved clients. Paste a local term-llm block and use Clean pasted YAML to strip api_key, base_url and enabled.',
    ),
    notice,
  );
  const save = submit(
    form,
    context,
    async () => {
      try {
        await api.updateProviderConfig(state.revision, providerID, textarea.value);
      } catch (error) {
        if (error instanceof ProxyAPIError && error.status === 409) {
          try {
            await context.refresh(focusKey);
          } catch {
            // Preserve the conflict message.
          }
        }
        throw error;
      }
      await context.refresh(focusKey);
      context.announce(
        textarea.value.trim()
          ? `Saved client config for ${providerName(group)}. Existing client approvals are unchanged.`
          : `Cleared client config for ${providerName(group)}. Existing client approvals are unchanged.`,
      );
    },
    'Save client config',
  );
  const cancel = button('Cancel', () => {
    setExpanded(false);
    toggle.focus();
  });
  const buttons = actions(save, clean, cancel);
  if (group.config) {
    buttons.append(
      confirmAction(
        'Clear',
        async () => {
          await api.updateProviderConfig(state.revision, providerID, '');
          await context.refresh(focusKey);
          context.announce(
            `Cleared client config for ${providerName(group)}. Existing client approvals are unchanged.`,
          );
        },
        context,
        `Clear term-llm client config for ${providerName(group)}? Routes and client approvals stay unchanged.`,
      ),
    );
  }
  form.append(buttons);
  panel.append(form);
  section.append(panel);
  return section;
}

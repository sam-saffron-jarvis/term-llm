import type {
  ProxyAdminRoute,
  ProxyCaller,
  ProxyProvider,
  ProxyProviderFamily,
  ProxyState,
} from '../src/api/proxy-client';

export const providerFamilies: [ProxyProviderFamily, string][] = [
  ['inference', 'Inference'],
  ['classification', 'Classification'],
  ['search', 'Search'],
  ['media', 'Media'],
  ['embeddings', 'Embeddings'],
];

export interface ProviderGroup extends Omit<ProxyProvider, 'routes'> {
  routes: ProxyAdminRoute[];
}

/**
 * A provider's visible name is its ID: it is unique, and it is what appears
 * in URLs and client configs. The label only describes it, because several
 * providers can share a preset's default label (two "Anthropic" accounts).
 */
export function providerName(provider: Pick<ProxyProvider, 'id'>): string {
  return provider.id;
}

/** ID plus a differing label, for tooltips and accessible names. */
export function providerTitle(provider: Pick<ProxyProvider, 'id' | 'label'>): string {
  const label = provider.label?.trim();
  return label && label !== provider.id ? `${provider.id} (${label})` : provider.id;
}

export function providerGroups(state: ProxyState): ProviderGroup[] {
  const routes = new Map(state.routes.map((route) => [route.name, route]));
  return state.providers.map((provider) => ({
    ...provider,
    routes: provider.routes.flatMap((name) => {
      const route = routes.get(name);
      return route ? [route] : [];
    }),
  }));
}

export function providerApproval(
  group: ProviderGroup,
  caller: ProxyCaller,
): 'none' | 'partial' | 'approved' {
  if (caller.revoked) return 'none';
  const count = group.routes.filter((route) => caller.grants.includes(route.name)).length;
  return !count ? 'none' : count === group.routes.length ? 'approved' : 'partial';
}

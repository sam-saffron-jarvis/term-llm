import { fireEvent, render, screen } from '@testing-library/preact';
import { expect, it, vi } from 'vitest';
import { MCP } from './MCPModal';
import { AppStore } from '../../stores/app-store';
import { StoreContext } from '../../app/context';
import { normalizeMCPState } from '../../stores/store-utils';

it.each([false, true])('offers only granted linked MCP actions (manager %s)', async (manager) => {
  const store = new AppStore({
    prefix: '',
    version: 'test',
    sidebarCategories: [],
    agentName: '',
    agentNames: [],
    title: '',
    locationSharing: false,
    worktrees: false,
    hub: null,
    vapidKey: '',
  });
  store.linkedDevicesMode.value = true;
  store.linkedMCPManage.value = manager;
  store.mcp.value = {
    ...store.mcp.value,
    ...normalizeMCPState({
      servers: [{ name: 'shared', can_sign_in: true, can_remove: true }],
      enabled: [],
    }),
    oauth: {
      shared: {
        flowId: 'flow',
        authorizationURL: 'https://login.example/auth',
        state: 'pending',
        error: '',
        popupBlocked: false,
      },
    },
  };
  store.toggleMCP = vi.fn(async () => {});
  render(
    <StoreContext.Provider value={store}>
      <MCP />
    </StoreContext.Provider>,
  );
  fireEvent.click(screen.getByRole('checkbox', { name: 'Enable shared' }));
  expect(store.toggleMCP).toHaveBeenCalledWith('shared');
  const link = screen.queryByRole('link', { name: 'Open sign-in link' });
  if (!manager) {
    expect(link).toBeNull();
    expect(screen.queryByRole('button', { name: 'Add' })).toBeNull();
    return;
  }
  expect(link).toHaveAttribute('href', 'https://login.example/auth');
  expect(link).toHaveAttribute('target', '_blank');
  expect(link).toHaveAttribute('rel', expect.stringContaining('noopener'));
  fireEvent.click(screen.getByRole('button', { name: 'Add' }));
  const remote = await screen.findByRole('tab', { name: 'Remote URL' });
  expect(screen.queryByRole('tab', { name: 'Local command' })).toBeNull();
  fireEvent.click(remote);
  expect(screen.getByLabelText('URL')).toBeVisible();
  expect(screen.queryByLabelText('Headers')).toBeNull();
});

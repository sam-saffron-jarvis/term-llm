import { act, fireEvent, render, screen } from '@testing-library/preact';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { StoreContext } from '../app/context';
import { AppStore } from '../stores/app-store';
import { testConfig } from '../stores/store-test-fixtures';
import { Sidebar } from './Sidebar';

let store: AppStore;
beforeEach(() => {
  vi.useFakeTimers();
  store = new AppStore(testConfig);
});
afterEach(() => {
  store.dispose();
  vi.useRealTimers();
});

function showSearch() {
  const get = vi.spyOn(store.api, 'get').mockResolvedValue({ sessions: [] });
  render(
    <StoreContext.Provider value={store}>
      <Sidebar />
    </StoreContext.Provider>,
  );
  const input = screen.getByRole('searchbox', { name: 'Search chats' });
  return { get, type: (value: string) => fireEvent.input(input, { target: { value } }) };
}

async function advance(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

describe('sidebar search field', () => {
  it('sends only the final query without snippets, 200 ms after the last input', async () => {
    const { get, type } = showSearch();
    type('a'); // 0 ms
    await advance(50);
    type('ab'); // 50 ms
    await advance(190);
    expect(get).not.toHaveBeenCalled();
    type('abc'); // 240 ms
    await advance(199);
    expect(get).not.toHaveBeenCalled(); // 439 ms
    await advance(1); // 440 ms
    expect(get).toHaveBeenCalledTimes(1);
    const url = new URL(get.mock.calls[0][0], 'http://localhost');
    expect(url.pathname).toBe('/v1/sessions/search');
    expect(url.searchParams.get('q')).toBe('abc');
    expect(url.searchParams.get('prefix')).toBe('1');
    expect(url.searchParams.get('include_snippet')).toBe('0');
    await advance(200);
    expect(get).toHaveBeenCalledTimes(1);
  });

  it('cancels a pending request when the field is cleared', async () => {
    const { get, type } = showSearch();
    type('abc');
    await advance(100);
    type('');
    expect(store.searchResults.value).toBeNull();
    expect(store.searchLoading.value).toBe(false);
    await advance(200);
    expect(get).not.toHaveBeenCalled();
  });
});

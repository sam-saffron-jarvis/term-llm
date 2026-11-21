import '../../styles/features/mcp.css';
import { useEffect, useRef, useState } from 'preact/hooks';
import { useStore } from '../../app/context';
import type { MCPAddResult } from '../../domain/types';
import { Icon } from '../Icon';
import { Overlay } from '../Overlay';
import { lazyComponent } from '../lazyComponent';
import { MCPServerList } from './MCPServerList';

const MCPAddServer = lazyComponent(
  () =>
    import('./MCPAddServer')
      .then(({ MCPAddServer }) => MCPAddServer)
      .catch(() => () => (
        <p role="alert">Could not load the add-server form. Reload the page to retry.</p>
      )),
  <p role="status">Loading add-server form…</p>,
);

/**
 * MCP servers dialog. One overlay hosts two views so switching between the
 * list and the pushed "Add server" sheet never re-opens the dialog.
 */
export function MCP() {
  const store = useStore();
  const linked = store.linkedDevicesMode.value;
  const canManage = !linked || store.linkedMCPManage.value;
  const [view, setView] = useState<'list' | 'add'>('list');
  const [notice, setNotice] = useState('');
  const addButton = useRef<HTMLButtonElement>(null);
  const returning = useRef(false);

  useEffect(() => () => store.dismissRemovedMCPServer(), [store]);
  useEffect(() => {
    if (view !== 'list' || !returning.current) return;
    returning.current = false;
    addButton.current?.focus();
  }, [view]);

  const openAdd = () => {
    setNotice('');
    setView('add');
  };
  const back = () => {
    returning.current = true;
    setView('list');
  };
  const added = (result: MCPAddResult) => {
    setNotice(
      result.needs_input
        ? `Added ${result.name}. Fill in its placeholder values in mcp.json, then turn it on.`
        : '',
    );
    back();
  };

  if (canManage && view === 'add')
    return (
      <Overlay
        title="Add server"
        className="mcp-modal"
        onEscape={back}
        titleLeading={
          <button
            class="icon-btn mcp-back"
            type="button"
            aria-label="Back to MCP servers"
            onClick={back}
          >
            <Icon name="arrow-left" />
          </button>
        }
      >
        <MCPAddServer onAdded={added} onCancel={back} />
      </Overlay>
    );

  return (
    <Overlay
      title="MCP servers"
      className="mcp-modal"
      titleActions={
        canManage && (
          <button ref={addButton} class="mcp-button" type="button" onClick={openAdd}>
            <Icon name="add" />
            Add
          </button>
        )
      }
    >
      {linked && (
        <p>
          {canManage
            ? 'Choose tools for this conversation. You can add public HTTPS servers and connect accounts for your own installations.'
            : 'Choose host-shared tools for this conversation. Installation and sign-in are managed on the host.'}
        </p>
      )}
      <MCPServerList notice={notice} onAdd={openAdd} />
    </Overlay>
  );
}

(() => {
'use strict';

const app = window.TermLLMApp;
const { elements } = app;
const input = elements.promptInput;
const menu = elements.slashCommandMenu;

const commands = [
  {
    name: '/compact',
    description: 'Compress the active conversation context',
  },
  {
    name: '/compress',
    description: 'Compress the active conversation context',
  },
  {
    name: '/goal',
    description: 'Set or manage the session goal',
  },
  {
    name: '/mcp',
    description: 'Manage MCP servers for this conversation',
  },
  {
    name: '/model',
    description: 'Choose the provider and model',
  },
  {
    name: '/new',
    description: 'Start a new conversation',
  },
  {
    name: '/side',
    description: 'Ask without interrupting the main response',
  },
].sort((a, b) => a.name.localeCompare(b.name));

let matches = [];
let selected = 0;

const hide = () => {
  matches = [];
  selected = 0;
  menu.hidden = true;
  menu.replaceChildren();
  input.setAttribute('aria-expanded', 'false');
  input.setAttribute('aria-activedescendant', '');
};

const accept = (command = matches[selected]) => {
  if (!command) return false;
  input.value = `${command.name} `;
  hide();
  app.autoGrowPrompt?.();
  input.focus();
  return true;
};

const render = () => {
  menu.replaceChildren();
  matches.forEach((command, index) => {
    const option = document.createElement('button');
    option.type = 'button';
    option.id = `slash-command-${index}`;
    option.className = 'slash-command-option';
    option.setAttribute('role', 'option');
    option.setAttribute('aria-selected', String(index === selected));
    option.classList.toggle('selected', index === selected);

    const name = document.createElement('span');
    name.className = 'slash-command-name';
    name.textContent = command.name;
    const description = document.createElement('span');
    description.className = 'slash-command-description';
    description.textContent = command.description;
    option.append(name, description);

    option.addEventListener('mousedown', (event) => event.preventDefault());
    option.addEventListener('click', () => accept(command));
    option.addEventListener('mousemove', () => {
      if (selected === index) return;
      selected = index;
      render();
    });
    menu.append(option);
  });
  menu.hidden = matches.length === 0;
  input.setAttribute('aria-expanded', String(matches.length > 0));
  input.setAttribute('aria-activedescendant', matches.length > 0 ? `slash-command-${selected}` : '');
};

const update = () => {
  const value = String(input.value || '');
  if (!/^\/[^\s]*$/.test(value)) {
    hide();
    return;
  }
  const query = value.toLowerCase();
  matches = commands.filter((command) => command.name.startsWith(query));
  selected = 0;
  render();
};

const consume = (event) => {
  event.preventDefault();
  event.stopImmediatePropagation();
};

input.addEventListener('input', update);
input.addEventListener('blur', hide);
input.addEventListener('keydown', (event) => {
  if (menu.hidden || matches.length === 0 || event.isComposing) return;
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    consume(event);
    const offset = event.key === 'ArrowDown' ? 1 : -1;
    selected = (selected + offset + matches.length) % matches.length;
    render();
    return;
  }
  if (event.key === 'Tab' || (event.key === 'Enter' && !event.shiftKey)) {
    consume(event);
    accept();
    return;
  }
  if (event.key === 'Escape') {
    consume(event);
    hide();
  }
});

Object.assign(app, { hideSlashCommands: hide, updateSlashCommands: update });
})();

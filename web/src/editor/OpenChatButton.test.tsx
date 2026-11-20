import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { WfNode } from '../api/types';
import { chatPath, chatWidgetUrl, OpenChatButton, OpenChatView } from './OpenChatButton';

const fetchWorkflow = vi.fn();
vi.mock('../api/hooks', () => ({ fetchWorkflow: (...a: unknown[]) => fetchWorkflow(...a) }));
const settings = vi.hoisted(() => ({ chatBasePath: '/chat', maxNoteTextBytes: 16384 }));
vi.mock('../auth/AuthContext', () => ({ useEditorSettings: () => settings }));

const chat = (parameters: Record<string, unknown> = {}): WfNode => ({
  name: 'Support Chat', type: 'engi.trigger.chat', position: [0, 0], parameters,
});

function renderButton(node: WfNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/workflows/wf_1']}>
        <Routes><Route path="/workflows/:id" element={<OpenChatButton node={node} />} /></Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('chat URL', () => {
  it('uses the configured path, trimmed', () => {
    expect(chatPath(chat({ path: '/support/' }), 'wf_1')).toBe('support');
    expect(chatWidgetUrl(chat({ path: 'a/b' }), 'wf_1', 'http://x', '/chat')).toBe('http://x/chat/a/b/widget');
    expect(chatWidgetUrl(chat({ path: 'a/b' }), 'wf_1', 'http://x', '/talk')).toBe('http://x/talk/a/b/widget');
  });
  it('defaults to <workflow id>/<node name slug> like the server', () => {
    expect(chatPath(chat(), 'wf_1')).toBe('wf_1/support-chat');
    expect(chatPath({ ...chat(), name: '!!!' }, 'wf_1')).toBe('wf_1/webhook');
  });
});

describe('OpenChatView', () => {
  it('links to the widget in a new tab', () => {
    render(<OpenChatView url="http://x/chat/s/widget" />);
    const link = screen.getByRole('link', { name: 'Open chat' });
    expect(link).toHaveAttribute('href', 'http://x/chat/s/widget');
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', expect.stringContaining('noopener'));
  });
  it('is disabled with the reason when blocked', () => {
    render(<OpenChatView url="u" blocked="Because." />);
    expect(screen.getByRole('button', { name: 'Open chat' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Open chat' })).toHaveAccessibleDescription('Because.');
  });
});

describe('OpenChatButton', () => {
  beforeEach(() => {
    fetchWorkflow.mockReset();
    settings.chatBasePath = '/chat';
  });

  it('opens the chat under the server chat.basePath', async () => {
    settings.chatBasePath = '/talk';
    fetchWorkflow.mockResolvedValue({ version: 1, doc: { name: 'w', active: true, nodes: [chat({ path: 'support' })], connections: [] } });
    renderButton(chat({ path: 'support' }));
    const link = await screen.findByRole('link', { name: 'Open chat' });
    expect(link).toHaveAttribute('href', `${window.location.origin}/talk/support/widget`);
  });

  it('opens the chat of an active workflow', async () => {
    fetchWorkflow.mockResolvedValue({ version: 1, doc: { name: 'w', active: true, nodes: [chat({ path: 'support' })], connections: [] } });
    renderButton(chat({ path: 'support' }));
    const link = await screen.findByRole('link', { name: 'Open chat' });
    expect(link).toHaveAttribute('href', `${window.location.origin}/chat/support/widget`);
  });
  it('explains why an inactive workflow has no chat', async () => {
    fetchWorkflow.mockResolvedValue({ version: 1, doc: { name: 'w', active: false, nodes: [chat()], connections: [] } });
    renderButton(chat());
    expect(await screen.findByText(/Activate the workflow/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Open chat' })).toBeDisabled();
  });
  it('is unavailable for authenticated chats', async () => {
    const node = chat({ authentication: 'basic' });
    fetchWorkflow.mockResolvedValue({ version: 1, doc: { name: 'w', active: true, nodes: [node], connections: [] } });
    renderButton(node);
    expect(await screen.findByText(/only available when authentication is set to None/)).toBeInTheDocument();
  });
  it('asks to save a node that is not saved yet', async () => {
    fetchWorkflow.mockResolvedValue({ version: 1, doc: { name: 'w', active: true, nodes: [], connections: [] } });
    renderButton(chat());
    expect(await screen.findByText(/Save the workflow first/)).toBeInTheDocument();
  });
});

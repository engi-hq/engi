import { useQuery } from '@tanstack/react-query';
import { useParams } from 'react-router-dom';
import { fetchWorkflow } from '../api/hooks';
import type { WfNode } from '../api/types';
import { useEditorSettings } from '../auth/AuthContext';
import { t } from '../i18n/t';

/** The route the host serves a chat trigger under: its path, or <workflow id>/<node name as a slug> when empty. */
export function chatPath(node: WfNode, workflowId: string): string {
  const params = (node.parameters ?? {}) as Record<string, unknown>;
  const set = typeof params.path === 'string' ? params.path.replace(/^\/+|\/+$/g, '') : '';
  if (set) return set;
  const slug = node.name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '') || 'webhook';
  return `${workflowId}/${slug}`;
}

/** URL of the hosted chat page of a chat trigger; basePath is the server's chat.basePath. */
export function chatWidgetUrl(node: WfNode, workflowId: string, origin: string, basePath: string): string {
  return `${origin}${basePath}/${chatPath(node, workflowId)}/widget`;
}

interface ViewProps {
  url: string;
  /** Why the chat cannot be opened; undefined when it can. */
  blocked?: string;
}

/** Presentational part: a link when the chat can be opened, a disabled button and the reason otherwise. */
export function OpenChatView({ url, blocked }: ViewProps) {
  if (blocked) {
    return (
      <div className="pfield">
        <button className="btn sm" disabled aria-describedby="open-chat-why">{t('chat.open')}</button>
        <div className="field-hint" id="open-chat-why">{blocked}</div>
      </div>
    );
  }
  return (
    <div className="pfield">
      <a className="btn sm" href={url} target="_blank" rel="noopener noreferrer">{t('chat.open')}</a>
    </div>
  );
}

/** "Open chat" for a Chat Trigger node: opens its hosted widget once the saved workflow is active. */
export function OpenChatButton({ node }: { node: WfNode }) {
  const { id } = useParams();
  const { chatBasePath } = useEditorSettings();
  const wf = useQuery({
    queryKey: ['chat-trigger-active', id],
    enabled: !!id,
    queryFn: () => fetchWorkflow(id ?? ''),
    refetchOnMount: 'always',
    staleTime: 0,
    gcTime: 0,
  });
  if (!id) return null;
  const saved = wf.data?.doc.nodes.find((n) => n.name === node.name);
  const auth = (node.parameters as Record<string, unknown> | undefined)?.authentication;
  let blocked: string | undefined;
  if (!wf.data?.doc.active) blocked = t('chat.inactive');
  else if (!saved) blocked = t('chat.unsaved');
  else if (auth !== undefined && auth !== 'none') blocked = t('chat.authenticated');
  return <OpenChatView url={chatWidgetUrl(saved ?? node, id, window.location.origin, chatBasePath)} blocked={blocked} />;
}

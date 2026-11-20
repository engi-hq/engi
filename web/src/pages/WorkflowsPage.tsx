import { useRef, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { useTags } from '../api/accountHooks';
import { exportWorkflow, useWorkflowMutations, useWorkflows } from '../api/hooks';
import { normalizeDoc } from '../api/normalize';
import type { WorkflowDoc, WorkflowSummary } from '../api/types';
import { t } from '../i18n/t';
import { ConfirmDialog, Dialog } from '../ui/Dialog';
import { Icon } from '../ui/Icon';
import { Switch } from '../ui/Switch';
import { describeError, useToast } from '../ui/Toast';
import { downloadText, safeFilename } from '../util/download';
import { useDebounced } from '../util/useDebounced';

/** A new workflow starts with a manual trigger so it can run at once. */
export function blankWorkflow(name: string): WorkflowDoc {
  return {
    name,
    nodes: [{ name: 'Start', type: 'engi.trigger.manual', typeVersion: 1, position: [0, 0], parameters: {} }],
    connections: [],
  };
}

/** Validates pasted or uploaded workflow JSON; throws a readable message. */
export function parseWorkflowJson(text: string): WorkflowDoc {
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch {
    throw new Error(t('workflows.invalidJson'));
  }
  const doc = raw as Partial<WorkflowDoc> | null;
  if (!doc || typeof doc !== 'object' || !Array.isArray(doc.nodes) || typeof doc.name !== 'string') {
    throw new Error(t('workflows.notWorkflow'));
  }
  return normalizeDoc(doc as WorkflowDoc);
}

export function WorkflowsPage() {
  const [search, setSearch] = useState('');
  const q = useDebounced(search);
  const [tag, setTag] = useState('');
  const tags = useTags();
  const list = useWorkflows({ q, tag: tag || undefined });
  const m = useWorkflowMutations();
  const toast = useToast();
  const nav = useNavigate();
  const [importing, setImporting] = useState(false);
  const [deleting, setDeleting] = useState<WorkflowSummary | null>(null);
  const items = list.data?.pages.flatMap((p) => p.data) ?? [];

  const create = () =>
    m.create.mutate(blankWorkflow(t('workflows.untitled')), {
      onSuccess: (wf) => nav(`/workflows/${wf.id}`),
      onError: toast.error,
    });

  const onExport = async (wf: WorkflowSummary) => {
    try {
      const doc = await exportWorkflow(wf.id);
      downloadText(`${safeFilename(wf.name)}.json`, JSON.stringify(doc, null, 2));
    } catch (e) {
      toast.error(e);
    }
  };

  return (
    <main className="page">
      <div className="page-inner">
        <div className="page-head">
          <h1>{t('nav.workflows')}</h1>
          <span className="spacer" />
          <button className="btn" onClick={() => setImporting(true)}><Icon name="upload" /> {t('workflows.import')}</button>
          <button className="btn primary" onClick={create} disabled={m.create.isPending}>
            <Icon name="plus" /> {t('workflows.create')}
          </button>
        </div>
        <input
          type="search"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder={t('workflows.search')}
          aria-label={t('workflows.search')}
        />
        {(tags.data?.length ?? 0) > 0 && (
          <div role="group" aria-label={t('tags.filter')} style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
            <button className="btn sm" aria-pressed={tag === ''} onClick={() => setTag('')}>{t('tags.all')}</button>
            {tags.data?.map((x) => (
              <button key={x.id} className="btn sm" aria-pressed={tag === x.name} onClick={() => setTag(tag === x.name ? '' : x.name)}>{x.name}</button>
            ))}
          </div>
        )}
        <div className="card">
          {list.isPending ? (
            <div className="empty"><span className="spinner" /></div>
          ) : list.isError ? (
            <div className="empty" role="alert">{describeError(list.error)}</div>
          ) : items.length === 0 ? (
            <div className="empty">{q || tag ? t('workflows.noMatch') : t('workflows.empty')}</div>
          ) : (
            <table className="list">
              <thead>
                <tr>
                  <th>{t('common.name')}</th>
                  <th>{t('workflows.nodes')}</th>
                  <th>{t('workflows.active')}</th>
                  <th><span className="sr-only">{t('common.actions')}</span></th>
                </tr>
              </thead>
              <tbody>
                {items.map((wf) => (
                  <tr key={wf.id}>
                    <td>
                      <Link to={`/workflows/${wf.id}`}><strong>{wf.name}</strong></Link>
                      {wf.tags?.map((x) => <span key={x} className="badge" style={{ marginLeft: 6 }}>{x}</span>)}
                    </td>
                    <td>{wf.nodeCount}</td>
                    <td>
                      <Switch
                        checked={wf.active}
                        label={t('workflows.toggleActive', { name: wf.name })}
                        hideLabel
                        onChange={(active) =>
                          m.setActive.mutate({ id: wf.id, active }, {
                            onError: toast.error,
                            onSuccess: () => toast.info(t(active ? 'workflows.activated' : 'workflows.deactivated', { name: wf.name })),
                          })
                        }
                      />
                    </td>
                    <td>
                      <div className="row-actions">
                        <button className="btn ghost icon sm" onClick={() => void onExport(wf)} aria-label={t('workflows.exportOne', { name: wf.name })} title={t('workflows.export')}>
                          <Icon name="download" />
                        </button>
                        <button className="btn ghost icon sm" onClick={() => setDeleting(wf)} aria-label={t('workflows.deleteOne', { name: wf.name })} title={t('common.delete')}>
                          <Icon name="trash" />
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
        {list.hasNextPage && (
          <button className="btn" onClick={() => void list.fetchNextPage()} disabled={list.isFetchingNextPage}>
            {t('common.loadMore')}
          </button>
        )}
      </div>
      {importing && <ImportDialog onClose={() => setImporting(false)} />}
      {deleting && (
        <ConfirmDialog
          title={t('workflows.deleteTitle')}
          message={t('workflows.deleteMessage', { name: deleting.name })}
          confirmLabel={t('common.delete')}
          danger
          onClose={() => setDeleting(null)}
          onConfirm={() => m.remove.mutate(deleting.id, { onError: toast.error })}
        />
      )}
    </main>
  );
}

function ImportDialog({ onClose }: { onClose: () => void }) {
  const m = useWorkflowMutations();
  const nav = useNavigate();
  const [text, setText] = useState('');
  const [error, setError] = useState('');
  const fileRef = useRef<HTMLInputElement>(null);

  const submit = () => {
    try {
      const doc = parseWorkflowJson(text);
      m.import.mutate(doc, {
        onSuccess: (wf) => { onClose(); nav(`/workflows/${wf.id}`); },
        onError: (e) => setError(describeError(e)),
      });
    } catch (e) {
      setError(describeError(e));
    }
  };

  return (
    <Dialog title={t('workflows.import')} onClose={onClose} wide>
      <p className="field-hint" style={{ margin: 0 }}>{t('workflows.importHint')}</p>
      <input
        ref={fileRef}
        type="file"
        accept="application/json,.json"
        aria-label={t('workflows.chooseFile')}
        onChange={(e) => void e.target.files?.[0]?.text().then(setText)}
      />
      <textarea
        rows={10}
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder={t('workflows.pasteJson')}
        aria-label={t('workflows.pasteJson')}
        data-autofocus
      />
      {error && <div className="callout error" role="alert">{error}</div>}
      <div className="dialog-actions">
        <button className="btn" onClick={onClose}>{t('common.cancel')}</button>
        <button className="btn primary" onClick={submit} disabled={!text.trim() || m.import.isPending}>{t('workflows.import')}</button>
      </div>
    </Dialog>
  );
}

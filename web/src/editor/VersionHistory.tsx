import { useState } from 'react';
import { ApiError } from '../api/client';
import { restoreWorkflowVersion, useWorkflowVersions, type LoadedWorkflow } from '../api/hooks';
import { t } from '../i18n/t';
import { ConfirmDialog } from '../ui/Dialog';
import { Icon } from '../ui/Icon';
import { describeError } from '../ui/Toast';

const formatDate = (iso: string) => new Date(iso).toLocaleString();

interface PanelProps {
  workflowId: string;
  /** Version currently shown on the canvas, if any. */
  selected: number | null;
  /** Version number of the saved current document. */
  current: number;
  onSelect: (version: number) => void;
  onClose: () => void;
}

/** Side panel listing saved versions, newest first, paged by the API cursor. */
export function HistoryPanel({ workflowId, selected, current, onSelect, onClose }: PanelProps) {
  const q = useWorkflowVersions(workflowId);
  const rows = q.data?.pages.flatMap((p) => p.data) ?? [];
  return (
    <aside className="panel" aria-label={t('history.title')}>
      <div className="panel-head">
        <h2 style={{ margin: 0, fontSize: 15 }}>{t('history.title')}</h2>
        <span className="spacer" />
        <button className="btn icon ghost sm" onClick={onClose} aria-label={t('common.close')}><Icon name="close" /></button>
      </div>
      <div className="panel-body">
        {q.isPending && <span className="spinner" aria-label={t('common.loading')} />}
        {q.isError && <div className="callout error" role="alert">{describeError(q.error)}</div>}
        {q.isSuccess && rows.length === 0 && <div className="empty">{t('history.empty')}</div>}
        {rows.length > 0 && (
          <ul className="history-list" aria-label={t('history.title')}>
            {rows.map((v) => (
              <li key={v.version}>
                <button className="history-item" aria-current={selected === v.version} onClick={() => onSelect(v.version)}>
                  <strong>
                    {t('history.version', { v: v.version })}
                    {v.version === current && <> <span className="badge">{t('history.current')}</span></>}
                  </strong>
                  <time dateTime={v.createdAt} className="field-hint">{formatDate(v.createdAt)}</time>
                </button>
              </li>
            ))}
          </ul>
        )}
        {q.hasNextPage && (
          <button className="btn sm" style={{ marginTop: 8 }} disabled={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>
            {t('history.more')}
          </button>
        )}
      </div>
    </aside>
  );
}

interface BannerProps {
  workflowId: string;
  version: number;
  /** True when the editor holds unsaved changes (restore then asks first). */
  dirty: boolean;
  onBack: () => void;
  onRestored: (loaded: LoadedWorkflow) => void;
}

/** Read-only banner shown while a historical version is on the canvas. */
export function VersionBanner({ workflowId, version, dirty, onBack, onRestored }: BannerProps) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [confirm, setConfirm] = useState(false);

  const restore = async () => {
    setBusy(true);
    setError('');
    try {
      onRestored(await restoreWorkflowVersion(workflowId, version));
    } catch (e) {
      setError(e instanceof ApiError && e.status === 409 ? t('history.conflict') : describeError(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <div className="banner" role="status">
        <Icon name="clock" />
        <span>{t('history.viewing', { v: version })}</span>
        {error && <span className="runbar-error" role="alert">{error}</span>}
        <span className="spacer" />
        <button className="btn sm" onClick={onBack}>{t('history.back')}</button>
        <button className="btn sm primary" disabled={busy} onClick={() => (dirty ? setConfirm(true) : void restore())}>
          {busy ? t('history.restoring') : t('history.restore')}
        </button>
      </div>
      {confirm && (
        <ConfirmDialog
          title={t('history.restoreTitle', { v: version })}
          message={t('history.restoreMessage', { v: version })}
          confirmLabel={t('history.restore')}
          danger
          onClose={() => setConfirm(false)}
          onConfirm={() => void restore()}
        />
      )}
    </>
  );
}

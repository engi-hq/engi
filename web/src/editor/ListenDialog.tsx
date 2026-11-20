import { useEffect, useRef, useState } from 'react';
import { api } from '../api/client';
import { armTestWebhook } from '../api/hooks';
import type { ArmedWebhook, Execution, Page } from '../api/types';
import { t } from '../i18n/t';
import { Dialog } from '../ui/Dialog';
import { describeError } from '../ui/Toast';

interface Props {
  workflowId: string;
  node?: string;
  onExecution: (executionId: string) => void;
  onClose: () => void;
}

const POLL_MS = 1500;

export function webhookUrl(urlPath: string): string {
  return `${window.location.origin}${urlPath}`;
}

/** Arms the test webhook, shows its URL and waits for the call that starts a manual execution. */
export function ListenDialog({ workflowId, node, onExecution, onClose }: Props) {
  const [armed, setArmed] = useState<ArmedWebhook | null>(null);
  const [error, setError] = useState('');
  const [now, setNow] = useState(Date.now());
  const since = useRef(Date.now());

  useEffect(() => {
    let cancelled = false;
    since.current = Date.now() - 1000;
    armTestWebhook(workflowId, node).then((a) => { if (!cancelled) setArmed(a); }).catch((e) => { if (!cancelled) setError(describeError(e)); });
    return () => { cancelled = true; };
  }, [workflowId, node]);

  useEffect(() => {
    if (!armed) return;
    const tick = setInterval(() => setNow(Date.now()), 1000);
    const poll = setInterval(() => {
      api.get<Page<Execution>>('/executions', { workflowId, limit: 5 }).then((page) => {
        const hit = page.data.find((e) => e.mode === 'manual' && Date.parse(e.startedAt) >= since.current);
        if (hit) onExecution(hit.id);
      }).catch(() => undefined);
    }, POLL_MS);
    return () => { clearInterval(tick); clearInterval(poll); };
  }, [armed, workflowId, onExecution]);

  const left = armed ? Math.max(0, Math.round((Date.parse(armed.expiresAt) - now) / 1000)) : 0;
  const url = armed ? webhookUrl(armed.urlPath) : '';

  return (
    <Dialog title={t('listen.title')} onClose={onClose}>
      {error ? <div className="callout error" role="alert">{error}</div> : !armed ? <span className="spinner" /> : (
        <>
          <p style={{ margin: 0 }}>{t('listen.hint')}</p>
          <div className="inline">
            <span className="badge">{armed.method}</span>
            <input type="text" readOnly value={url} aria-label={t('listen.url')} onFocus={(e) => e.currentTarget.select()} />
            <button className="btn" onClick={() => void navigator.clipboard?.writeText(url)}>{t('common.copy')}</button>
          </div>
          <div role="status" aria-live="polite" className="field-hint">
            {left > 0 ? t('listen.waiting', { s: left }) : t('listen.expired')}
          </div>
        </>
      )}
      <div className="dialog-actions">
        {armed && left === 0 && <button className="btn" onClick={() => setArmed(null)}>{t('listen.rearm')}</button>}
        <button className="btn" onClick={onClose}>{t('common.close')}</button>
      </div>
    </Dialog>
  );
}

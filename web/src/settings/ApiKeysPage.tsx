import { useState, type FormEvent } from 'react';
import { useApiKeyMutations, useApiKeys, type ApiKey, type CreatedApiKey } from '../api/accountHooks';
import { t } from '../i18n/t';
import { ConfirmDialog, Dialog } from '../ui/Dialog';
import { Icon } from '../ui/Icon';
import { describeError, useToast } from '../ui/Toast';
import { fieldErrors } from './fieldErrors';

const fmt = (iso?: string) => (iso ? new Date(iso).toLocaleString() : '');

export function ApiKeysPage() {
  const list = useApiKeys();
  const m = useApiKeyMutations();
  const toast = useToast();
  const [creating, setCreating] = useState(false);
  const [secret, setSecret] = useState<CreatedApiKey | null>(null);
  const [revoking, setRevoking] = useState<ApiKey | null>(null);

  return (
    <>
      <div className="page-head">
        <h2>{t('settings.apiKeys')}</h2>
        <span className="spacer" />
        <button className="btn primary" onClick={() => setCreating(true)}><Icon name="plus" /> {t('keys.create')}</button>
      </div>
      <div className="card">
        {list.isPending ? <div className="empty"><span className="spinner" /></div>
          : list.isError ? <div className="empty" role="alert">{describeError(list.error)}</div>
          : list.data.length === 0 ? <div className="empty">{t('keys.empty')}</div> : (
            <table className="list">
              <thead><tr>
                <th>{t('common.name')}</th><th>{t('keys.prefix')}</th><th>{t('keys.scopes')}</th>
                <th>{t('keys.created')}</th><th>{t('keys.lastUsed')}</th><th>{t('keys.expires')}</th>
                <th><span className="sr-only">{t('common.actions')}</span></th>
              </tr></thead>
              <tbody>
                {list.data.map((k) => (
                  <tr key={k.id}>
                    <td><strong>{k.name}</strong></td>
                    <td><code>{k.prefix}</code></td>
                    <td>{k.scopes.join(', ')}</td>
                    <td>{fmt(k.createdAt)}</td>
                    <td>{fmt(k.lastUsedAt) || t('keys.never')}</td>
                    <td>{fmt(k.expiresAt) || t('keys.never')}</td>
                    <td>
                      <div className="row-actions">
                        {k.revokedAt ? <span className="badge canceled">{t('keys.revoked')}</span> : (
                          <button className="btn sm" aria-label={t('keys.revokeOne', { name: k.name })} onClick={() => setRevoking(k)}>{t('keys.revoke')}</button>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
      </div>
      {creating && <CreateKeyDialog onClose={() => setCreating(false)} onCreated={(k) => { setCreating(false); setSecret(k); }} />}
      {secret && <SecretDialog created={secret} onClose={() => setSecret(null)} />}
      {revoking && (
        <ConfirmDialog
          title={t('keys.revokeTitle')}
          message={t('keys.revokeMessage', { name: revoking.name })}
          confirmLabel={t('keys.revoke')}
          danger
          onClose={() => setRevoking(null)}
          onConfirm={() => m.revoke.mutate(revoking.id, { onError: toast.error })}
        />
      )}
    </>
  );
}

function CreateKeyDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (k: CreatedApiKey) => void }) {
  const m = useApiKeyMutations();
  const [error, setError] = useState<unknown>(null);
  const fe = fieldErrors(error);
  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const f = new FormData(e.currentTarget);
    const exp = String(f.get('expiresAt') ?? '');
    const scope = String(f.get('scope')) as 'read' | 'write';
    setError(null);
    m.create.mutate(
      { name: String(f.get('name')).trim(), scopes: [scope], ...(exp ? { expiresAt: new Date(exp).toISOString() } : {}) },
      { onSuccess: onCreated, onError: setError },
    );
  };
  return (
    <Dialog title={t('keys.createTitle')} onClose={onClose}>
      <form onSubmit={submit} style={{ display: 'grid', gap: 12 }}>
        <label className="field">
          <span className="field-label">{t('common.name')}</span>
          <input name="name" required data-autofocus aria-invalid={!!fe.name} />
          {fe.name && <span className="field-hint" role="alert">{fe.name}</span>}
        </label>
        <label className="field">
          <span className="field-label">{t('keys.scope')}</span>
          <select name="scope" defaultValue="read">
            <option value="read">{t('keys.scopeRead')}</option>
            <option value="write">{t('keys.scopeWrite')}</option>
          </select>
        </label>
        <label className="field">
          <span className="field-label">{t('keys.expiry')}</span>
          <input name="expiresAt" type="datetime-local" aria-invalid={!!fe.expiresAt} />
          {fe.expiresAt && <span className="field-hint" role="alert">{fe.expiresAt}</span>}
        </label>
        {!!error && Object.keys(fe).length === 0 && <div className="callout error" role="alert">{describeError(error)}</div>}
        <div className="dialog-actions">
          <button type="button" className="btn" onClick={onClose}>{t('common.cancel')}</button>
          <button type="submit" className="btn primary" disabled={m.create.isPending}>{t('keys.create')}</button>
        </div>
      </form>
    </Dialog>
  );
}

/** Shows the one-time token. Only the explicit button closes it (no overlay click is relied on). */
function SecretDialog({ created, onClose }: { created: CreatedApiKey; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    void navigator.clipboard?.writeText(created.token).then(() => setCopied(true), () => undefined);
  };
  return (
    <Dialog title={t('keys.secretTitle')} onClose={onClose}>
      <div className="callout error" role="alert">{t('keys.secretWarning')}</div>
      <label className="field">
        <span className="field-label">{t('keys.secretLabel')}</span>
        <input readOnly value={created.token} onFocus={(e) => e.currentTarget.select()} style={{ fontFamily: 'var(--font-mono, monospace)' }} />
      </label>
      <div className="dialog-actions">
        <button className="btn" onClick={copy}><Icon name="copy" /> {copied ? t('keys.copied') : t('common.copy')}</button>
        <button className="btn primary" data-autofocus onClick={onClose}>{t('keys.done')}</button>
      </div>
      <span className="sr-only" aria-live="polite">{copied ? t('keys.copied') : ''}</span>
    </Dialog>
  );
}

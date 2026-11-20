import { useEffect, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useCredentialMutations, useCredentials, useCredentialTypes } from '../api/hooks';
import type { CredentialSummary } from '../api/types';
import { CredentialDialog } from '../credentials/CredentialDialog';
import { t } from '../i18n/t';
import { ConfirmDialog } from '../ui/Dialog';
import { Icon } from '../ui/Icon';
import { describeError, useToast } from '../ui/Toast';

export function CredentialsPage() {
  const list = useCredentials();
  const types = useCredentialTypes();
  const m = useCredentialMutations();
  const toast = useToast();
  const [params, setParams] = useSearchParams();
  const [dialog, setDialog] = useState<{ id?: string } | null>(null);
  const [deleting, setDeleting] = useState<CredentialSummary | null>(null);
  const [testing, setTesting] = useState<Record<string, string>>({});

  // The OAuth2 callback sends the browser back here with ?credentialId=.
  useEffect(() => {
    if (params.get('credentialId')) {
      toast.info(t('cred.authorized'));
      setParams({}, { replace: true });
    }
  }, [params, setParams, toast]);

  const typeName = (n: string) => types.data?.find((x) => x.name === n)?.displayName ?? n;
  const test = (c: CredentialSummary) =>
    m.test.mutate(c.id, {
      onSuccess: (r) => setTesting((s) => ({ ...s, [c.id]: r.success ? t('cred.testOk') : (r.message ?? t('cred.testFailed')) })),
      onError: (e) => setTesting((s) => ({ ...s, [c.id]: describeError(e) })),
    });

  return (
    <main className="page">
      <div className="page-inner">
        <div className="page-head">
          <h1>{t('nav.credentials')}</h1>
          <span className="spacer" />
          <button className="btn primary" onClick={() => setDialog({})}><Icon name="plus" /> {t('cred.create')}</button>
        </div>
        <div className="card">
          {list.isPending ? <div className="empty"><span className="spinner" /></div>
            : list.isError ? <div className="empty" role="alert">{describeError(list.error)}</div>
            : list.data.length === 0 ? <div className="empty">{t('cred.empty')}</div> : (
              <table className="list">
                <thead><tr><th>{t('common.name')}</th><th>{t('cred.type')}</th><th>{t('cred.status')}</th><th><span className="sr-only">{t('common.actions')}</span></th></tr></thead>
                <tbody>
                  {list.data.map((c) => (
                    <tr key={c.id}>
                      <td><button className="btn ghost" onClick={() => setDialog({ id: c.id })}><strong>{c.name}</strong></button></td>
                      <td>{typeName(c.type)}</td>
                      <td aria-live="polite">{testing[c.id] ?? ''}</td>
                      <td>
                        <div className="row-actions">
                          {types.data?.find((x) => x.name === c.type)?.testable && (
                            <button className="btn sm" onClick={() => test(c)}>{t('cred.test')}</button>
                          )}
                          <button className="btn ghost icon sm" onClick={() => setDeleting(c)} aria-label={t('cred.deleteOne', { name: c.name })}><Icon name="trash" /></button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
        </div>
      </div>
      {dialog && <CredentialDialog id={dialog.id} onClose={() => setDialog(null)} />}
      {deleting && (
        <ConfirmDialog
          title={t('cred.deleteTitle')}
          message={t('cred.deleteMessage', { name: deleting.name })}
          confirmLabel={t('common.delete')}
          danger
          onClose={() => setDeleting(null)}
          onConfirm={() => m.remove.mutate(deleting.id, { onError: toast.error })}
        />
      )}
    </main>
  );
}

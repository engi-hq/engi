import { useState, type FormEvent } from 'react';
import {
  SECRET_SENTINEL, VARIABLE_KEY_RE, useVariableMutations, useVariables, type Variable,
} from '../api/accountHooks';
import { ApiError } from '../api/client';
import { t } from '../i18n/t';
import { ConfirmDialog, Dialog } from '../ui/Dialog';
import { Icon } from '../ui/Icon';
import { Switch } from '../ui/Switch';
import { describeError, useToast } from '../ui/Toast';
import { fieldErrors } from './fieldErrors';

export function VariablesPage() {
  const list = useVariables();
  const m = useVariableMutations();
  const toast = useToast();
  const [editing, setEditing] = useState<Variable | 'new' | null>(null);
  const [deleting, setDeleting] = useState<Variable | null>(null);

  return (
    <>
      <div className="page-head">
        <h2>{t('settings.variables')}</h2>
        <span className="spacer" />
        <button className="btn primary" onClick={() => setEditing('new')}><Icon name="plus" /> {t('vars.create')}</button>
      </div>
      <div className="card">
        {list.isPending ? <div className="empty"><span className="spinner" /></div>
          : list.isError ? <div className="empty" role="alert">{describeError(list.error)}</div>
          : list.data.length === 0 ? <div className="empty">{t('vars.empty')}</div> : (
            <table className="list">
              <thead><tr>
                <th>{t('vars.key')}</th><th>{t('vars.value')}</th>
                <th><span className="sr-only">{t('common.actions')}</span></th>
              </tr></thead>
              <tbody>
                {list.data.map((v) => (
                  <tr key={v.id}>
                    <td><code>{v.key}</code></td>
                    <td>{v.secret ? <em>{t('vars.hidden')}</em> : <code>{v.value}</code>}</td>
                    <td>
                      <div className="row-actions">
                        <button className="btn sm" aria-label={t('vars.editOne', { key: v.key })} onClick={() => setEditing(v)}>{t('vars.edit')}</button>
                        <button className="btn ghost icon sm" aria-label={t('vars.deleteOne', { key: v.key })} onClick={() => setDeleting(v)}><Icon name="trash" /></button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
      </div>
      {editing && <VariableDialog variable={editing === 'new' ? null : editing} onClose={() => setEditing(null)} />}
      {deleting && (
        <ConfirmDialog
          title={t('vars.deleteTitle')}
          message={t('vars.deleteMessage', { key: deleting.key })}
          confirmLabel={t('common.delete')}
          danger
          onClose={() => setDeleting(null)}
          onConfirm={() => m.remove.mutate(deleting.id, { onError: toast.error })}
        />
      )}
    </>
  );
}

function VariableDialog({ variable, onClose }: { variable: Variable | null; onClose: () => void }) {
  const m = useVariableMutations();
  const [key, setKey] = useState(variable?.key ?? '');
  const [value, setValue] = useState(variable?.value ?? '');
  const [secret, setSecret] = useState(variable?.secret ?? false);
  const [error, setError] = useState<unknown>(null);
  const [local, setLocal] = useState('');
  const fe = fieldErrors(error);
  const keyBad = key !== '' && !VARIABLE_KEY_RE.test(key);
  const dup = error instanceof ApiError && error.status === 409;
  const keyError = fe.key ?? (dup ? t('vars.duplicate') : '');

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    setLocal('');
    // Turning secret off while the value is still the placeholder would store the placeholder.
    if (variable?.secret && !secret && value === SECRET_SENTINEL) {
      setLocal(t('vars.newValueRequired'));
      return;
    }
    const done = { onSuccess: onClose, onError: setError };
    if (variable) m.update.mutate({ id: variable.id, patch: { key, value, secret } }, done);
    else m.create.mutate({ key, value, secret }, done);
  };
  const pending = m.create.isPending || m.update.isPending;

  return (
    <Dialog title={variable ? t('vars.edit') : t('vars.create')} onClose={onClose}>
      <form onSubmit={submit} style={{ display: 'grid', gap: 12 }}>
        <label className="field">
          <span className="field-label">{t('vars.key')}</span>
          <input value={key} onChange={(e) => setKey(e.target.value)} required data-autofocus aria-invalid={keyBad || !!keyError} />
          <span className="field-hint" role={keyBad || keyError ? 'alert' : undefined}>{keyError || t('vars.keyHint')}</span>
        </label>
        <label className="field">
          <span className="field-label">{t('vars.value')}</span>
          <input value={value} onChange={(e) => setValue(e.target.value)} type={secret ? 'password' : 'text'} aria-invalid={!!fe.value || !!local} />
          {(fe.value || local) && <span className="field-hint" role="alert">{fe.value || local}</span>}
        </label>
        <Switch checked={secret} onChange={setSecret} label={t('vars.secret')} />
        <span className="field-hint">{t('vars.secretHint')}</span>
        {!!error && !keyError && !fe.value && <div className="callout error" role="alert">{describeError(error)}</div>}
        <div className="dialog-actions">
          <button type="button" className="btn" onClick={onClose}>{t('common.cancel')}</button>
          <button type="submit" className="btn primary" disabled={pending || keyBad}>{t('common.save')}</button>
        </div>
      </form>
    </Dialog>
  );
}

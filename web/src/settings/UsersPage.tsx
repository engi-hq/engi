import { useState, type FormEvent } from 'react';
import { useUserMutations, useUsers } from '../api/accountHooks';
import type { User } from '../api/types';
import { useAuth } from '../auth/AuthContext';
import { t } from '../i18n/t';
import { Dialog } from '../ui/Dialog';
import { Icon } from '../ui/Icon';
import { describeError, useToast } from '../ui/Toast';
import { fieldErrors } from './fieldErrors';

export function UsersPage() {
  const { user: me } = useAuth();
  const isOwner = me?.role === 'owner';
  const list = useUsers(isOwner);
  const m = useUserMutations();
  const toast = useToast();
  const [creating, setCreating] = useState(false);
  if (!isOwner) return <div className="callout" role="alert">{t('users.ownerOnly')}</div>;

  return (
    <>
      <div className="page-head">
        <h2>{t('settings.users')}</h2>
        <span className="spacer" />
        <button className="btn primary" onClick={() => setCreating(true)}><Icon name="plus" /> {t('users.invite')}</button>
      </div>
      <div className="card">
        {list.isPending ? <div className="empty"><span className="spinner" /></div>
          : list.isError ? <div className="empty" role="alert">{describeError(list.error)}</div>
          : list.data.length === 0 ? <div className="empty">{t('users.empty')}</div> : (
            <table className="list">
              <thead><tr><th>{t('settings.name')}</th><th>{t('settings.email')}</th><th>{t('users.role')}</th><th>{t('users.status')}</th><th><span className="sr-only">{t('common.actions')}</span></th></tr></thead>
              <tbody>
                {list.data.map((u: User) => (
                  <tr key={u.id}>
                    <td><strong>{u.name}</strong></td>
                    <td>{u.email}</td>
                    <td>{u.role}</td>
                    <td>{u.disabled ? t('users.disabled') : t('users.active')}</td>
                    <td>
                      <div className="row-actions">
                        {u.id !== me?.id && (
                          <button
                            className="btn sm"
                            aria-label={t(u.disabled ? 'users.enableOne' : 'users.disableOne', { name: u.name })}
                            onClick={() => m.update.mutate({ id: u.id, patch: { disabled: !u.disabled } }, { onError: toast.error })}
                          >
                            {u.disabled ? t('users.enable') : t('users.disable')}
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
      </div>
      {creating && <CreateUserDialog onClose={() => setCreating(false)} />}
    </>
  );
}

function CreateUserDialog({ onClose }: { onClose: () => void }) {
  const m = useUserMutations();
  const [error, setError] = useState<unknown>(null);
  const fe = fieldErrors(error);
  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const f = new FormData(e.currentTarget);
    setError(null);
    m.create.mutate(
      { name: String(f.get('name')).trim(), email: String(f.get('email')).trim(), password: String(f.get('password')) },
      { onSuccess: onClose, onError: setError },
    );
  };
  return (
    <Dialog title={t('users.createTitle')} onClose={onClose}>
      <form onSubmit={submit} style={{ display: 'grid', gap: 12 }}>
        <label className="field">
          <span className="field-label">{t('settings.name')}</span>
          <input name="name" required data-autofocus aria-invalid={!!fe.name} />
          {fe.name && <span className="field-hint" role="alert">{fe.name}</span>}
        </label>
        <label className="field">
          <span className="field-label">{t('settings.email')}</span>
          <input name="email" type="email" required aria-invalid={!!fe.email} />
          {fe.email && <span className="field-hint" role="alert">{fe.email}</span>}
        </label>
        <label className="field">
          <span className="field-label">{t('auth.password')}</span>
          <input name="password" type="password" required autoComplete="new-password" aria-invalid={!!fe.password} />
          <span className="field-hint" role={fe.password ? 'alert' : undefined}>{fe.password ?? t('auth.passwordHint')}</span>
        </label>
        {!!error && Object.keys(fe).length === 0 && <div className="callout error" role="alert">{describeError(error)}</div>}
        <div className="dialog-actions">
          <button type="button" className="btn" onClick={onClose}>{t('common.cancel')}</button>
          <button type="submit" className="btn primary" disabled={m.create.isPending}>{t('users.create')}</button>
        </div>
      </form>
    </Dialog>
  );
}

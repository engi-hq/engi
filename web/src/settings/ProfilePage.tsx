import { useState, type FormEvent } from 'react';
import { useUserMutations } from '../api/accountHooks';
import type { UserPatch } from '../api/accountHooks';
import { useAuth } from '../auth/AuthContext';
import { t } from '../i18n/t';
import { describeError, useToast } from '../ui/Toast';
import { fieldErrors } from './fieldErrors';

export function ProfilePage() {
  const { user } = useAuth();
  const m = useUserMutations();
  const toast = useToast();
  const [error, setError] = useState<unknown>(null);
  if (!user) return null;
  const fe = fieldErrors(error);

  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const f = new FormData(e.currentTarget);
    const patch: UserPatch = {};
    const name = String(f.get('name')).trim();
    const email = String(f.get('email')).trim();
    const password = String(f.get('password'));
    if (name !== user.name) patch.name = name;
    if (email !== user.email) patch.email = email;
    if (password) patch.password = password;
    setError(null);
    m.update.mutate({ id: user.id, patch }, {
      onSuccess: () => toast.info(t('settings.saved')),
      onError: setError,
    });
  };

  return (
    <form className="card" style={{ padding: 16, display: 'grid', gap: 12, maxWidth: 480 }} onSubmit={submit}>
      <label className="field">
        <span className="field-label">{t('settings.name')}</span>
        <input name="name" defaultValue={user.name} required autoComplete="name" aria-invalid={!!fe.name} />
        {fe.name && <span className="field-hint" role="alert">{fe.name}</span>}
      </label>
      <label className="field">
        <span className="field-label">{t('settings.email')}</span>
        <input name="email" type="email" defaultValue={user.email} required autoComplete="email" aria-invalid={!!fe.email} />
        {fe.email && <span className="field-hint" role="alert">{fe.email}</span>}
      </label>
      <label className="field">
        <span className="field-label">{t('settings.newPassword')}</span>
        <input name="password" type="password" autoComplete="new-password" aria-invalid={!!fe.password} />
        <span className="field-hint">{fe.password ?? t('settings.passwordHint')}</span>
      </label>
      {!!error && Object.keys(fe).length === 0 && <div className="callout error" role="alert">{describeError(error)}</div>}
      <div><button className="btn primary" type="submit" disabled={m.update.isPending}>{t('common.save')}</button></div>
    </form>
  );
}

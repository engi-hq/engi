import { useQuery } from '@tanstack/react-query';
import { useState, type FormEvent } from 'react';
import { Navigate, useLocation, useNavigate } from 'react-router-dom';
import { api } from '../api/client';
import { t } from '../i18n/t';
import { Logo } from '../ui/Logo';
import { describeError } from '../ui/Toast';
import { useAuth } from './AuthContext';

export function useSetupRequired() {
  return useQuery({
    queryKey: ['setup'],
    queryFn: () => api.get<{ required: boolean }>('/auth/setup'),
    staleTime: 0,
  });
}

function useAfterLogin(): string {
  const loc = useLocation();
  const from = (loc.state as { from?: string } | null)?.from;
  return from && from.startsWith('/') ? from : '/workflows';
}

export function LoginPage() {
  const { user, login, loading } = useAuth();
  const setup = useSetupRequired();
  const nav = useNavigate();
  const dest = useAfterLogin();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  if (loading || setup.isPending) return <div className="auth-page"><span className="spinner" /></div>;
  if (user) return <Navigate to={dest} replace />;
  if (setup.data?.required) return <Navigate to="/setup" replace />;

  const submit = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const f = new FormData(e.currentTarget);
    setBusy(true);
    setError('');
    try {
      await login(String(f.get('email')), String(f.get('password')));
      nav(dest, { replace: true });
    } catch (err) {
      setError(describeError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="auth-page">
      <form className="card auth-card" onSubmit={submit}>
        <Logo />
        <h1>{t('auth.loginTitle')}</h1>
        <label className="field">
          <span className="field-label">{t('auth.email')}</span>
          <input name="email" type="email" autoComplete="username" required data-autofocus autoFocus />
        </label>
        <label className="field">
          <span className="field-label">{t('auth.password')}</span>
          <input name="password" type="password" autoComplete="current-password" required />
        </label>
        {error && <div className="callout error" role="alert">{error}</div>}
        <button className="btn primary" type="submit" disabled={busy}>{t('auth.login')}</button>
      </form>
    </main>
  );
}

export function SetupPage() {
  const { user, setup: doSetup } = useAuth();
  const status = useSetupRequired();
  const nav = useNavigate();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  if (status.isPending) return <div className="auth-page"><span className="spinner" /></div>;
  if (user) return <Navigate to="/workflows" replace />;
  if (!status.data?.required) return <Navigate to="/login" replace />;

  const submit = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const f = new FormData(e.currentTarget);
    if (f.get('password') !== f.get('confirm')) {
      setError(t('auth.passwordMismatch'));
      return;
    }
    setBusy(true);
    setError('');
    try {
      await doSetup(String(f.get('email')), String(f.get('name')), String(f.get('password')));
      nav('/workflows', { replace: true });
    } catch (err) {
      setError(describeError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="auth-page">
      <form className="card auth-card" onSubmit={submit}>
        <Logo />
        <h1>{t('auth.setupTitle')}</h1>
        <p className="field-hint" style={{ margin: 0 }}>{t('auth.setupHint')}</p>
        <label className="field">
          <span className="field-label">{t('auth.name')}</span>
          <input name="name" type="text" autoComplete="name" required autoFocus />
        </label>
        <label className="field">
          <span className="field-label">{t('auth.email')}</span>
          <input name="email" type="email" autoComplete="username" required />
        </label>
        <label className="field">
          <span className="field-label">{t('auth.password')}</span>
          <input name="password" type="password" autoComplete="new-password" required minLength={10} />
          <span className="field-hint">{t('auth.passwordHint')}</span>
        </label>
        <label className="field">
          <span className="field-label">{t('auth.confirm')}</span>
          <input name="confirm" type="password" autoComplete="new-password" required />
        </label>
        {error && <div className="callout error" role="alert">{error}</div>}
        <button className="btn primary" type="submit" disabled={busy}>{t('auth.createOwner')}</button>
      </form>
    </main>
  );
}

/** Wraps routes that need a session. */
export function RequireAuth({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth();
  const loc = useLocation();
  if (loading) return <div className="auth-page"><span className="spinner" aria-label={t('common.loading')} /></div>;
  if (!user) return <Navigate to="/login" replace state={{ from: loc.pathname + loc.search }} />;
  return <>{children}</>;
}

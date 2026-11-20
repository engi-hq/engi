import { NavLink, Outlet, useNavigate } from 'react-router-dom';
import { useAuth } from '../auth/AuthContext';
import { t } from '../i18n/t';
import { Icon } from './Icon';
import { Logo } from './Logo';
import { useTheme } from './theme';

export function Shell() {
  const { user, logout } = useAuth();
  const nav = useNavigate();
  const { theme, toggle } = useTheme();
  return (
    <div className="shell">
      <header className="topbar">
        <NavLink to="/workflows" className="brand"><Logo /> Engi</NavLink>
        <nav aria-label={t('nav.main')}>
          <NavLink to="/workflows">{t('nav.workflows')}</NavLink>
          <NavLink to="/executions">{t('nav.executions')}</NavLink>
          <NavLink to="/credentials">{t('nav.credentials')}</NavLink>
          <NavLink to="/settings">{t('nav.settings')}</NavLink>
        </nav>
        <span className="spacer" />
        <button className="btn ghost icon" onClick={toggle} aria-label={t('nav.theme', { theme })} title={t('nav.theme', { theme })}>
          <Icon name="sun" />
        </button>
        <span className="field-hint">{user?.email}</span>
        <button
          className="btn ghost"
          onClick={() => void logout().then(() => nav('/login'))}
        >
          <Icon name="logout" /> {t('auth.logout')}
        </button>
      </header>
      <Outlet />
    </div>
  );
}

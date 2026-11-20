import { NavLink, Outlet } from 'react-router-dom';
import { useAuth } from '../auth/AuthContext';
import { t } from '../i18n/t';

export function SettingsLayout() {
  const { user } = useAuth();
  return (
    <main className="page">
      <div className="page-inner">
        <div className="page-head"><h1>{t('nav.settings')}</h1></div>
        <nav aria-label={t('settings.nav')} style={{ display: 'flex', gap: 16, marginBottom: 16 }}>
          <NavLink to="/settings/profile">{t('settings.profile')}</NavLink>
          {user?.role === 'owner' && <NavLink to="/settings/users">{t('settings.users')}</NavLink>}
          <NavLink to="/settings/api-keys">{t('settings.apiKeys')}</NavLink>
          <NavLink to="/settings/tags">{t('settings.tags')}</NavLink>
          <NavLink to="/settings/variables">{t('settings.variables')}</NavLink>
        </nav>
        <Outlet />
      </div>
    </main>
  );
}

import { lazy, Suspense } from 'react';
import { createBrowserRouter, Navigate, RouterProvider, type RouteObject } from 'react-router-dom';
import { AuthProvider } from './auth/AuthContext';
import { LoginPage, RequireAuth, SetupPage } from './auth/AuthPages';
import { CredentialsPage } from './pages/CredentialsPage';
import { ExecutionsPage } from './pages/ExecutionsPage';
import { WorkflowsPage } from './pages/WorkflowsPage';
import { SettingsLayout } from './settings/SettingsLayout';
import { ApiKeysPage } from './settings/ApiKeysPage';
import { ProfilePage } from './settings/ProfilePage';
import { TagsPage } from './settings/TagsPage';
import { VariablesPage } from './settings/VariablesPage';
import { UsersPage } from './settings/UsersPage';
import { Shell } from './ui/Shell';

// The editor pulls in the canvas library; load it only on its route.
const EditorPage = lazy(() => import('./pages/EditorPage').then((m) => ({ default: m.EditorPage })));

export const routes: RouteObject[] = [
  { path: '/login', element: <LoginPage /> },
  { path: '/setup', element: <SetupPage /> },
  {
    element: <RequireAuth><Shell /></RequireAuth>,
    children: [
      { path: '/', element: <Navigate to="/workflows" replace /> },
      { path: '/workflows', element: <WorkflowsPage /> },
      { path: '/workflows/:id', element: <Suspense fallback={<main className="page"><span className="spinner" /></main>}><EditorPage /></Suspense> },
      { path: '/executions', element: <ExecutionsPage /> },
      { path: '/credentials', element: <CredentialsPage /> },
      {
        path: '/settings',
        element: <SettingsLayout />,
        children: [
          { index: true, element: <Navigate to="profile" replace /> },
          { path: 'profile', element: <ProfilePage /> },
          { path: 'users', element: <UsersPage /> },
          { path: 'api-keys', element: <ApiKeysPage /> },
          { path: 'tags', element: <TagsPage /> },
          { path: 'variables', element: <VariablesPage /> },
        ],
      },
      { path: '*', element: <Navigate to="/workflows" replace /> },
    ],
  },
];

const router = createBrowserRouter(routes);

export function App() {
  return (
    <AuthProvider>
      <RouterProvider router={router} />
    </AuthProvider>
  );
}

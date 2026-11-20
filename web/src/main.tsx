import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import { ApiError } from './api/client';
import { ToastProvider } from './ui/Toast';
import './styles/tokens.css';
import './styles/base.css';
import { applyTheme } from './ui/theme';

try {
  const saved = localStorage.getItem('engi.theme');
  if (saved === 'light' || saved === 'dark') applyTheme(saved);
} catch {
  /* storage may be blocked */
}

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      retry: (count, error) => !(error instanceof ApiError && error.status >= 400 && error.status < 500) && count < 2,
    },
  },
});

const root = document.getElementById('root');
if (!root) throw new Error('missing #root');
createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <App />
      </ToastProvider>
    </QueryClientProvider>
  </StrictMode>,
);

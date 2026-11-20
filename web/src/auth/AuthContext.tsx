import { useQuery, useQueryClient } from '@tanstack/react-query';
import { createContext, useCallback, useContext, useEffect, useMemo, type ReactNode } from 'react';
import { api, ApiError, setCsrfToken, setUnauthorizedHandler } from '../api/client';
import type { EditorSettings, Me, Session, User } from '../api/types';

interface AuthState {
  user: User | null;
  /** Server settings for the editor, from GET /me; undefined while signed out. */
  editor: EditorSettings | undefined;
  loading: boolean;
  login: (email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  setup: (email: string, name: string, password: string) => Promise<void>;
}

const Ctx = createContext<AuthState | null>(null);

async function fetchMe(): Promise<Me | null> {
  try {
    const me = await api.get<Me>('/me');
    setCsrfToken(me.csrfToken);
    return me;
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) {
      setCsrfToken(undefined);
      return null;
    }
    throw e;
  }
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient();
  const me = useQuery({ queryKey: ['me'], queryFn: fetchMe, staleTime: Infinity, retry: false });

  useEffect(() => {
    setUnauthorizedHandler(() => {
      setCsrfToken(undefined);
      qc.setQueryData(['me'], null);
    });
    return () => setUnauthorizedHandler(undefined);
  }, [qc]);

  const login = useCallback(
    async (email: string, password: string) => {
      await api.post<Session>('/auth/login', { email, password });
      // The login answer has no editor settings, so read GET /me, which has both the caller and them.
      qc.setQueryData<Me | null>(['me'], await fetchMe());
    },
    [qc],
  );
  const logout = useCallback(async () => {
    await api.post('/auth/logout');
    setCsrfToken(undefined);
    qc.clear();
    qc.setQueryData(['me'], null);
  }, [qc]);
  const setup = useCallback(
    async (email: string, name: string, password: string) => {
      await api.post('/auth/setup', { email, name, password });
      await login(email, password);
    },
    [login],
  );

  const value = useMemo<AuthState>(
    () => ({ user: me.data?.user ?? null, editor: me.data?.editor, loading: me.isPending, login, logout, setup }),
    [me.data, me.isPending, login, logout, setup],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useAuth(): AuthState {
  const v = useContext(Ctx);
  if (!v) throw new Error('useAuth outside AuthProvider');
  return v;
}

/** The server settings the editor needs. Only for screens behind RequireAuth, where they are always loaded. */
export function useEditorSettings(): EditorSettings {
  const { editor } = useAuth();
  if (!editor) throw new Error('editor settings are not loaded; render the editor behind RequireAuth');
  return editor;
}

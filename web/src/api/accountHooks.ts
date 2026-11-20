import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, listAll, request } from './client';
import type { components } from './schema';
import type { User } from './types';

export type ApiKey = components['schemas']['ApiKey'];
export type CreatedApiKey = components['schemas']['CreatedApiKey'];
export type NewApiKey = components['schemas']['NewApiKey'];
export type NewUser = components['schemas']['NewUser'];
export type UserPatch = components['schemas']['UserPatch'];

export function useUsers(enabled: boolean) {
  return useQuery({ queryKey: ['users'], enabled, queryFn: () => listAll<User>('/users') });
}

export function useUserMutations() {
  const qc = useQueryClient();
  const refresh = () => qc.invalidateQueries({ queryKey: ['users'] });
  return {
    create: useMutation({ mutationFn: (u: NewUser) => api.post<User>('/users', u), onSuccess: refresh }),
    update: useMutation({
      mutationFn: ({ id, patch }: { id: string; patch: UserPatch }) => request<User>('PATCH', `/users/${id}`, { body: patch }),
      onSuccess: (u, v) => {
        void refresh();
        // Keep the signed-in identity fresh when editing oneself.
        const me = qc.getQueryData<{ user: User }>(['me']);
        if (me && me.user.id === v.id) qc.setQueryData(['me'], { ...me, user: u });
      },
    }),
  };
}
export function useApiKeys() {
  return useQuery({ queryKey: ['api-keys'], queryFn: () => listAll<ApiKey>('/api-keys') });
}

export function useApiKeyMutations() {
  const qc = useQueryClient();
  const refresh = () => qc.invalidateQueries({ queryKey: ['api-keys'] });
  return {
    create: useMutation({ mutationFn: (k: NewApiKey) => api.post<CreatedApiKey>('/api-keys', k), onSuccess: refresh }),
    revoke: useMutation({ mutationFn: (id: string) => api.del(`/api-keys/${id}`), onSuccess: refresh }),
  };
}

export interface Tag { id: string; name: string }
export interface Variable { id: string; key: string; value: string; secret: boolean }
export interface VariableInput { key: string; value: string; secret?: boolean }

/** The server returns this in place of a secret variable's value; sending it back keeps the stored value. */
export const SECRET_SENTINEL = '__ENGI_SECRET__';
/** Mirrors the server's variable key rule (identifier-like, usable as `$vars.<key>`). */
export const VARIABLE_KEY_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;

export function useTags() {
  return useQuery({ queryKey: ['tags'], queryFn: () => listAll<Tag>('/tags') });
}

export function useTagMutations() {
  const qc = useQueryClient();
  // Rename/delete touch every workflow that carries the tag.
  const refresh = () => Promise.all([qc.invalidateQueries({ queryKey: ['tags'] }), qc.invalidateQueries({ queryKey: ['workflows'] })]);
  return {
    rename: useMutation({
      mutationFn: ({ id, name }: { id: string; name: string }) => request<Tag>('PATCH', `/tags/${id}`, { body: { name } }),
      onSuccess: refresh,
    }),
    remove: useMutation({ mutationFn: (id: string) => api.del(`/tags/${id}`), onSuccess: refresh }),
  };
}

export function useVariables() {
  return useQuery({ queryKey: ['variables'], queryFn: () => listAll<Variable>('/variables') });
}

export function useVariableMutations() {
  const qc = useQueryClient();
  const refresh = () => qc.invalidateQueries({ queryKey: ['variables'] });
  return {
    create: useMutation({ mutationFn: (v: VariableInput) => api.post<Variable>('/variables', v), onSuccess: refresh }),
    update: useMutation({
      mutationFn: ({ id, patch }: { id: string; patch: Partial<VariableInput> }) => request<Variable>('PATCH', `/variables/${id}`, { body: patch }),
      onSuccess: refresh,
    }),
    remove: useMutation({ mutationFn: (id: string) => api.del(`/variables/${id}`), onSuccess: refresh }),
  };
}

import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, rawRequest } from './client';
import { normalizeDoc } from './normalize';
import type {
  ArmedWebhook, Credential, CredentialSummary, CredentialType, Execution, ExecutionStatus,
  NodeType, Page, WorkflowDoc, WorkflowSummary,
} from './types';

export const keys = {
  workflows: (q: object) => ['workflows', q] as const,
  workflow: (id: string) => ['workflow', id] as const,
  nodeTypes: ['node-types'] as const,
  credTypes: ['credential-types'] as const,
  credentials: ['credentials'] as const,
  executions: (q: object) => ['executions', q] as const,
  execution: (id: string) => ['execution', id] as const,
};

// ---- Catalogs ----
export function useNodeTypes() {
  return useQuery({
    queryKey: keys.nodeTypes,
    staleTime: Infinity,
    queryFn: async () => (await api.get<{ data: NodeType[] }>('/node-types')).data,
  });
}
export function useCredentialTypes() {
  return useQuery({
    queryKey: keys.credTypes,
    staleTime: Infinity,
    queryFn: async () => (await api.get<{ data: CredentialType[] }>('/credential-types')).data,
  });
}

// ---- Workflows ----
export function useWorkflows(filter: { q: string; active?: boolean; tag?: string }) {
  return useInfiniteQuery({
    queryKey: keys.workflows(filter),
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) =>
      api.get<Page<WorkflowSummary>>('/workflows', { q: filter.q, active: filter.active, tag: filter.tag, cursor: pageParam }),
    getNextPageParam: (last) => (last.hasMore ? last.nextCursor : undefined),
  });
}

export interface LoadedWorkflow {
  doc: WorkflowDoc;
  version: number;
}

export async function fetchWorkflow(id: string): Promise<LoadedWorkflow> {
  const res = await rawRequest('GET', `/workflows/${id}`);
  const doc = normalizeDoc((await res.json()) as WorkflowDoc);
  return { doc, version: doc.version ?? Number(res.headers.get('ETag')?.replace(/"/g, '') ?? 1) };
}

export function useWorkflow(id: string | undefined) {
  return useQuery({
    queryKey: keys.workflow(id ?? ''),
    enabled: !!id,
    queryFn: () => fetchWorkflow(id ?? ''),
    staleTime: Infinity,
    gcTime: 0,
  });
}

export function useWorkflowMutations() {
  const qc = useQueryClient();
  const refresh = () => qc.invalidateQueries({ queryKey: ['workflows'] });
  return {
    create: useMutation({
      mutationFn: (doc: WorkflowDoc) => api.post<WorkflowDoc>('/workflows', doc),
      onSuccess: refresh,
    }),
    import: useMutation({
      mutationFn: (doc: WorkflowDoc) => api.post<WorkflowDoc>('/workflows/import', doc),
      onSuccess: refresh,
    }),
    remove: useMutation({ mutationFn: (id: string) => api.del(`/workflows/${id}`), onSuccess: refresh }),
    setActive: useMutation({
      mutationFn: ({ id, active }: { id: string; active: boolean }) =>
        api.post(`/workflows/${id}/${active ? 'activate' : 'deactivate'}`),
      onSuccess: refresh,
    }),
  };
}

/** Saves a workflow with optimistic concurrency. Returns the stored document. */
export async function saveWorkflow(doc: WorkflowDoc): Promise<LoadedWorkflow> {
  const res = await rawRequest('PUT', `/workflows/${doc.id}`, {
    body: doc,
    headers: doc.version ? { 'If-Match': `"${doc.version}"` } : undefined,
  });
  const stored = normalizeDoc((await res.json()) as WorkflowDoc);
  return { doc: stored, version: stored.version ?? doc.version ?? 1 };
}

export async function exportWorkflow(id: string): Promise<WorkflowDoc> {
  return api.get<WorkflowDoc>(`/workflows/${id}/export`);
}

export function armTestWebhook(id: string, node?: string): Promise<ArmedWebhook> {
  return api.post<ArmedWebhook>(`/workflows/${id}/test-webhook`, node ? { node } : undefined);
}

export interface RunBody {
  startNode?: string;
  items?: object[];
  destination?: { node: string; mode: 'inclusive' | 'exclusive' };
  pinData?: Record<string, unknown>;
  /** Unsaved document to run without saving it. */
  workflow?: WorkflowDoc;
}

export function runWorkflow(id: string, body: RunBody): Promise<{ executionId: string }> {
  return api.post(`/workflows/${id}/run`, body);
}

export interface ExpressionPreviewBody {
  expression: string;
  node: string;
  itemIndex: number;
  runIndex: number;
  executionId?: string;
  workflowId?: string;
  workflow?: WorkflowDoc;
}

export function previewExpressionRemote(body: ExpressionPreviewBody, signal?: AbortSignal): Promise<{ value: unknown }> {
  return api.post('/expressions/preview', body, undefined, signal);
}

// ---- Executions ----
export interface ExecutionFilter {
  status: ExecutionStatus[];
  workflowId?: string;
}

/** Executions list; `refetchMs` polls at that interval (false = no polling). */
export function useExecutions(filter: ExecutionFilter, refetchMs: number | false = false) {
  return useInfiniteQuery({
    refetchInterval: refetchMs,
    queryKey: keys.executions(filter),
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) =>
      api.get<Page<Execution>>('/executions', {
        status: filter.status,
        workflowId: filter.workflowId,
        cursor: pageParam,
      }),
    getNextPageParam: (last) => (last.hasMore ? last.nextCursor : undefined),
  });
}

export function fetchExecution(id: string): Promise<Execution> {
  return api.get<Execution>(`/executions/${id}`);
}

export function useExecution(id: string | null | undefined) {
  return useQuery({
    queryKey: keys.execution(id ?? ''),
    enabled: !!id,
    queryFn: () => fetchExecution(id ?? ''),
  });
}

export function useExecutionMutations() {
  const qc = useQueryClient();
  const refresh = () => qc.invalidateQueries({ queryKey: ['executions'] });
  return {
    remove: useMutation({ mutationFn: (id: string) => api.del(`/executions/${id}`), onSuccess: refresh }),
    cancel: useMutation({ mutationFn: (id: string) => api.post(`/executions/${id}/cancel`), onSuccess: refresh }),
    retry: useMutation({
      mutationFn: (id: string) => api.post<{ executionId: string }>(`/executions/${id}/retry`),
      onSuccess: refresh,
    }),
  };
}

// ---- Credentials ----
export function useCredentials() {
  return useQuery({
    queryKey: keys.credentials,
    queryFn: async () => {
      const out: CredentialSummary[] = [];
      let cursor: string | undefined;
      for (let i = 0; i < 20; i++) {
        const page: Page<CredentialSummary> = await api.get('/credentials', { cursor });
        out.push(...page.data);
        if (!page.hasMore) break;
        cursor = page.nextCursor;
      }
      return out;
    },
  });
}

export function fetchCredential(id: string): Promise<Credential> {
  return api.get<Credential>(`/credentials/${id}`);
}

export function useCredentialMutations() {
  const qc = useQueryClient();
  const refresh = () => qc.invalidateQueries({ queryKey: keys.credentials });
  return {
    create: useMutation({
      mutationFn: (b: { name: string; type: string; data: Record<string, unknown> }) =>
        api.post<Credential>('/credentials', b),
      onSuccess: refresh,
    }),
    update: useMutation({
      mutationFn: ({ id, ...b }: { id: string; name: string; data: Record<string, unknown> }) =>
        api.put<Credential>(`/credentials/${id}`, b),
      onSuccess: refresh,
    }),
    remove: useMutation({ mutationFn: (id: string) => api.del(`/credentials/${id}`), onSuccess: refresh }),
    test: useMutation({ mutationFn: (id: string) => api.post<{ success: boolean; message?: string }>(`/credentials/${id}/test`) }),
    authorize: useMutation({ mutationFn: (id: string) => api.post<{ url: string }>(`/credentials/${id}/authorize`) }),
  };
}

/** The workflow document as it was at a given version (used to show past executions). */
export async function fetchWorkflowVersion(id: string, version: number): Promise<WorkflowDoc> {
  return normalizeDoc((await api.get<{ workflow: WorkflowDoc }>(`/workflows/${id}/versions/${version}`)).workflow);
}

export interface VersionSummary {
  version: number;
  name: string;
  createdAt: string;
}

export function useWorkflowVersions(id: string) {
  return useInfiniteQuery({
    queryKey: ['wf-versions', id],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => api.get<Page<VersionSummary>>(`/workflows/${id}/versions`, { cursor: pageParam }),
    getNextPageParam: (last) => (last.hasMore ? last.nextCursor : undefined),
  });
}

/** Saves an old version as the new current one; returns the stored document. */
export async function restoreWorkflowVersion(id: string, version: number): Promise<LoadedWorkflow> {
  const doc = normalizeDoc(await api.post<WorkflowDoc>(`/workflows/${id}/versions/${version}/restore`));
  return { doc, version: doc.version ?? 1 };
}

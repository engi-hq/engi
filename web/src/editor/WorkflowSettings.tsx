import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';
import { fetchWorkflow, useWorkflows } from '../api/hooks';
import type { WfNode } from '../api/types';
import { t } from '../i18n/t';
import { Dialog } from '../ui/Dialog';
import { describeError } from '../ui/Toast';
import { useDebounced } from '../util/useDebounced';

/** Node type that receives failures from other workflows (see docs/04, error workflows). */
export const ERROR_TRIGGER_TYPE = 'engi.trigger.error';

export interface SettingsDraft {
  errorWorkflowId: string;
  timeoutSeconds: number;
}

export function draftOf(settings: Record<string, unknown> | undefined): SettingsDraft {
  const s = settings ?? {};
  return {
    errorWorkflowId: typeof s['errorWorkflowId'] === 'string' ? s['errorWorkflowId'] : '',
    timeoutSeconds: typeof s['timeoutSeconds'] === 'number' ? s['timeoutSeconds'] : 0,
  };
}

/** Writes the draft over the existing settings, leaving unknown keys alone and dropping unset values. */
export function settingsFromDraft(base: Record<string, unknown> | undefined, d: SettingsDraft): Record<string, unknown> {
  const out: Record<string, unknown> = { ...(base ?? {}) };
  const set = (k: string, v: unknown, on: boolean) => { if (on) out[k] = v; else delete out[k]; };
  set('errorWorkflowId', d.errorWorkflowId, d.errorWorkflowId !== '');
  set('timeoutSeconds', d.timeoutSeconds, d.timeoutSeconds > 0);
  return out;
}

/** True when the nodes include an enabled Error Trigger, the only way an error workflow can start. */
export function hasErrorTrigger(nodes: Pick<WfNode, 'type' | 'disabled'>[]): boolean {
  return nodes.some((n) => n.type === ERROR_TRIGGER_TYPE && !n.disabled);
}

interface Props {
  workflowId: string;
  /** Nodes of the document being edited, used for the "This workflow" check. */
  nodes: WfNode[];
  settings: Record<string, unknown> | undefined;
  readOnly: boolean;
  onApply: (settings: Record<string, unknown>) => void;
  onClose: () => void;
}

/** Workflow-wide settings. Applying changes the open document; the normal save stores them. */
export function WorkflowSettingsDialog({ workflowId, nodes, settings, readOnly, onApply, onClose }: Props) {
  const [draft, setDraft] = useState(() => draftOf(settings));
  const patch = (p: Partial<SettingsDraft>) => setDraft((d) => ({ ...d, ...p }));
  const apply = () => { onApply(settingsFromDraft(settings, draft)); onClose(); };
  return (
    <Dialog title={t('wfsettings.title')} onClose={onClose}>
      <div style={{ display: 'grid', gap: 14 }}>
        <ErrorWorkflowField workflowId={workflowId} nodes={nodes} value={draft.errorWorkflowId} readOnly={readOnly} onChange={(errorWorkflowId) => patch({ errorWorkflowId })} />
        <label className="field">
          <span className="field-label">{t('wfsettings.timeout')}</span>
          <input
            type="number"
            min={0}
            step={1}
            value={draft.timeoutSeconds}
            disabled={readOnly}
            onChange={(e) => patch({ timeoutSeconds: Math.max(0, Number(e.target.value) || 0) })}
          />
          <span className="field-hint">{t('wfsettings.timeoutHint')}</span>
        </label>
        <span className="field-hint">{t('wfsettings.saveNote')}</span>
      </div>
      <div className="dialog-actions">
        <button className="btn" onClick={onClose}>{t('common.cancel')}</button>
        <button className="btn primary" onClick={apply} disabled={readOnly}>{t('wfsettings.apply')}</button>
      </div>
    </Dialog>
  );
}

interface FieldProps {
  workflowId: string;
  nodes: WfNode[];
  value: string;
  readOnly: boolean;
  onChange: (id: string) => void;
}

/** Searchable single choice of a workflow, with explicit "None" and "This workflow" entries. */
function ErrorWorkflowField({ workflowId, nodes, value, readOnly, onChange }: FieldProps) {
  const [search, setSearch] = useState('');
  const q = useDebounced(search.trim());
  const list = useWorkflows({ q });
  const rows = (list.data?.pages.flatMap((p) => p.data) ?? []).filter((w) => w.id !== workflowId);
  const self = value !== '' && value === workflowId;

  // The chosen workflow may not be on the loaded page, so its nodes are fetched by id.
  const target = useQuery({
    queryKey: ['workflow-nodes', value],
    enabled: value !== '' && !self,
    queryFn: async () => (await fetchWorkflow(value)).doc,
  });
  const targetGone = target.isError;
  const noTrigger = value !== '' && (self ? !hasErrorTrigger(nodes) : target.isSuccess && !hasErrorTrigger(target.data.nodes));
  const nameOf = (id: string) => rows.find((w) => w.id === id)?.name ?? target.data?.name ?? id;

  return (
    <div className="field" role="group" aria-labelledby="wfs-ew-label">
      <span className="field-label" id="wfs-ew-label">{t('wfsettings.errorWorkflow')}</span>
      <span className="field-hint">{t('wfsettings.errorWorkflowHint')}</span>
      <input
        type="search"
        value={search}
        placeholder={t('wfsettings.searchWorkflows')}
        aria-label={t('wfsettings.searchWorkflows')}
        disabled={readOnly}
        onChange={(e) => setSearch(e.target.value)}
      />
      <ul role="listbox" aria-labelledby="wfs-ew-label" style={{ listStyle: 'none', margin: 0, padding: 0, maxHeight: 180, overflow: 'auto', display: 'grid', gap: 2 }}>
        <Option selected={value === ''} disabled={readOnly} onPick={() => onChange('')}>{t('wfsettings.none')}</Option>
        <Option selected={self} disabled={readOnly} onPick={() => onChange(workflowId)}>{t('wfsettings.thisWorkflow')}</Option>
        {rows.map((w) => (
          <Option key={w.id} selected={value === w.id} disabled={readOnly} onPick={() => onChange(w.id)}>{w.name}</Option>
        ))}
        {value !== '' && !self && !rows.some((w) => w.id === value) && (
          <Option selected disabled={readOnly} onPick={() => onChange(value)}>{nameOf(value)}</Option>
        )}
      </ul>
      {list.isError && <div className="callout error" role="alert">{describeError(list.error)}</div>}
      {list.isPending && <span className="spinner" aria-label={t('common.loading')} />}
      {list.hasNextPage && (
        <button className="btn sm" disabled={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()}>{t('history.more')}</button>
      )}
      {targetGone && <div className="callout warning" role="alert">{t('wfsettings.targetMissing')}</div>}
      {noTrigger && !targetGone && (
        <div className="callout warning" role="alert">{t(self ? 'wfsettings.noTriggerSelf' : 'wfsettings.noTrigger', { name: nameOf(value) })}</div>
      )}
    </div>
  );
}

function Option({ selected, disabled, onPick, children }: { selected: boolean; disabled: boolean; onPick: () => void; children: React.ReactNode }) {
  return (
    <li role="option" aria-selected={selected}>
      <button type="button" className={`btn sm${selected ? ' primary' : ' ghost'}`} style={{ width: '100%', justifyContent: 'flex-start' }} disabled={disabled} onClick={onPick}>
        {children}
      </button>
    </li>
  );
}

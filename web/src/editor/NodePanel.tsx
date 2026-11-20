import { useEffect, useMemo, useState } from 'react';
import type { CredentialSummary, Execution, NodeType, Property, WfNode } from '../api/types';
import { emptyEnv, FormEnvContext, type FormEnv } from '../forms/env';
import { effectiveValue, isVisible, validate, type Values } from '../forms/logic';
import { PropertyForm } from '../forms/PropertyForm';
import type { PreviewScope } from '../forms/preview';
import type { ServerPreviewTarget } from '../forms/useExpressionPreview';
import { t } from '../i18n/t';
import { Icon } from '../ui/Icon';
import { Switch } from '../ui/Switch';
import { DataView } from './DataView';
import { OpenChatButton } from './OpenChatButton';
import { PinControls } from './PinControls';
import { StepsView } from './StepsView';
import { lastRun } from './runState';
import { SETTINGS_PROPS, settingsToValues, valuesToSettings } from './settings';

type Tab = 'parameters' | 'settings' | 'data';

interface Props {
  node: WfNode;
  nt?: NodeType;
  nodeNames: string[];
  varKeys?: string[];
  credentials: CredentialSummary[];
  execution?: Execution;
  readOnly: boolean;
  /** Pinned output of this node, if any. */
  pinned?: object[];
  onPin: (items: object[] | undefined) => void;
  running?: boolean;
  onRunTo: (mode: 'inclusive' | 'exclusive') => void;
  serverPreview?: ServerPreviewTarget;
  onPatch: (patch: Partial<WfNode>, key?: string) => void;
  onRename: (to: string) => { ok: boolean; reason?: string };
  onDelete: () => void;
  onClose: () => void;
  onCreateCredential: (type: string, slot: string) => void;
}

const settingsEnv: FormEnv = { ...emptyEnv, expressions: false };

/** Scope for expression previews: first input item of this node, first output item of every node. */
export function previewScope(ex: Execution | undefined, node: string): PreviewScope {
  const run = lastRun(ex, node);
  const nodes: PreviewScope['nodes'] = {};
  for (const [name, runs] of Object.entries(ex?.runData ?? {})) {
    const last = runs[runs.length - 1];
    nodes[name] = last?.output?.main?.flat()[0]?.json;
  }
  return { json: run?.input?.main?.flat()[0]?.json, nodes };
}

export function NodePanel(p: Props) {
  const { node, nt } = p;
  const [tab, setTab] = useState<Tab>('parameters');
  const [name, setName] = useState(node.name);
  const [nameError, setNameError] = useState('');
  const [showErrors, setShowErrors] = useState(false);

  useEffect(() => { setName(node.name); setNameError(''); setShowErrors(false); }, [node.name]);

  const params = useMemo(() => (node.parameters ?? {}) as Values, [node.parameters]);
  const scope = useMemo(() => previewScope(p.execution, node.name), [p.execution, node.name]);
  const env = useMemo<FormEnv>(() => ({
    credentials: p.credentials,
    preview: scope,
    jsonFields: Object.keys(scope.json ?? {}),
    nodeNames: p.nodeNames,
    varKeys: p.varKeys,
    readOnly: p.readOnly,
    expressions: true,
    serverPreview: p.serverPreview,
  }), [p.credentials, scope, p.nodeNames, p.varKeys, p.readOnly, p.serverPreview]);

  const errors = useMemo(() => (showErrors && nt ? validate(nt.properties, params) : {}), [showErrors, nt, params]);

  const commitName = () => {
    if (name === node.name) return;
    const r = p.onRename(name);
    if (!r.ok) setNameError(t(r.reason === 'duplicate' ? 'node.nameTaken' : 'node.nameEmpty'));
    else setNameError('');
  };

  const sets = settingsToValues(node.settings, node.notes);

  return (
    <aside className="panel" aria-label={t('panel.label', { name: node.name })}>
      <div className="panel-head">
        <span className="ncard-icon"><Icon name={nt?.icon ?? 'workflow'} size={16} /></span>
        <div style={{ flex: 1, minWidth: 0 }}>
          <input
            className="panel-name"
            type="text"
            value={name}
            disabled={p.readOnly}
            aria-label={t('node.name')}
            aria-invalid={nameError ? true : undefined}
            onChange={(e) => setName(e.target.value)}
            onBlur={commitName}
            onKeyDown={(e) => { if (e.key === 'Enter') { e.currentTarget.blur(); } if (e.key === 'Escape') { setName(node.name); } }}
          />
          <div className="field-hint">{nt?.displayName ?? node.type}</div>
        </div>
        <button className="btn ghost icon sm" onClick={p.onClose} aria-label={t('common.close')}><Icon name="close" /></button>
      </div>
      {nameError && <div className="field-error" role="alert" style={{ padding: '0 14px' }}>{nameError}</div>}
      {!nt && <div className="callout warning" style={{ margin: 14 }}>{t('canvas.unknownType')}</div>}
      <div role="tablist" className="tabs" aria-label={t('panel.tabs')}>
        {(['parameters', 'settings', 'data'] as Tab[]).map((id) => (
          <button key={id} role="tab" id={`tab-${id}`} aria-selected={tab === id} aria-controls={`tabpanel-${id}`} onClick={() => setTab(id)}>
            {t(`panel.tab.${id}`)}
          </button>
        ))}
      </div>
      <div role="tabpanel" id={`tabpanel-${tab}`} aria-labelledby={`tab-${tab}`} className="panel-body">
        {tab === 'parameters' && nt && (
          <FormEnvContext.Provider value={env}>
            {node.type === 'engi.trigger.chat' && <OpenChatButton node={node} />}
            <CredentialSlots node={node} nt={nt} credentials={p.credentials} readOnly={p.readOnly} onPatch={p.onPatch} onCreate={p.onCreateCredential} />
            <PropertyForm
              properties={nt.properties}
              values={params}
              errors={errors}
              onChange={(next) => { p.onPatch({ parameters: next }, `params:${node.name}`); }}
            />
            {!p.readOnly && (
              <button className="btn sm" onClick={() => setShowErrors(true)}>{t('panel.checkRequired')}</button>
            )}
          </FormEnvContext.Provider>
        )}
        {tab === 'settings' && (
          <FormEnvContext.Provider value={{ ...settingsEnv, readOnly: p.readOnly }}>
            <div style={{ display: 'grid', gap: 14 }}>
              <Switch
                checked={!!node.disabled}
                disabled={p.readOnly}
                label={t('node.disabled')}
                onChange={(disabled) => p.onPatch({ disabled })}
              />
              <PropertyForm
                properties={SETTINGS_PROPS}
                values={sets}
                onChange={(next) => {
                  const { settings, notes } = valuesToSettings(next);
                  p.onPatch({ settings, notes }, `settings:${node.name}`);
                }}
              />
            </div>
          </FormEnvContext.Provider>
        )}
        {tab === 'data' && <DataTab node={node} execution={p.execution} pinned={p.pinned} readOnly={p.readOnly} onPin={p.onPin} />}
      </div>
      {!p.readOnly && (
        <div className="panel-foot">
          <button className="btn sm" disabled={p.running} onClick={() => p.onRunTo('inclusive')}><Icon name="play" /> {t('run.to')}</button>
          <button className="btn sm" disabled={p.running} onClick={() => p.onRunTo('exclusive')}>{t('run.upTo')}</button>
          <button className="btn danger sm" onClick={p.onDelete}><Icon name="trash" /> {t('node.delete')}</button>
        </div>
      )}
    </aside>
  );
}

/** Which credential slots apply: slots selected by an options parameter only show when chosen. */
export function visibleSlots(nt: NodeType, params: Values) {
  return nt.credentials.filter((slot) => {
    const selector = nt.properties.find(
      (pr) => pr.type === 'options' && pr.options?.some((o) => String(o.value) === slot.name),
    );
    if (!selector) return true;
    if (!isVisible(selector, nt.properties, params)) return false;
    return String(effectiveValue(selector as Property, params)) === slot.name;
  });
}

function CredentialSlots({ node, nt, credentials, readOnly, onPatch, onCreate }: {
  node: WfNode; nt: NodeType; credentials: CredentialSummary[]; readOnly: boolean;
  onPatch: (patch: Partial<WfNode>) => void; onCreate: (type: string, slot: string) => void;
}) {
  const slots = visibleSlots(nt, (node.parameters ?? {}) as Values);
  if (slots.length === 0) return null;
  return (
    <div className="pform" style={{ marginBottom: 14 }}>
      {slots.map((slot) => {
        const list = credentials.filter((c) => !slot.type || c.type === slot.type);
        const value = node.credentials?.[slot.name] ?? '';
        const label = slot.type ? t('cred.slot', { type: slot.type }) : t('cred.slotAny');
        return (
          <div key={slot.name} className="pfield">
            <label className="field-label" htmlFor={`slot-${slot.name}`}>
              {label}{slot.required && <span className="req"> *</span>}
            </label>
            <div className="inline">
              <select
                id={`slot-${slot.name}`}
                value={value}
                disabled={readOnly}
                onChange={(e) => {
                  const next = { ...(node.credentials ?? {}) };
                  if (e.target.value) next[slot.name] = e.target.value; else delete next[slot.name];
                  onPatch({ credentials: Object.keys(next).length ? next : undefined });
                }}
              >
                <option value="">{t('form.noCredential')}</option>
                {list.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
              </select>
              {!readOnly && slot.type && (
                <button className="btn" onClick={() => onCreate(slot.type, slot.name)}>{t('cred.createInline')}</button>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}

function DataTab({ node, execution, pinned, readOnly, onPin }: { node: WfNode; execution?: Execution; pinned?: object[]; readOnly: boolean; onPin: (items: object[] | undefined) => void }) {
  const runs = execution?.runData?.[node.name] ?? [];
  const [runIdx, setRunIdx] = useState(runs.length - 1);
  const [branch, setBranch] = useState(0);
  useEffect(() => { setRunIdx(runs.length - 1); setBranch(0); }, [runs.length, node.name]);
  const run = runs[Math.max(0, Math.min(runIdx, runs.length - 1))];
  if (!execution || !run) {
    // A node that has not run can still hold pinned data.
    return (
      <div style={{ display: 'grid', gap: 14 }}>
        <div className="empty">{t(execution ? 'data.nodeNotRun' : 'data.noRun')}</div>
        <PinControls output={[]} pinned={pinned} readOnly={readOnly} onPin={onPin} />
      </div>
    );
  }
  const input = run.input?.main?.flat() ?? [];
  const ports = run.output?.main ?? [];
  const output = ports[branch] ?? [];
  return (
    <div style={{ display: 'grid', gap: 14 }}>
      <div className="toolbar">
        {runs.length > 1 && (
          <label className="field-label">{t('data.run')}{' '}
            <select value={runIdx} onChange={(e) => setRunIdx(Number(e.target.value))} style={{ width: 'auto' }}>
              {runs.map((_, i) => <option key={i} value={i}>{i + 1} / {runs.length}</option>)}
            </select>
          </label>
        )}
        <span className={`badge ${run.status}`}>{run.status}</span>
        {run.durationMs !== undefined && <span className="field-hint">{run.durationMs} ms</span>}
      </div>
      {run.error && <div className="callout error" role="alert"><strong>{run.error.message}</strong>{run.error.description && <div>{run.error.description}</div>}{run.error.itemIndex !== undefined && <div>{t('data.itemIndex', { n: run.error.itemIndex })}</div>}</div>}
      <h3>{t('data.input')}</h3>
      <DataView items={input} label={t('data.inputOf', { name: node.name })} executionId={execution.id} />
      <h3>{t('data.output')}</h3>
      {ports.length > 1 && (
        <div className="seg" role="group" aria-label={t('data.branch')}>
          {ports.map((pItems, i) => (
            <button key={i} type="button" aria-pressed={branch === i} onClick={() => setBranch(i)}>{t('data.branchN', { n: i, count: pItems?.length ?? 0 })}</button>
          ))}
        </div>
      )}
      <StepsView items={output} />
      <DataView items={output} label={t('data.outputOf', { name: node.name })} executionId={execution.id} />
      <PinControls output={output.map((i) => i.json ?? {})} pinned={pinned} readOnly={readOnly} onPin={onPin} />
    </div>
  );
}

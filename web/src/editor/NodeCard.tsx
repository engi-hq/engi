import { Handle, Position, type Node, type NodeProps } from '@xyflow/react';
import { memo } from 'react';
import type { NodeType, WfNode } from '../api/types';
import { t, tn } from '../i18n/t';
import { Icon } from '../ui/Icon';
import { isSubKind } from './model';
import type { NodeRunState } from './runState';

export interface CardData extends Record<string, unknown> {
  node: WfNode;
  nt?: NodeType;
  state?: NodeRunState;
  connectSource?: boolean;
  pinned?: boolean;
}
export type CardNode = Node<CardData, 'engi'>;

const PORT_GAP = 26;
const HEAD = 44;
/** Height of the strip along the bottom edge that labels sub-port inputs. */
const SUB_STRIP = 22;

export const groupVar = (g?: string) =>
  ({ trigger: 'var(--g-trigger)', flow: 'var(--g-flow)', transform: 'var(--g-transform)', action: 'var(--g-action)', code: 'var(--g-code)', ai: 'var(--g-ai)' } as Record<string, string>)[g ?? ''] ?? 'var(--g-other)';

function statusText(s: NodeRunState | undefined): string {
  if (!s) return '';
  const n = s.outputItems?.reduce((a, b) => a + b, 0);
  return t(`run.node.${s.status}`) + (n !== undefined && s.status === 'success' ? ` · ${tn('run.items', n)}` : '');
}

interface Placed {
  flat: number;
  spec: NodeType['inputs'][number];
}

/** Ports of one kind class, keeping their index in the descriptor (handle ids use it). */
const placed = (ports: NodeType['inputs'], sub: boolean): Placed[] =>
  ports.map((spec, flat) => ({ flat, spec })).filter((p) => isSubKind(p.spec.kind) === sub);

/** Short label of a sub-port kind: `ai.tool` becomes "tool". */
const kindLabel = (kind: string) => kind.replace(/^ai\./, '');

function NodeCardImpl({ data, selected }: NodeProps<CardNode>) {
  const { node, nt, state } = data;
  const mainIn = placed(nt?.inputs ?? [], false);
  const mainOut = placed(nt?.outputs ?? [], false);
  const subIn = placed(nt?.inputs ?? [], true);
  const subOut = placed(nt?.outputs ?? [], true);
  const rows = Math.max(mainIn.length, mainOut.length, 1);
  const height = Math.max(64, HEAD + rows * PORT_GAP - 8) + (subIn.length > 0 ? SUB_STRIP : 0);
  const mainHeight = height - (subIn.length > 0 ? SUB_STRIP : 0);
  const label = [node.name, nt?.displayName ?? node.type, node.disabled ? t('canvas.disabled') : '', statusText(state)].filter(Boolean).join(', ');
  const portTop = (i: number, n: number) => (n === 1 ? mainHeight / 2 : HEAD - 8 + i * PORT_GAP + PORT_GAP / 2 - 2);
  const spread = (i: number, n: number) => `${((i + 1) / (n + 1)) * 100}%`;
  return (
    <div
      className={`ncard${selected ? ' selected' : ''}${node.disabled ? ' disabled' : ''}${data.connectSource ? ' connect-src' : ''} st-${state?.status ?? 'idle'}${subOut.length > 0 ? ' subnode' : ''}`}
      style={{ height, ['--accent-g' as string]: groupVar(nt?.group) }}
      aria-label={label}
      data-node={node.name}
    >
      {data.pinned && <div className="ncard-pin" title={t('pin.badge')} aria-label={t('pin.badge')}><Icon name="pin" size={12} /></div>}
      {!nt && <div className="ncard-warn" title={t('canvas.unknownType')}><Icon name="alert" size={12} /></div>}
      <div className="ncard-head">
        <span className="ncard-icon"><Icon name={nt?.icon ?? 'workflow'} size={16} /></span>
        <div className="ncard-titles">
          <div className="ncard-title">{node.name}</div>
          <div className="ncard-sub">{nt?.displayName ?? node.type}</div>
        </div>
        <span className={`ncard-status st-${state?.status ?? 'idle'}`} title={state?.error ?? statusText(state)}>
          {state?.status === 'running' && <span className="spinner" />}
          {state?.status === 'success' && <Icon name="check" size={14} />}
          {state?.status === 'error' && <Icon name="alert" size={14} />}
          {state?.status === 'waiting' && <Icon name="clock" size={14} />}
        </span>
      </div>
      {state?.status === 'success' && state.outputItems && (
        <div className="ncard-count">{tn('run.items', state.outputItems.reduce((a, b) => a + b, 0))}</div>
      )}
      {mainIn.map((p, i) => (
        <Handle
          key={`in-${p.flat}`}
          id={`in-${p.flat}`}
          type="target"
          position={Position.Left}
          className="port main"
          style={{ top: portTop(i, mainIn.length) }}
          aria-label={t('canvas.inputPort', { name: p.spec.displayName ?? p.spec.name })}
        />
      ))}
      {mainOut.map((p, i) => (
        <span key={`out-${p.flat}`}>
          <Handle
            id={`out-${p.flat}`}
            type="source"
            position={Position.Right}
            className="port main"
            style={{ top: portTop(i, mainOut.length) }}
            aria-label={t('canvas.outputPort', { name: p.spec.displayName ?? p.spec.name })}
          />
          {mainOut.length > 1 && (
            <span className="port-label" style={{ top: portTop(i, mainOut.length) - 8 }}>{p.spec.displayName ?? p.spec.name}</span>
          )}
        </span>
      ))}
      {subIn.map((p, i) => (
        <span key={`sin-${p.flat}`}>
          <Handle
            id={`in-${p.flat}`}
            type="target"
            position={Position.Bottom}
            className={`port sub kind-${kindLabel(p.spec.kind)}`}
            style={{ left: spread(i, subIn.length) }}
            aria-label={t('canvas.subInputPort', { name: p.spec.displayName ?? p.spec.name, kind: p.spec.kind })}
          />
          <span className="sub-label" style={{ left: spread(i, subIn.length) }} data-kind={p.spec.kind}>
            {p.spec.displayName ?? kindLabel(p.spec.kind)}{p.spec.required && <span className="req">*</span>}
          </span>
        </span>
      ))}
      {subOut.map((p, i) => (
        <Handle
          key={`sout-${p.flat}`}
          id={`out-${p.flat}`}
          type="source"
          position={Position.Top}
          className={`port sub kind-${kindLabel(p.spec.kind)}`}
          style={{ left: spread(i, subOut.length) }}
          aria-label={t('canvas.subOutputPort', { name: p.spec.displayName ?? p.spec.name, kind: p.spec.kind })}
        />
      ))}
    </div>
  );
}

export const NodeCard = memo(NodeCardImpl);

import type { Item } from '../api/types';
import { t, tn } from '../i18n/t';
import { JsonView } from './DataView';

export interface AgentStep {
  tool: string;
  input: unknown;
  output: unknown;
  isError: boolean;
  durationMs?: number;
  iteration?: number;
}

export interface AgentRunInfo {
  steps: AgentStep[];
  iterations?: number;
  stopReason?: string;
  usage?: { inputTokens?: number; outputTokens?: number };
}

const isObj = (v: unknown): v is Record<string, unknown> => v !== null && typeof v === 'object' && !Array.isArray(v);

/** The agent info of an output item, or null when the item is not an agent result (no `steps` array of tool calls). */
export function agentInfo(item: Item): AgentRunInfo | null {
  const j = item.json;
  if (!j || !Array.isArray(j['steps'])) return null;
  const steps: AgentStep[] = [];
  for (const s of j['steps'] as unknown[]) {
    if (!isObj(s) || typeof s['tool'] !== 'string') return null;
    steps.push({
      tool: s['tool'], input: s['input'], output: s['output'], isError: s['isError'] === true,
      durationMs: typeof s['durationMs'] === 'number' ? s['durationMs'] : undefined,
      iteration: typeof s['iteration'] === 'number' ? s['iteration'] : undefined,
    });
  }
  const usage = isObj(j['usage']) ? (j['usage'] as AgentRunInfo['usage']) : undefined;
  return {
    steps, usage,
    iterations: typeof j['iterations'] === 'number' ? j['iterations'] : undefined,
    stopReason: typeof j['stopReason'] === 'string' ? j['stopReason'] : undefined,
  };
}

/** True when any output item looks like an agent result. */
export const hasAgentSteps = (items: Item[]): boolean => items.some((i) => agentInfo(i) !== null);

/** The tool calls an AI Agent made, per output item: what it asked, what came back, how long it took. */
export function StepsView({ items }: { items: Item[] }) {
  const runs = items.map((it, i) => ({ i, info: agentInfo(it) })).filter((r): r is { i: number; info: AgentRunInfo } => r.info !== null);
  if (runs.length === 0) return null;
  return (
    <section className="steps" aria-label={t('steps.title')}>
      <h3>{t('steps.title')}</h3>
      {runs.map(({ i, info }) => (
        <div key={i} className="steps-item" role="group" aria-label={t('steps.item', { n: i })}>
          <div className="toolbar">
            {runs.length > 1 && <strong>{t('steps.item', { n: i })}</strong>}
            <span className="field-hint">{tn('steps.summary', info.steps.length)}</span>
            {info.iterations !== undefined && <span className="field-hint">{t('steps.iterations', { n: info.iterations })}</span>}
            {info.usage && (
              <span className="field-hint">{t('steps.usage', { input: info.usage.inputTokens ?? 0, output: info.usage.outputTokens ?? 0 })}</span>
            )}
            {info.stopReason && info.stopReason !== 'end' && <span className="badge error">{t('steps.stop', { reason: info.stopReason })}</span>}
          </div>
          {info.steps.length === 0 ? (
            <div className="empty">{t('steps.none')}</div>
          ) : (
            <ol className="steps-list">
              {info.steps.map((s, k) => (
                <li key={k}>
                  <details className={`step${s.isError ? ' step-error' : ''}`}>
                    <summary>
                      <strong>{s.tool}</strong>
                      {s.isError && <span className="badge error">{t('steps.failed')}</span>}
                      {s.durationMs !== undefined && <span className="field-hint">{s.durationMs} ms</span>}
                    </summary>
                    <h4>{t('steps.input')}</h4>
                    <JsonView value={s.input} />
                    <h4>{t('steps.output')}</h4>
                    <JsonView value={s.output} />
                  </details>
                </li>
              ))}
            </ol>
          )}
        </div>
      ))}
    </section>
  );
}

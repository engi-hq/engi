import type { NodeSettings, Property } from '../api/types';
import type { Values } from '../forms/logic';

/** Node settings are generic (not per descriptor), so their form schema lives here and renders through PropertyForm. */
export const SETTINGS_PROPS: Property[] = [
  { name: 'maxAttempts', type: 'number', displayName: 'Retry: total attempts', default: 1, description: '0 or 1 means no retry.' },
  {
    name: 'backoff', type: 'options', displayName: 'Retry: backoff', default: 'fixed',
    options: [{ name: 'Fixed delay', value: 'fixed' }, { name: 'Exponential', value: 'exponential' }],
    displayOptions: { hide: { maxAttempts: [0, 1] } },
  },
  { name: 'initialDelayMs', type: 'number', displayName: 'Retry: delay (ms)', default: 1000, displayOptions: { hide: { maxAttempts: [0, 1] } } },
  {
    name: 'onError', type: 'options', displayName: 'On error', default: 'stop',
    options: [
      { name: 'Stop the execution', value: 'stop' },
      { name: 'Continue with the error as an item', value: 'continue' },
      { name: 'Send to the error output', value: 'errorOutput' },
    ],
  },
  { name: 'alwaysOutputData', type: 'boolean', displayName: 'Always output data', default: false, description: 'Emit one empty item when the node returns nothing.' },
  { name: 'executeOnce', type: 'boolean', displayName: 'Execute once', default: false, description: 'Run only for the first input item.' },
  { name: 'timeoutMs', type: 'number', displayName: 'Timeout (ms)', default: 0, description: '0 uses the server default.' },
  { name: 'notes', type: 'string', displayName: 'Notes' },
];

export function settingsToValues(s: NodeSettings | undefined, notes: string | undefined): Values {
  const v: Values = {};
  if (s?.retry?.maxAttempts !== undefined) v['maxAttempts'] = s.retry.maxAttempts;
  if (s?.retry?.backoff) v['backoff'] = s.retry.backoff;
  if (s?.retry?.initialDelayMs !== undefined) v['initialDelayMs'] = s.retry.initialDelayMs;
  if (s?.onError) v['onError'] = s.onError;
  if (s?.alwaysOutputData !== undefined) v['alwaysOutputData'] = s.alwaysOutputData;
  if (s?.executeOnce !== undefined) v['executeOnce'] = s.executeOnce;
  if (s?.timeoutMs !== undefined) v['timeoutMs'] = s.timeoutMs;
  if (notes) v['notes'] = notes;
  return v;
}

/** Back to the wire shape. Only values that differ from "unset" are written. */
export function valuesToSettings(v: Values): { settings: NodeSettings; notes: string } {
  const s: NodeSettings = {};
  const attempts = v['maxAttempts'];
  if (typeof attempts === 'number') {
    s.retry = { maxAttempts: attempts };
    if (typeof v['backoff'] === 'string') s.retry.backoff = v['backoff'];
    if (typeof v['initialDelayMs'] === 'number') s.retry.initialDelayMs = v['initialDelayMs'];
  }
  if (typeof v['onError'] === 'string') s.onError = v['onError'] as NodeSettings['onError'];
  if (typeof v['alwaysOutputData'] === 'boolean') s.alwaysOutputData = v['alwaysOutputData'];
  if (typeof v['executeOnce'] === 'boolean') s.executeOnce = v['executeOnce'];
  if (typeof v['timeoutMs'] === 'number') s.timeoutMs = v['timeoutMs'];
  return { settings: s, notes: typeof v['notes'] === 'string' ? v['notes'] : '' };
}

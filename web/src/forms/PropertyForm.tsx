import { useId, useState } from 'react';
import type { Property } from '../api/types';
import { t } from '../i18n/t';
import { Icon } from '../ui/Icon';
import { Switch } from '../ui/Switch';
import { useFormEnv } from './env';
import { effectiveValue, fromExpression, isExpression, isVisible, toExpression, type Values } from './logic';
import { LazyCode } from './LazyCode';
import { previewExpression } from './preview';
import { useExpressionPreview } from './useExpressionPreview';

interface FormProps {
  properties: Property[];
  values: Values;
  onChange: (next: Values) => void;
  /** Root parameters, used to resolve displayOptions of nested fields. */
  root?: Values;
  errors?: Record<string, string>;
  prefix?: string;
}

/** Renders a property list from a node descriptor. It knows no node by name. */
export function PropertyForm({ properties, values, onChange, root, errors = {}, prefix = '' }: FormProps) {
  const set = (name: string, v: unknown) => {
    const next = { ...values };
    if (v === undefined) delete next[name];
    else next[name] = v;
    onChange(next);
  };
  return (
    <div className="pform">
      {properties
        .filter((p) => isVisible(p, properties, values, root ?? values))
        .map((p) => (
          <Field
            key={p.name}
            prop={p}
            value={effectiveValue(p, values)}
            onChange={(v) => set(p.name, v)}
            root={root ?? values}
            error={errors[prefix + p.name]}
            errors={errors}
            path={prefix + p.name}
          />
        ))}
    </div>
  );
}

interface FieldProps {
  prop: Property;
  value: unknown;
  onChange: (v: unknown) => void;
  root: Values;
  error?: string;
  errors: Record<string, string>;
  path: string;
}

const EXPRESSION_TYPES = new Set(['string', 'number', 'boolean', 'options', 'json', 'dateTime', 'resourceLocator']);

function Field({ prop, value, onChange, root, error, errors, path }: FieldProps) {
  const env = useFormEnv();
  const id = useId();
  const label = prop.displayName ?? prop.name;
  const expr = isExpression(value);

  if (prop.type === 'notice') {
    return <div className="callout" role="note">{prop.description ?? label}</div>;
  }

  const canToggle = EXPRESSION_TYPES.has(prop.type) && !env.readOnly && env.expressions;
  const labelEl = (
    <span className="field-label">
      {label}
      {prop.required && <span className="req" aria-hidden="true"> *</span>}
      {prop.required && <span className="sr-only"> ({t('form.required')})</span>}
    </span>
  );

  const isComposite = prop.type === 'collection' || prop.type === 'fixedCollection';

  return (
    <div className={`pfield${isComposite ? ' composite' : ''}`} data-field={path}>
      <div className="pfield-head">
        {isComposite || prop.type === 'multiOptions' ? <span id={`${id}-l`}>{labelEl}</span> : <label htmlFor={id}>{labelEl}</label>}
        <span className="spacer" />
        {canToggle && (
          <div className="seg" role="group" aria-label={t('form.mode', { name: label })}>
            <button type="button" aria-pressed={!expr} onClick={() => expr && onChange(fromExpression(value as string, prop))}>
              {t('form.fixed')}
            </button>
            <button type="button" aria-pressed={expr} onClick={() => !expr && onChange(toExpression(value))}>
              {t('form.expression')}
            </button>
          </div>
        )}
      </div>
      {expr ? (
        <ExpressionControl id={id} label={label} value={value as string} onChange={onChange} />
      ) : (
        <Control id={id} prop={prop} value={value} onChange={onChange} root={root} errors={errors} path={path} error={error} labelId={`${id}-l`} />
      )}
      {prop.description && prop.type !== 'notice' && <div className="field-hint">{prop.description}</div>}
      {error && <div className="field-error" role="alert">{error === 'required' ? t('form.requiredError', { name: label }) : error}</div>}
    </div>
  );
}

function ExpressionControl({ id, label, value, onChange }: { id: string; label: string; value: string; onChange: (v: unknown) => void }) {
  const env = useFormEnv();
  const preview = previewExpression(value, env.preview);
  const remote = useExpressionPreview(value, env.serverPreview);
  return (
    <div className="expr" id={id}>
      <LazyCode
        language="expression"
        value={value.slice(1)}
        onChange={(v) => onChange(`=${v}`)}
        ariaLabel={t('form.expressionFor', { name: label })}
        jsonFields={env.jsonFields}
        nodeNames={env.nodeNames}
        varKeys={env.varKeys}
        placeholder="{{ $json.field }}"
      />
      <div className="expr-preview" aria-live="polite">
        <span className="field-label">{t('form.preview')}</span>{' '}
        {remote.state === 'value' ? (
          <code>{remote.value}</code>
        ) : remote.state === 'error' ? (
          <span className="field-error" role="alert">{remote.message}</span>
        ) : remote.state === 'loading' ? (
          <span className="field-hint">{t('form.preview.loading')}</span>
        ) : preview.ok ? (
          <code>{preview.value}</code>
        ) : (
          <span className="field-hint">{t(`form.preview.${preview.reason}`)}</span>
        )}
      </div>
    </div>
  );
}

interface ControlProps {
  id: string;
  labelId: string;
  prop: Property;
  value: unknown;
  onChange: (v: unknown) => void;
  root: Values;
  errors: Record<string, string>;
  path: string;
  error?: string;
}

function Control(p: ControlProps) {
  const env = useFormEnv();
  const { prop, value, onChange, id } = p;
  const ro = env.readOnly;
  const invalid = p.error ? true : undefined;
  switch (prop.type) {
    case 'boolean':
      return (
        <Switch checked={value === true} onChange={onChange} label={prop.displayName ?? prop.name} hideLabel disabled={ro} />
      );
    case 'number':
      return (
        <input
          id={id}
          type="number"
          value={typeof value === 'number' ? value : ''}
          disabled={ro}
          aria-invalid={invalid}
          placeholder={prop.placeholder}
          onChange={(e) => onChange(e.target.value === '' ? undefined : Number(e.target.value))}
        />
      );
    case 'options':
      return (
        <>
          <select id={id} value={String(value ?? '')} disabled={ro} aria-invalid={invalid} onChange={(e) => {
            const o = prop.options?.find((x) => String(x.value) === e.target.value);
            onChange(o ? o.value : e.target.value);
          }}>
            {!prop.options?.some((o) => String(o.value) === String(value ?? '')) && <option value="">{t('form.choose')}</option>}
            {prop.options?.map((o) => <option key={String(o.value)} value={String(o.value)}>{o.name}</option>)}
          </select>
          {prop.options?.find((o) => String(o.value) === String(value))?.description && (
            <div className="field-hint">{prop.options.find((o) => String(o.value) === String(value))?.description}</div>
          )}
        </>
      );
    case 'multiOptions': {
      const chosen = Array.isArray(value) ? (value as unknown[]) : [];
      return (
        <div role="group" aria-labelledby={p.labelId} className="checks">
          {prop.options?.map((o) => (
            <label key={String(o.value)} className="check">
              <input
                type="checkbox"
                disabled={ro}
                checked={chosen.includes(o.value)}
                onChange={(e) => onChange(e.target.checked ? [...chosen, o.value] : chosen.filter((x) => x !== o.value))}
              />
              {o.name}
            </label>
          ))}
        </div>
      );
    }
    case 'json': {
      const text = typeof value === 'string' ? value : value === undefined ? '' : JSON.stringify(value, null, 2);
      return <JsonControl id={id} text={text} onChange={onChange} label={prop.displayName ?? prop.name} ro={ro} placeholder={prop.placeholder} />;
    }
    case 'code':
      return (
        <div id={id}>
          <LazyCode language="js" value={typeof value === 'string' ? value : ''} onChange={onChange} ariaLabel={prop.displayName ?? prop.name} minHeight={120} />
        </div>
      );
    case 'dateTime':
      return <DateTimeControl id={id} value={typeof value === 'string' ? value : ''} onChange={onChange} ro={ro} />;
    case 'collection':
      return <CollectionControl {...p} />;
    case 'fixedCollection':
      return <FixedCollectionControl {...p} />;
    case 'credentialsSelect': {
      const types = (prop.options ?? []).map((o) => String(o.value));
      const list = env.credentials.filter((c) => types.length === 0 || types.includes(c.type));
      return (
        <select id={id} value={String(value ?? '')} disabled={ro} aria-invalid={invalid} onChange={(e) => onChange(e.target.value || undefined)}>
          <option value="">{t('form.noCredential')}</option>
          {list.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
        </select>
      );
    }
    default: {
      if (prop.secret) return <SecretInput id={id} value={typeof value === 'string' ? value : ''} onChange={onChange} ro={ro} />;
      // string, resourceLocator and any unknown type fall back to text.
      const s = typeof value === 'string' ? value : value === undefined || value === null ? '' : String(value);
      return s.includes('\n') ? (
        <textarea id={id} value={s} disabled={ro} aria-invalid={invalid} onChange={(e) => onChange(e.target.value)} />
      ) : (
        <input id={id} type="text" value={s} disabled={ro} aria-invalid={invalid} placeholder={prop.placeholder} onChange={(e) => onChange(e.target.value)} />
      );
    }
  }
}

export const SECRET_SENTINEL = '__ENGI_SECRET__';

/** Secret field: the server's sentinel means "stored, unchanged"; typing replaces it. */
function SecretInput({ id, value, onChange, ro }: { id: string; value: string; onChange: (v: unknown) => void; ro: boolean }) {
  if (value === SECRET_SENTINEL) {
    return (
      <div className="inline">
        <input id={id} type="password" value="" disabled placeholder={t('cred.secretStored')} aria-label={t('cred.secretStored')} />
        {!ro && <button type="button" className="btn" onClick={() => onChange('')}>{t('cred.replaceSecret')}</button>}
      </div>
    );
  }
  return <input id={id} type="password" autoComplete="new-password" value={value} disabled={ro} onChange={(e) => onChange(e.target.value)} />;
}

function JsonControl({ id, text, onChange, label, ro, placeholder }: { id: string; text: string; onChange: (v: unknown) => void; label: string; ro: boolean; placeholder?: string }) {
  let problem = '';
  if (text.trim() !== '' && !text.includes('{{')) {
    try {
      JSON.parse(text);
    } catch (e) {
      problem = e instanceof Error ? e.message : String(e);
    }
  }
  return (
    <div id={id}>
      {ro ? <pre className="readonly-code">{text}</pre> : (
        <LazyCode language="json" value={text} onChange={(v) => onChange(v === '' ? undefined : v)} ariaLabel={label} placeholder={placeholder} minHeight={96} />
      )}
      {problem && <div className="field-error" role="status">{t('form.invalidJson')}: {problem}</div>}
    </div>
  );
}

function DateTimeControl({ id, value, onChange, ro }: { id: string; value: string; onChange: (v: unknown) => void; ro: boolean }) {
  const [utc, setUtc] = useState(() => value.endsWith('Z') || value === '');
  const local = value ? toLocalInput(value, utc) : '';
  const commit = (raw: string, asUtc: boolean) => {
    if (!raw) return onChange(undefined);
    onChange(fromLocalInput(raw, asUtc));
  };
  return (
    <div className="inline">
      <input id={id} type="datetime-local" value={local} disabled={ro} onChange={(e) => commit(e.target.value, utc)} />
      <select aria-label={t('form.timezone')} value={utc ? 'utc' : 'local'} disabled={ro} onChange={(e) => { const u = e.target.value === 'utc'; setUtc(u); commit(local, u); }}>
        <option value="utc">UTC</option>
        <option value="local">{t('form.localTime')}</option>
      </select>
    </div>
  );
}

const pad = (n: number) => String(n).padStart(2, '0');

export function toLocalInput(iso: string, utc: boolean): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return utc
    ? `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}T${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}`
    : `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export function fromLocalInput(raw: string, utc: boolean): string {
  const d = utc ? new Date(`${raw}:00Z`) : new Date(`${raw}:00`);
  return Number.isNaN(d.getTime()) ? raw : d.toISOString();
}

function CollectionControl({ prop, value, onChange, root, errors, path }: ControlProps) {
  const env = useFormEnv();
  const current = (value && typeof value === 'object' && !Array.isArray(value) ? value : {}) as Values;
  const children = prop.children ?? [];
  const present = children.filter((c) => c.name in current);
  const absent = children.filter((c) => !(c.name in current));
  return (
    <div className="nested">
      {present.map((c) => (
        <div key={c.name} className="nested-row">
          <div style={{ flex: 1 }}>
            <PropertyForm
              properties={[c]}
              values={current}
              root={root}
              errors={errors}
              prefix={`${path}.`}
              onChange={(next) => onChange(next)}
            />
          </div>
          {!env.readOnly && (
            <button type="button" className="btn ghost icon sm" aria-label={t('form.removeField', { name: c.displayName ?? c.name })} onClick={() => {
              const next = { ...current };
              delete next[c.name];
              onChange(next);
            }}>
              <Icon name="close" />
            </button>
          )}
        </div>
      ))}
      {!env.readOnly && absent.length > 0 && (
        <select
          aria-label={t('form.addField')}
          value=""
          onChange={(e) => {
            const c = children.find((x) => x.name === e.target.value);
            if (c) onChange({ ...current, [c.name]: c.default ?? (c.type === 'boolean' ? false : c.type === 'number' ? 0 : '') });
          }}
        >
          <option value="">{t('form.addField')}</option>
          {absent.map((c) => <option key={c.name} value={c.name}>{c.displayName ?? c.name}</option>)}
        </select>
      )}
    </div>
  );
}

function FixedCollectionControl({ prop, value, onChange, root, errors, path }: ControlProps) {
  const env = useFormEnv();
  const rows = (Array.isArray(value) ? value : []) as Values[];
  const children = prop.children ?? [];
  const blank = (): Values => {
    const r: Values = {};
    for (const c of children) if (c.default !== undefined) r[c.name] = c.default;
    return r;
  };
  const move = (i: number, d: number) => {
    const next = [...rows];
    const j = i + d;
    if (j < 0 || j >= next.length) return;
    [next[i], next[j]] = [next[j] as Values, next[i] as Values];
    onChange(next);
  };
  return (
    <div className="nested">
      {rows.map((row, i) => (
        <div key={i} className="nested-row" role="group" aria-label={t('form.row', { n: i + 1 })}>
          <div style={{ flex: 1 }}>
            <PropertyForm
              properties={children}
              values={row}
              root={root}
              errors={errors}
              prefix={`${path}.${i}.`}
              onChange={(next) => onChange(rows.map((r, k) => (k === i ? next : r)))}
            />
          </div>
          {!env.readOnly && (
            <div className="row-actions" style={{ flexDirection: 'column' }}>
              <button type="button" className="btn ghost icon sm" aria-label={t('form.moveUp')} disabled={i === 0} onClick={() => move(i, -1)}>↑</button>
              <button type="button" className="btn ghost icon sm" aria-label={t('form.moveDown')} disabled={i === rows.length - 1} onClick={() => move(i, 1)}>↓</button>
              <button type="button" className="btn ghost icon sm" aria-label={t('form.removeRow', { n: i + 1 })} onClick={() => onChange(rows.filter((_, k) => k !== i))}>
                <Icon name="trash" />
              </button>
            </div>
          )}
        </div>
      ))}
      {!env.readOnly && (
        <button type="button" className="btn sm" onClick={() => onChange([...rows, blank()])}>
          <Icon name="plus" /> {t('form.addRow')}
        </button>
      )}
    </div>
  );
}

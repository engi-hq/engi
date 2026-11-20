import { useMemo, useState } from 'react';
import { useCredentialMutations, useCredentialTypes, fetchCredential } from '../api/hooks';
import type { Credential } from '../api/types';
import { emptyEnv, FormEnvContext } from '../forms/env';
import { defaultParameters, validate, type Values } from '../forms/logic';
import { PropertyForm } from '../forms/PropertyForm';
import { t } from '../i18n/t';
import { Dialog } from '../ui/Dialog';
import { describeError, useToast } from '../ui/Toast';
import { useEffect } from 'react';

interface Props {
  /** Edit an existing credential, or create one (optionally of a preset type). */
  id?: string;
  presetType?: string;
  onClose: () => void;
  onSaved?: (c: Credential) => void;
}

const formEnv = { ...emptyEnv, expressions: false };

export function CredentialDialog({ id, presetType, onClose, onSaved }: Props) {
  const types = useCredentialTypes();
  const m = useCredentialMutations();
  const toast = useToast();
  const [loaded, setLoaded] = useState<Credential | null>(null);
  const [name, setName] = useState('');
  const [type, setType] = useState(presetType ?? '');
  const [data, setData] = useState<Values>({});
  const [error, setError] = useState('');
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [testMsg, setTestMsg] = useState<{ ok: boolean; text: string } | null>(null);

  useEffect(() => {
    if (!id) return;
    void fetchCredential(id).then((c) => {
      setLoaded(c);
      setName(c.name);
      setType(c.type);
      setData(c.data as Values);
    }).catch((e) => setError(describeError(e)));
  }, [id]);

  const ct = useMemo(() => types.data?.find((x) => x.name === type), [types.data, type]);

  const pickType = (name: string) => {
    setType(name);
    const found = types.data?.find((x) => x.name === name);
    setData(found ? defaultParameters(found.fields) : {});
  };

  const save = () => {
    if (!ct) return;
    const errs = validate(ct.fields, data);
    setErrors(errs);
    if (!name.trim() || Object.keys(errs).length > 0) {
      setError(t('cred.fixErrors'));
      return;
    }
    setError('');
    const done = (c: Credential) => { toast.info(t('cred.saved')); onSaved?.(c); onClose(); };
    if (id) m.update.mutate({ id, name: name.trim(), data }, { onSuccess: done, onError: (e) => setError(describeError(e)) });
    else m.create.mutate({ name: name.trim(), type, data }, { onSuccess: done, onError: (e) => setError(describeError(e)) });
  };

  const test = () => {
    if (!id) return;
    m.test.mutate(id, {
      onSuccess: (r) => setTestMsg({ ok: r.success, text: r.message ?? (r.success ? t('cred.testOk') : t('cred.testFailed')) }),
      onError: (e) => setTestMsg({ ok: false, text: describeError(e) }),
    });
  };

  const authorize = () => {
    if (!id) return;
    m.authorize.mutate(id, {
      onSuccess: (r) => { window.location.href = r.url; },
      onError: (e) => setError(describeError(e)),
    });
  };

  return (
    <Dialog title={id ? t('cred.edit') : t('cred.create')} onClose={onClose}>
      {(id && !loaded && !error) || types.isPending ? <span className="spinner" /> : (
        <FormEnvContext.Provider value={formEnv}>
          <label className="field">
            <span className="field-label">{t('cred.type')}</span>
            <select value={type} disabled={!!id} onChange={(e) => pickType(e.target.value)} data-autofocus={!id}>
              <option value="">{t('form.choose')}</option>
              {types.data?.map((x) => <option key={x.name} value={x.name}>{x.displayName}</option>)}
            </select>
          </label>
          <label className="field">
            <span className="field-label">{t('common.name')}</span>
            <input type="text" value={name} onChange={(e) => setName(e.target.value)} />
          </label>
          {ct && <PropertyForm properties={ct.fields} values={data} onChange={setData} errors={errors} />}
          {loaded?.needsReauth && <div className="callout warning">{t('cred.needsReauth')}</div>}
          {testMsg && <div className={`callout${testMsg.ok ? '' : ' error'}`} role="status">{testMsg.text}</div>}
          {error && <div className="callout error" role="alert">{error}</div>}
          <div className="dialog-actions">
            {id && ct?.oauth2 && <button className="btn" onClick={authorize}>{t('cred.authorize')}</button>}
            {id && ct?.testable && <button className="btn" onClick={test} disabled={m.test.isPending}>{t('cred.test')}</button>}
            <span className="spacer" />
            <button className="btn" onClick={onClose}>{t('common.cancel')}</button>
            <button className="btn primary" onClick={save} disabled={!ct || m.create.isPending || m.update.isPending}>{t('common.save')}</button>
          </div>
        </FormEnvContext.Provider>
      )}
    </Dialog>
  );
}

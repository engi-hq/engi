import { useState, type FormEvent } from 'react';
import { useTagMutations, useTags, type Tag } from '../api/accountHooks';
import { t } from '../i18n/t';
import { ConfirmDialog, Dialog } from '../ui/Dialog';
import { Icon } from '../ui/Icon';
import { describeError, useToast } from '../ui/Toast';

export function TagsPage() {
  const list = useTags();
  const m = useTagMutations();
  const toast = useToast();
  const [renaming, setRenaming] = useState<Tag | null>(null);
  const [deleting, setDeleting] = useState<Tag | null>(null);

  return (
    <>
      <div className="page-head"><h2>{t('tags.title')}</h2></div>
      <div className="card">
        {list.isPending ? <div className="empty"><span className="spinner" /></div>
          : list.isError ? <div className="empty" role="alert">{describeError(list.error)}</div>
          : list.data.length === 0 ? <div className="empty">{t('tags.empty')}</div> : (
            <table className="list">
              <thead><tr><th>{t('common.name')}</th><th><span className="sr-only">{t('common.actions')}</span></th></tr></thead>
              <tbody>
                {list.data.map((tag) => (
                  <tr key={tag.id}>
                    <td><strong>{tag.name}</strong></td>
                    <td>
                      <div className="row-actions">
                        <button className="btn sm" aria-label={t('tags.renameOne', { name: tag.name })} onClick={() => setRenaming(tag)}>{t('tags.rename')}</button>
                        <button className="btn ghost icon sm" aria-label={t('tags.deleteOne', { name: tag.name })} onClick={() => setDeleting(tag)}><Icon name="trash" /></button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
      </div>
      {renaming && <RenameDialog tag={renaming} onClose={() => setRenaming(null)} />}
      {deleting && (
        <ConfirmDialog
          title={t('tags.deleteTitle')}
          message={t('tags.deleteMessage', { name: deleting.name })}
          confirmLabel={t('common.delete')}
          danger
          onClose={() => setDeleting(null)}
          onConfirm={() => m.remove.mutate(deleting.id, { onError: toast.error })}
        />
      )}
    </>
  );
}

function RenameDialog({ tag, onClose }: { tag: Tag; onClose: () => void }) {
  const m = useTagMutations();
  const [name, setName] = useState(tag.name);
  const [error, setError] = useState<unknown>(null);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    m.rename.mutate({ id: tag.id, name: name.trim() }, { onSuccess: onClose, onError: setError });
  };
  return (
    <Dialog title={t('tags.renameTitle')} onClose={onClose}>
      <form onSubmit={submit} style={{ display: 'grid', gap: 12 }}>
        <label className="field">
          <span className="field-label">{t('common.name')}</span>
          <input value={name} onChange={(e) => setName(e.target.value)} required data-autofocus />
          <span className="field-hint">{t('tags.renameHint')}</span>
        </label>
        {!!error && <div className="callout error" role="alert">{describeError(error)}</div>}
        <div className="dialog-actions">
          <button type="button" className="btn" onClick={onClose}>{t('common.cancel')}</button>
          <button type="submit" className="btn primary" disabled={m.rename.isPending || !name.trim()}>{t('common.save')}</button>
        </div>
      </form>
    </Dialog>
  );
}

import { ReactFlowProvider, useReactFlow } from '@xyflow/react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link, useBlocker, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { useVariables } from '../api/accountHooks';
import { useEditorSettings } from '../auth/AuthContext';
import { api, ApiError } from '../api/client';
import {
  exportWorkflow, fetchExecution, fetchWorkflow, fetchWorkflowVersion, saveWorkflow, useCredentials, useNodeTypes, useWorkflow, useWorkflowMutations,
  type LoadedWorkflow,
} from '../api/hooks';
import type { Execution, NodeType, WorkflowDoc } from '../api/types';
import { CredentialDialog } from '../credentials/CredentialDialog';
import { TagEditor } from '../editor/TagEditor';
import { Canvas, type Apply } from '../editor/Canvas';
import { CommandPalette, ShortcutHelp, type Command } from '../editor/Commands';
import { ConflictDialog, diffDocs, type DocDiff } from '../editor/ConflictDialog';
import { LAYOUT_CONFIG, autoLayout } from '../editor/layout';
import { commit, initHistory, redo, reset, undo } from '../editor/history';
import { HistoryPanel, VersionBanner } from '../editor/VersionHistory';
import { ListenDialog } from '../editor/ListenDialog';
import { WorkflowSettingsDialog } from '../editor/WorkflowSettings';
import {
  addConnection, addNode, checkConnection, copyFragment, createNode, descriptorFor, indexTypes, firstFreeOutput, freePosition, nextPosition, parseFragment,
  pasteFragment, removeNodes, renameNode, setPin, updateNode, type ConnectionProblem,
} from '../editor/model';
import { NodePanel } from '../editor/NodePanel';
import { addNote, createNote, notesOf, removeNotes } from '../editor/notes';
import { Timeline } from '../editor/Timeline';
import { Palette } from '../editor/Palette';
import { rememberNode } from '../editor/paletteSearch';
import { statesFromExecution } from '../editor/runState';
import { useRunner } from '../editor/useRunner';
import { isEditable, useShortcuts } from '../editor/useShortcuts';
import { t, tn } from '../i18n/t';
import { ConfirmDialog } from '../ui/Dialog';
import { Icon } from '../ui/Icon';
import { Switch } from '../ui/Switch';
import { describeError, useToast } from '../ui/Toast';
import { downloadText, safeFilename } from '../util/download';

export function EditorPage() {
  const { id } = useParams();
  const wf = useWorkflow(id);
  const types = useNodeTypes();
  if (wf.isPending || types.isPending) return <main className="page"><span className="spinner" aria-label={t('common.loading')} /></main>;
  if (wf.isError || types.isError) {
    return <main className="page"><div className="callout error" role="alert">{describeError(wf.error ?? types.error)}</div><Link to="/workflows">{t('editor.back')}</Link></main>;
  }
  return (
    <ReactFlowProvider>
      <Editor key={`${id}`} loaded={wf.data} nodeTypes={types.data} />
    </ReactFlowProvider>
  );
}

const WEBHOOK_SUFFIX = '.webhook';

function Editor({ loaded, nodeTypes }: { loaded: LoadedWorkflow; nodeTypes: NodeType[] }) {
  const toast = useToast();
  const nav = useNavigate();
  const qc = useQueryClient();
  const rf = useReactFlow();
  const [params, setParams] = useSearchParams();
  const mut = useWorkflowMutations();
  const credentials = useCredentials();
  const variables = useVariables();
  const editorSettings = useEditorSettings();
  const idx = useMemo(() => indexTypes(nodeTypes), [nodeTypes]);

  const [hist, setHist] = useState(() => initHistory(loaded.doc));
  const [saved, setSaved] = useState<WorkflowDoc>(loaded.doc);
  const [saving, setSaving] = useState(false);
  const [selected, setSelected] = useState<string[]>([]);
  const [selectedNotes, setSelectedNotes] = useState<string[]>([]);
  const [open, setOpen] = useState<string | null>(null);
  const [timelineOpen, setTimelineOpen] = useState(false);
  const [dialog, setDialog] = useState<null | 'palette' | 'commands' | 'help' | 'listen' | 'delete' | 'settings'>(null);
  const [conflict, setConflict] = useState<{ diff: DocDiff; latest: LoadedWorkflow } | null>(null);
  const [credDialog, setCredDialog] = useState<{ type: string; slot: string; node: string } | null>(null);
  const [connectFrom, setConnectFrom] = useState<string | null>(null);
  const [notice, setNotice] = useState('');
  const [historyOpen, setHistoryOpen] = useState(false);
  const [viewVersion, setViewVersion] = useState<number | null>(null);

  const doc = hist.present;
  const dirty = doc !== saved;
  const wfId = doc.id as string;

  const apply: Apply = useCallback((fn, key) => setHist((h) => commit(h, fn(h.present), key)), []);

  // ---- Past execution (read-only) ----
  const execParam = params.get('execution');
  const pastExec = useQuery({ queryKey: ['execution', execParam], enabled: !!execParam, queryFn: () => fetchExecution(execParam as string) });
  const pastDoc = useQuery({
    queryKey: ['wf-version', wfId, pastExec.data?.workflowVersion],
    enabled: !!pastExec.data,
    queryFn: () => fetchWorkflowVersion(wfId, (pastExec.data as Execution).workflowVersion).catch(() => null),
  });
  const viewing = !!execParam;

  // ---- Historical version (read-only) ----
  const versionQ = useQuery({
    queryKey: ['wf-version', wfId, viewVersion, 'view'],
    enabled: viewVersion !== null,
    queryFn: () => fetchWorkflowVersion(wfId, viewVersion as number),
  });
  const viewingVersion = viewVersion !== null;

  // ---- Running ----
  const runner = useRunner(wfId, () => qc.invalidateQueries({ queryKey: ['executions'] }));
  const execution = viewing ? pastExec.data : runner.state.execution;
  const states = useMemo(() => (viewing ? statesFromExecution(pastExec.data) : runner.state.states), [viewing, pastExec.data, runner.state.states]);
  const shownDoc = viewingVersion && versionQ.data ? versionQ.data : viewing && pastDoc.data ? pastDoc.data : doc;
  const readOnly = viewing || viewingVersion;
  const running = runner.state.phase === 'starting' || runner.state.phase === 'running';

  const save = useCallback(async (override?: WorkflowDoc): Promise<boolean> => {
    const target = override ?? hist.present;
    setSaving(true);
    try {
      const stored = await saveWorkflow({ ...target, version: saved.version });
      setSaved(stored.doc);
      setHist((h) => reset(h, stored.doc));
      void qc.invalidateQueries({ queryKey: ['workflows'] });
      toast.info(t('editor.saved', { v: stored.version }));
      return true;
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        const latest = await fetchWorkflow(wfId);
        setConflict({ diff: diffDocs(target, latest.doc), latest });
      } else {
        toast.error(e);
      }
      return false;
    } finally {
      setSaving(false);
    }
  }, [hist.present, saved.version, qc, toast, wfId]);

  const runNow = useCallback(async () => {
    if (readOnly || running) return;
    if (dirty && !(await save())) return;
    void runner.run({ pinData: hist.present.pinData });
  }, [readOnly, running, dirty, save, runner, hist.present.pinData]);

  /** Partial run up to a node, using the current unsaved document so saving first is not needed. */
  const runTo = useCallback((node: string, mode: 'inclusive' | 'exclusive') => {
    if (readOnly || running) return;
    void runner.run({ destination: { node, mode }, workflow: hist.present, pinData: hist.present.pinData });
  }, [readOnly, running, runner, hist.present]);

  // ---- Unsaved changes guard ----
  const blocker = useBlocker(({ currentLocation, nextLocation }) => dirty && currentLocation.pathname !== nextLocation.pathname);
  useEffect(() => {
    if (!dirty) return;
    const h = (e: BeforeUnloadEvent) => { e.preventDefault(); };
    window.addEventListener('beforeunload', h);
    return () => window.removeEventListener('beforeunload', h);
  }, [dirty]);

  // ---- Editing helpers ----
  const refused = useCallback((p: ConnectionProblem) => setNotice(t(`conn.${p}`)), []);
  const selectedNames = selected.filter((n) => doc.nodes.some((x) => x.name === n));
  const single = selectedNames.length === 1 && selectedNotes.length === 0 ? selectedNames[0] : undefined;
  const selectedNoteIds = selectedNotes.filter((id) => notesOf(doc).some((n) => n.id === id));
  const hasSelection = selectedNames.length + selectedNoteIds.length > 0;

  const tidy = useCallback(() => {
    if (readOnly) return;
    apply((d) => autoLayout(d, selectedNames));
    // Fit after React Flow has received the new positions.
    window.setTimeout(() => void rf.fitView({ padding: LAYOUT_CONFIG.fitPadding, maxZoom: LAYOUT_CONFIG.fitMaxZoom, duration: 200 }), 50);
  }, [readOnly, apply, selectedNames, rf]);

  const addFromType = useCallback((nt: NodeType) => {
    const centre = rf.screenToFlowPosition({ x: window.innerWidth / 2 - 95, y: window.innerHeight / 2 - 30 });
    let created = '';
    apply((d) => {
      // Follow the selected node, or the last node when nothing is selected.
      const anchor = single ?? (selected.length === 0 ? d.nodes[d.nodes.length - 1]?.name : undefined);
      let pos = anchor ? nextPosition(d, anchor) : ([Math.round(centre.x), Math.round(centre.y)] as [number, number]);
      pos = freePosition(d, pos);
      const node = createNode(nt, d, pos);
      created = node.name;
      let next = addNode(d, node);
      if (anchor && nt.inputs.length > 0) {
        const fromIndex = firstFreeOutput(next, idx, anchor);
        const e = { fromNode: anchor, fromIndex, toNode: node.name, toIndex: 0 };
        if (fromIndex >= 0 && checkConnection(next, idx, e) === null) next = addConnection(next, idx, e);
      }
      return next;
    });
    rememberNode(nt.type);
    setDialog(null);
    queueMicrotask(() => { setSelected([created]); setOpen(created); });
  }, [apply, idx, rf, selected.length, single]);

  const addStickyNote = useCallback(() => {
    if (readOnly) return;
    const centre = rf.screenToFlowPosition({ x: window.innerWidth / 2 - 120, y: window.innerHeight / 2 - 80 });
    let created = '';
    apply((d) => {
      const note = createNote(d, [Math.round(centre.x), Math.round(centre.y)]);
      created = note.id;
      return addNote(d, note);
    });
    queueMicrotask(() => { setSelected([]); setSelectedNotes([created]); });
  }, [apply, readOnly, rf]);

  const deleteSelected = useCallback(() => {
    if (readOnly || !hasSelection) return;
    apply((d) => removeNotes(removeNodes(d, selectedNames), selectedNoteIds));
    setSelected([]);
    setSelectedNotes([]);
    setOpen(null);
  }, [apply, readOnly, hasSelection, selectedNames, selectedNoteIds]);

  const toggleDisabled = useCallback(() => {
    if (readOnly || selectedNames.length === 0) return;
    apply((d) => {
      const allOff = selectedNames.every((n) => d.nodes.find((x) => x.name === n)?.disabled);
      return selectedNames.reduce((acc, n) => updateNode(acc, n, { disabled: !allOff }), d);
    });
  }, [apply, readOnly, selectedNames]);

  const duplicate = useCallback(() => {
    if (readOnly || !hasSelection) return;
    let names: string[] = [];
    let noteIds: string[] = [];
    apply((d) => { const r = pasteFragment(d, copyFragment(d, selectedNames, selectedNoteIds)); names = r.names; noteIds = r.noteIds; return r.doc; });
    queueMicrotask(() => { setSelected(names); setSelectedNotes(noteIds); });
  }, [apply, readOnly, hasSelection, selectedNames, selectedNoteIds]);

  // Copy / paste as workflow JSON through the native clipboard events.
  useEffect(() => {
    const onCopy = (e: ClipboardEvent) => {
      if (isEditable(e.target) || !hasSelection) return;
      e.clipboardData?.setData('text/plain', JSON.stringify(copyFragment(doc, selectedNames, selectedNoteIds), null, 2));
      e.preventDefault();
      toast.info(tn('editor.copied', selectedNames.length + selectedNoteIds.length));
    };
    const onPaste = (e: ClipboardEvent) => {
      if (isEditable(e.target) || readOnly) return;
      const frag = parseFragment(e.clipboardData?.getData('text/plain') ?? '');
      if (!frag) return;
      e.preventDefault();
      let names: string[] = [];
      let noteIds: string[] = [];
      apply((d) => { const r = pasteFragment(d, frag); names = r.names; noteIds = r.noteIds; return r.doc; });
      queueMicrotask(() => { setSelected(names); setSelectedNotes(noteIds); });
    };
    document.addEventListener('copy', onCopy);
    document.addEventListener('paste', onPaste);
    return () => { document.removeEventListener('copy', onCopy); document.removeEventListener('paste', onPaste); };
  }, [doc, selectedNames, selectedNoteIds, hasSelection, apply, readOnly, toast]);

  const doExport = useCallback(async () => {
    try {
      const d = dirty ? doc : await exportWorkflow(wfId);
      downloadText(`${safeFilename(doc.name)}.json`, JSON.stringify(d, null, 2));
    } catch (e) { toast.error(e); }
  }, [dirty, doc, wfId, toast]);

  const setActive = (active: boolean) =>
    mut.setActive.mutate({ id: wfId, active }, {
      onSuccess: () => { setSaved((s) => ({ ...s, active })); setHist((h) => reset(h, { ...h.present, active })); toast.info(t(active ? 'workflows.activated' : 'workflows.deactivated', { name: doc.name })); },
      onError: toast.error,
    });

  const completeConnect = useCallback(() => {
    if (!connectFrom || !single || single === connectFrom) return;
    const fromIndex = firstFreeOutput(doc, idx, connectFrom);
    const e = { fromNode: connectFrom, fromIndex: Math.max(0, fromIndex), toNode: single, toIndex: 0 };
    const problem = checkConnection(doc, idx, e);
    if (problem) setNotice(t(`conn.${problem}`));
    else apply((d) => addConnection(d, idx, e));
    setConnectFrom(null);
  }, [connectFrom, single, doc, idx, apply]);

  const hasWebhook = doc.nodes.some((n) => n.type.endsWith(WEBHOOK_SUFFIX));
  const webhookNode = doc.nodes.filter((n) => n.type.endsWith(WEBHOOK_SUFFIX))[0]?.name;

  const commands: Command[] = [
    { id: 'add', label: t('cmd.addNode'), shortcut: 'Tab', run: () => setDialog('palette'), enabled: !readOnly },
    { id: 'note', label: t('cmd.addNote'), shortcut: 'N', run: addStickyNote, enabled: !readOnly },
    { id: 'save', label: t('cmd.save'), shortcut: 'Ctrl/Cmd+S', run: () => void save(), enabled: !readOnly && dirty },
    { id: 'run', label: t('cmd.run'), shortcut: 'Ctrl/Cmd+Enter', run: () => void runNow(), enabled: !readOnly },
    { id: 'listen', label: t('cmd.listen'), run: () => setDialog('listen'), enabled: hasWebhook && !readOnly },
    { id: 'undo', label: t('cmd.undo'), shortcut: 'Ctrl/Cmd+Z', run: () => setHist(undo), enabled: !readOnly && hist.past.length > 0 },
    { id: 'redo', label: t('cmd.redo'), shortcut: 'Ctrl/Cmd+Shift+Z', run: () => setHist(redo), enabled: !readOnly && hist.future.length > 0 },
    { id: 'disable', label: t('cmd.disable'), shortcut: 'D', run: toggleDisabled, enabled: !readOnly && selectedNames.length > 0 },
    { id: 'duplicate', label: t('cmd.duplicate'), shortcut: 'Ctrl/Cmd+D', run: duplicate, enabled: !readOnly && hasSelection },
    { id: 'delete', label: t('cmd.delete'), shortcut: 'Delete', run: deleteSelected, enabled: !readOnly && hasSelection },
    { id: 'connect', label: t('cmd.connect'), shortcut: 'C', run: () => single && setConnectFrom(single), enabled: !readOnly && !!single },
    { id: 'tidy', label: t('cmd.tidy'), shortcut: 'Ctrl/Cmd+Shift+L', run: tidy, enabled: !readOnly },
    { id: 'fit', label: t('cmd.fit'), run: () => void rf.fitView({ padding: 0.3, maxZoom: 1 }) },
    { id: 'export', label: t('cmd.export'), run: () => void doExport() },
    { id: 'executions', label: t('cmd.executions'), run: () => nav(`/executions?workflowId=${wfId}`) },
    { id: 'settings', label: t('cmd.workflowSettings'), run: () => setDialog('settings') },
    { id: 'history', label: t('cmd.history'), shortcut: 'H', run: () => setHistoryOpen((o) => !o) },
    { id: 'help', label: t('cmd.help'), shortcut: '?', run: () => setDialog('help') },
  ];

  useShortcuts([
    { combo: 'tab', run: () => !readOnly && setDialog('palette') },
    { combo: '+', run: () => !readOnly && setDialog('palette') },
    { combo: 'mod+k', run: () => setDialog('commands') },
    { combo: 'mod+s', run: () => { if (!readOnly && dirty) void save(); } },
    { combo: 'mod+enter', run: () => void runNow() },
    { combo: 'mod+z', run: () => !readOnly && setHist(undo) },
    { combo: 'mod+shift+z', run: () => !readOnly && setHist(redo) },
    { combo: 'mod+d', run: duplicate },
    { combo: 'mod+shift+l', run: tidy },
    { combo: 'd', run: toggleDisabled },
    { combo: 'n', run: addStickyNote },
    { combo: 'c', run: () => { if (single && !readOnly) setConnectFrom(single); } },
    { combo: 'enter', run: () => { if (connectFrom) completeConnect(); else if (single) setOpen(single); } },
    { combo: 'escape', run: () => { setConnectFrom(null); setNotice(''); } },
    { combo: 'h', run: () => setHistoryOpen((o) => !o) },
    { combo: '?', run: () => setDialog('help') },
    { combo: 'shift+?', run: () => setDialog('help') },
  ]);

  const openNode = open ? shownDoc.nodes.find((n) => n.name === open) : undefined;
  const previewTarget = useMemo(
    () => (openNode ? { node: openNode.name, workflowId: wfId, executionId: execution?.id, workflow: doc } : undefined),
    [openNode?.name, wfId, execution?.id, doc], // eslint-disable-line react-hooks/exhaustive-deps
  );
  const credNodeSlots = credDialog;

  const viewedStatus = viewing && pastExec.data ? pastExec.data : undefined;

  return (
    <div className="editor">
      <header className="editor-bar">
        <Link to="/workflows" className="btn ghost icon" aria-label={t('editor.back')} title={t('editor.back')}><Icon name="arrow-left" /></Link>
        <input
          className="wf-name"
          type="text"
          value={doc.name}
          disabled={readOnly}
          aria-label={t('editor.workflowName')}
          onChange={(e) => apply((d) => ({ ...d, name: e.target.value }), 'name')}
        />
        <TagEditor tags={doc.tags ?? []} readOnly={readOnly} onChange={(tags) => apply((d) => ({ ...d, tags }), 'tags')} />
        {dirty && <span className="badge waiting" title={t('editor.unsaved')}>{t('editor.unsaved')}</span>}
        <span className="spacer" />
        {!readOnly && (
          <>
            <button className="btn icon" onClick={() => setHist(undo)} disabled={hist.past.length === 0} aria-label={t('cmd.undo')} title={`${t('cmd.undo')} (Ctrl/Cmd+Z)`}><Icon name="undo" /></button>
            <button className="btn icon" onClick={() => setHist(redo)} disabled={hist.future.length === 0} aria-label={t('cmd.redo')} title={`${t('cmd.redo')} (Ctrl/Cmd+Shift+Z)`}><Icon name="redo" /></button>
            <button className="btn icon" onClick={tidy} aria-label={t('cmd.tidy')} title={`${t('cmd.tidy')} (Ctrl/Cmd+Shift+L)`}><Icon name="list" /></button>
            <button className="btn" onClick={() => setDialog('palette')}><Icon name="plus" /> {t('cmd.addNode')}</button>
            <button className="btn icon" onClick={addStickyNote} aria-label={t('note.add')} title={`${t('note.add')} (N)`}><Icon name="note" /></button>
            {hasWebhook && <button className="btn" onClick={() => (dirty ? void save().then((ok) => ok && setDialog('listen')) : setDialog('listen'))}><Icon name="webhook" /> {t('cmd.listen')}</button>}
            {running ? (
              <button className="btn danger" onClick={() => runner.state.executionId && void api.post(`/executions/${runner.state.executionId}/cancel`).catch(toast.error)}>
                <Icon name="stop" /> {t('run.cancel')}
              </button>
            ) : (
              <button className="btn primary" onClick={() => void runNow()}><Icon name="play" /> {t('run.run')}</button>
            )}
            <button className="btn" onClick={() => void save()} disabled={!dirty || saving}><Icon name="save" /> {t('common.save')}</button>
            <Switch checked={!!doc.active} onChange={setActive} label={t('workflows.active')} disabled={dirty || mut.setActive.isPending} />
          </>
        )}
        <button className="btn ghost icon" onClick={() => setDialog('settings')} aria-label={t('wfsettings.title')} title={t('wfsettings.title')}><Icon name="settings" /></button>
        {execution && (
          <button className="btn ghost" onClick={() => setTimelineOpen((o) => !o)} aria-pressed={timelineOpen}>
            <Icon name="clock" /> {t('timeline.title')}
          </button>
        )}
        <button className="btn ghost icon" onClick={() => setHistoryOpen((o) => !o)} aria-pressed={historyOpen} aria-label={t('history.title')} title={`${t('history.title')} (H)`}><Icon name="clock" /></button>
        <button className="btn ghost icon" onClick={() => setDialog('commands')} aria-label={t('commands.title')} title="Ctrl/Cmd+K"><Icon name="list" /></button>
      </header>

      {viewing && (
        <div className="banner" role="status">
          <Icon name="eye" />
          {pastExec.isPending ? t('common.loading') : pastExec.isError ? describeError(pastExec.error) : (
            <>
              <span>{t('editor.viewingExecution', { id: viewedStatus?.id ?? '' })}</span>
              <span className={`badge ${viewedStatus?.status}`}>{viewedStatus?.status}</span>
              {pastDoc.data === null && <span className="field-hint">{t('editor.versionGone')}</span>}
            </>
          )}
          <span className="spacer" />
          <button className="btn sm" onClick={() => { setParams({}, { replace: true }); setOpen(null); }}>{t('editor.backToEditing')}</button>
        </div>
      )}
      {viewingVersion && (
        <>
          <VersionBanner
            workflowId={wfId}
            version={viewVersion}
            dirty={dirty}
            onBack={() => { setViewVersion(null); setOpen(null); }}
            onRestored={(loaded) => {
              setSaved(loaded.doc);
              setHist(initHistory(loaded.doc));
              setViewVersion(null);
              setOpen(null);
              void qc.invalidateQueries({ queryKey: ['wf-versions', wfId] });
              void qc.invalidateQueries({ queryKey: ['workflows'] });
              toast.info(t('history.restored', { v: viewVersion, n: loaded.version }));
            }}
          />
          {versionQ.isError && <div className="banner warn" role="alert"><Icon name="alert" /> {describeError(versionQ.error)}</div>}
        </>
      )}
      {connectFrom && (
        <div className="banner" role="status">
          <Icon name="link" /> {t('editor.connecting', { name: connectFrom })}
          <span className="spacer" />
          <button className="btn sm" onClick={() => setConnectFrom(null)}>{t('common.cancel')}</button>
        </div>
      )}
      {notice && (
        <div className="banner warn" role="alert">
          <Icon name="alert" /> {notice}
          <span className="spacer" />
          <button className="btn sm" onClick={() => setNotice('')}>{t('common.close')}</button>
        </div>
      )}

      <div className="editor-body">
        <div className="canvas-wrap">
          <Canvas
            doc={shownDoc}
            types={idx}
            states={states}
            apply={apply}
            readOnly={readOnly}
            selected={selected}
            selectedNotes={selectedNotes}
            noteTextLimit={editorSettings.maxNoteTextBytes}
            connectFrom={connectFrom}
            onSelect={setSelected}
            onSelectNotes={setSelectedNotes}
            onOpen={setOpen}
            onRefused={refused}
          />
          {execution && timelineOpen && (
            <Timeline execution={execution} onJump={(n) => { setSelected([n]); setOpen(n); }} onClose={() => setTimelineOpen(false)} />
          )}
          <RunBar
            phase={runner.state.phase}
            execution={viewing ? undefined : runner.state.execution}
            error={runner.state.error}
            onJump={(n) => { setSelected([n]); setOpen(n); }}
          />
        </div>
        {historyOpen && (
          <HistoryPanel
            workflowId={wfId}
            selected={viewVersion}
            current={saved.version ?? 0}
            onSelect={(v) => { setViewVersion(v); setOpen(null); if (execParam) setParams({}, { replace: true }); }}
            onClose={() => setHistoryOpen(false)}
          />
        )}
        {openNode && (
          <NodePanel
            key={openNode.name}
            node={openNode}
            nt={descriptorFor(idx, openNode)}
            nodeNames={shownDoc.nodes.map((n) => n.name)}
            varKeys={variables.data?.map((v) => v.key)}
            credentials={credentials.data ?? []}
            execution={execution}
            readOnly={readOnly}
            pinned={shownDoc.pinData?.[openNode.name] as object[] | undefined}
            onPin={(items) => apply((d) => setPin(d, openNode.name, items))}
            running={running}
            onRunTo={(mode) => runTo(openNode.name, mode)}
            serverPreview={previewTarget}
            onPatch={(patch, key) => apply((d) => updateNode(d, openNode.name, patch), key)}
            onRename={(to) => {
              const r = renameNode(doc, openNode.name, to);
              if (!r.ok) return r;
              apply(() => r.doc);
              setOpen(to.trim());
              setSelected([to.trim()]);
              return r;
            }}
            onDelete={() => setDialog('delete')}
            onClose={() => setOpen(null)}
            onCreateCredential={(type, slot) => setCredDialog({ type, slot, node: openNode.name })}
          />
        )}
      </div>

      {dialog === 'palette' && <Palette types={nodeTypes} onPick={addFromType} onClose={() => setDialog(null)} />}
      {dialog === 'commands' && <CommandPalette commands={commands} onClose={() => setDialog(null)} />}
      {dialog === 'help' && <ShortcutHelp onClose={() => setDialog(null)} />}
      {dialog === 'settings' && (
        <WorkflowSettingsDialog
          workflowId={wfId}
          nodes={doc.nodes}
          settings={doc.settings}
          readOnly={readOnly}
          onApply={(settings) => apply((d) => ({ ...d, settings }))}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog === 'listen' && (
        <ListenDialog
          workflowId={wfId}
          node={webhookNode}
          onClose={() => setDialog(null)}
          onExecution={(eid) => { setDialog(null); runner.attach(eid); }}
        />
      )}
      {dialog === 'delete' && openNode && (
        <ConfirmDialog
          title={t('node.deleteTitle')}
          message={t('node.deleteMessage', { name: openNode.name })}
          confirmLabel={t('common.delete')}
          danger
          onClose={() => setDialog(null)}
          onConfirm={() => { apply((d) => removeNodes(d, [openNode.name])); setOpen(null); setSelected([]); }}
        />
      )}
      {conflict && (
        <ConflictDialog
          diff={conflict.diff}
          latestVersion={conflict.latest.version}
          onClose={() => setConflict(null)}
          onLoadLatest={() => { setSaved(conflict.latest.doc); setHist(initHistory(conflict.latest.doc)); setConflict(null); }}
          onOverwrite={() => { const mine = { ...hist.present, version: conflict.latest.version }; setSaved(conflict.latest.doc); setConflict(null); void save({ ...mine }); }}
        />
      )}
      {blocker.state === 'blocked' && (
        <ConfirmDialog
          title={t('editor.leaveTitle')}
          message={t('editor.leaveMessage')}
          confirmLabel={t('editor.leave')}
          danger
          onClose={() => blocker.reset()}
          onConfirm={() => blocker.proceed()}
        />
      )}
      {credNodeSlots && (
        <CredentialDialog
          presetType={credNodeSlots.type}
          onClose={() => setCredDialog(null)}
          onSaved={(c) => apply((d) => {
            const n = d.nodes.find((x) => x.name === credNodeSlots.node);
            return n ? updateNode(d, n.name, { credentials: { ...(n.credentials ?? {}), [credNodeSlots.slot]: c.id } }) : d;
          })}
        />
      )}
    </div>
  );
}

function RunBar({ phase, execution, error, onJump }: { phase: string; execution?: Execution; error?: string; onJump: (node: string) => void }) {
  if (phase === 'idle' && !error) return null;
  const err = execution?.error;
  return (
    <div className="runbar" role="status" aria-live="polite">
      {phase === 'starting' && <><span className="spinner" /> {t('run.starting')}</>}
      {phase === 'running' && <><span className="spinner" /> {t('run.running')}</>}
      {phase === 'finished' && execution && (
        <>
          <span className={`badge ${execution.status}`}>{execution.status}</span>
          <span>{t('run.finished', { id: execution.id })}</span>
          {err && (
            <span className="runbar-error">
              {err.node && <button className="btn sm" onClick={() => onJump(err.node as string)}>{err.node}</button>} {err.message}
            </span>
          )}
        </>
      )}
      {error && <span className="runbar-error" role="alert">{error}</span>}
    </div>
  );
}

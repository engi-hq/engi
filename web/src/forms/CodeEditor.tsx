import { autocompletion, type CompletionContext, type CompletionResult } from '@codemirror/autocomplete';
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
import { javascript } from '@codemirror/lang-javascript';
import { json } from '@codemirror/lang-json';
import { bracketMatching } from '@codemirror/language';
import { Compartment, EditorState, type Extension } from '@codemirror/state';
import {
  Decoration, type DecorationSet, EditorView, MatchDecorator, ViewPlugin, type ViewUpdate, keymap, placeholder as cmPlaceholder,
} from '@codemirror/view';
import { useEffect, useRef } from 'react';

export type CodeLanguage = 'json' | 'js' | 'expression' | 'plain';

export interface CodeEditorProps {
  value: string;
  onChange: (v: string) => void;
  language: CodeLanguage;
  ariaLabel: string;
  placeholder?: string;
  /** Keys offered after `$json.` in expression mode. */
  jsonFields?: string[];
  nodeNames?: string[];
  /** Variable keys offered after `$vars.`. */
  varKeys?: string[];
  minHeight?: number;
}

const exprMark = Decoration.mark({ class: 'cm-expr' });
const matcher = new MatchDecorator({ regexp: /\{\{[\s\S]*?(?:\}\}|$)/g, decoration: () => exprMark });
const exprHighlight = ViewPlugin.fromClass(
  class {
    decorations: DecorationSet;
    constructor(view: EditorView) {
      this.decorations = matcher.createDeco(view);
    }
    update(u: ViewUpdate) {
      this.decorations = matcher.updateDeco(u, this.decorations);
    }
  },
  { decorations: (v) => v.decorations },
);

const VARIABLES = [
  ['$json', 'JSON of the current input item'],
  ['$binary', 'Binary metadata of the current item'],
  ['$input', 'Current input: item, all(), first(), last()'],
  ['$vars', 'Workflow variables'],
  ['$env', 'Allow-listed environment variables'],
  ['$execution', 'Execution id and mode'],
  ['$workflow', 'Workflow id and name'],
  ['$now', 'Current time'],
  ['$today', 'Midnight today (UTC)'],
  ['$itemIndex', 'Index of the current item'],
  ['$runIndex', 'Index of the current run'],
  ['$fn', 'Helper functions'],
] as const;

export function expressionCompletions(fields: string[], nodes: string[], varKeys: string[] = []) {
  return (ctx: CompletionContext): CompletionResult | null => {
    const before = ctx.state.sliceDoc(0, ctx.pos);
    const open = before.lastIndexOf('{{');
    if (open === -1 || before.lastIndexOf('}}') > open) return null;
    const dot = ctx.matchBefore(/\$json\.[\w$]*/);
    if (dot) {
      return { from: dot.from + '$json.'.length, options: fields.map((f) => ({ label: f, type: 'property' })), validFor: /^[\w$]*$/ };
    }
    const vars = ctx.matchBefore(/\$vars\.[\w$]*/);
    if (vars) {
      return { from: vars.from + '$vars.'.length, options: varKeys.map((k) => ({ label: k, type: 'variable' })), validFor: /^[\w$]*$/ };
    }
    const nodeCall = ctx.matchBefore(/\$\(["'][^"']*/);
    if (nodeCall) {
      const from = nodeCall.from + 3;
      return { from, options: nodes.map((n) => ({ label: n, type: 'text', apply: `${n}` })), validFor: /^[^"']*$/ };
    }
    const word = ctx.matchBefore(/\$[\w$]*/);
    if (!word) return null;
    return {
      from: word.from,
      options: [
        ...VARIABLES.map(([label, info]) => ({ label, info, type: 'variable' })),
        { label: '$("Node name")', apply: '$("', info: "Another node's output", type: 'function' },
      ],
      validFor: /^\$[\w$]*$/,
    };
  };
}

/** CodeMirror 6 wrapper. Loaded lazily (see LazyCode). */
export default function CodeEditor(props: CodeEditorProps) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const onChange = useRef(props.onChange);
  onChange.current = props.onChange;
  const fields = useRef<string[]>([]);
  const nodes = useRef<string[]>([]);
  const varKeys = useRef<string[]>([]);
  fields.current = props.jsonFields ?? [];
  nodes.current = props.nodeNames ?? [];
  varKeys.current = props.varKeys ?? [];
  const lang = useRef(new Compartment());

  useEffect(() => {
    if (!host.current) return;
    const exts: Extension[] = [
      history(),
      bracketMatching(),
      keymap.of([...defaultKeymap, ...historyKeymap, indentWithTab]),
      EditorView.lineWrapping,
      EditorView.contentAttributes.of({ 'aria-label': props.ariaLabel }),
      EditorView.updateListener.of((u) => {
        if (u.docChanged) onChange.current(u.state.doc.toString());
      }),
      lang.current.of(languageExtension(props.language)),
      EditorView.theme({
        '&': { fontSize: '12.5px', backgroundColor: 'var(--surface)', color: 'var(--text)' },
        '.cm-content': { fontFamily: 'var(--font-mono)', caretColor: 'var(--text)' },
        '.cm-gutters': { background: 'var(--surface-2)', color: 'var(--text-muted)', border: 'none' },
        '&.cm-focused': { outline: '2px solid var(--focus)' },
        '.cm-expr': { background: 'var(--accent-soft)', color: 'var(--accent)', borderRadius: '3px', fontWeight: '600' },
        '.cm-tooltip': { background: 'var(--surface)', color: 'var(--text)', border: '1px solid var(--border-strong)' },
        '.cm-tooltip-autocomplete ul li[aria-selected]': { background: 'var(--accent)', color: 'var(--on-accent)' },
      }),
    ];
    if (props.language === 'expression') {
      exts.push(exprHighlight, autocompletion({ override: [expressionCompletions(fields.current, nodes.current, varKeys.current)] }));
    }
    if (props.placeholder) exts.push(cmPlaceholder(props.placeholder));
    view.current = new EditorView({ parent: host.current, state: EditorState.create({ doc: props.value, extensions: exts }) });
    return () => {
      view.current?.destroy();
      view.current = null;
    };
    // The editor is created once per mount; later prop changes are synced below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [props.language]);

  useEffect(() => {
    const v = view.current;
    if (v && v.state.doc.toString() !== props.value) {
      v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: props.value } });
    }
  }, [props.value]);

  return <div ref={host} className="code-editor" style={{ minHeight: props.minHeight ?? 64 }} />;
}

function languageExtension(l: CodeLanguage): Extension {
  switch (l) {
    case 'json':
      return json();
    case 'js':
      return javascript();
    default:
      return [];
  }
}

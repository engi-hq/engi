import { lazy, Suspense } from 'react';
import type { CodeEditorProps } from './CodeEditor';

const CodeEditor = lazy(() => import('./CodeEditor'));

/** Plain textarea while the editor chunk loads, so forms never block on it. */
export function LazyCode(props: CodeEditorProps) {
  return (
    <Suspense
      fallback={
        <textarea
          value={props.value}
          onChange={(e) => props.onChange(e.target.value)}
          aria-label={props.ariaLabel}
          rows={4}
        />
      }
    >
      <CodeEditor {...props} />
    </Suspense>
  );
}

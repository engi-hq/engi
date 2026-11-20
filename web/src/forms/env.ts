import { createContext, useContext } from 'react';
import type { CredentialSummary } from '../api/types';
import type { PreviewScope } from './preview';
import type { ServerPreviewTarget } from './useExpressionPreview';

/** Context the form renderer needs besides the descriptor: credentials and preview data. */
export interface FormEnv {
  credentials: CredentialSummary[];
  preview: PreviewScope;
  jsonFields: string[];
  nodeNames: string[];
  /** Variable keys for `$vars.` autocomplete. */
  varKeys?: string[];
  readOnly: boolean;
  /** Offer the fixed/expression toggle (off for credential forms). */
  expressions: boolean;
  /** Server-side preview target; without it only the local path preview runs. */
  serverPreview?: ServerPreviewTarget;
}

export const emptyEnv: FormEnv = { credentials: [], preview: {}, jsonFields: [], nodeNames: [], readOnly: false, expressions: true };
export const FormEnvContext = createContext<FormEnv>(emptyEnv);
export const useFormEnv = () => useContext(FormEnvContext);

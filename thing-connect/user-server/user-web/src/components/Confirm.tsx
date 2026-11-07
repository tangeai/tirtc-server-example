import { Button } from './Button';
import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react';
import { Dialog } from './Dialog';
import { useLanguage } from '../lib/i18n';
interface Question { title: string; message: string; choices: { value: string; label: string }[] }
type Ask = (question: Question) => Promise<string>;
const Context = createContext<Ask>(() => Promise.resolve('cancel'));
export function useConfirm() { return useContext(Context); }
export function ConfirmProvider({ children }: { children: ReactNode }) {
  const { t } = useLanguage();
  const [question, setQuestion] = useState<Question>();
  const resolve = useRef<(value: string) => void>(() => {});
  function finish(value: string) { const callback = resolve.current; resolve.current = () => {}; setQuestion(undefined); callback(value); }
  useEffect(() => () => resolve.current('cancel'), []);
  const ask: Ask = question => { resolve.current('cancel'); setQuestion(question); return new Promise(done => { resolve.current = done; }); };
  return <Context.Provider value={ask}>{children}{question && <Dialog title={question.title} onClose={() => finish('cancel')}><p className="pre-wrap">{t(question.message)}</p><div className="confirm-actions">{question.choices.map(choice => <Button type="button" key={choice.value} className={['delete', 'discard'].includes(choice.value) ? 'danger' : ['save', 'restore', 'ok'].includes(choice.value) ? 'primary' : undefined} onClick={() => finish(choice.value)}>{t(choice.label)}</Button>)}</div></Dialog>}</Context.Provider>;
}

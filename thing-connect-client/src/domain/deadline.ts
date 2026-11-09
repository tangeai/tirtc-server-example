import { ApiError } from './http';

// Native callbacks may never arrive. Late callbacks cannot complete a timed-out request.
export function withDeadline<T>(start: (resolve: (value: T) => void, reject: (reason: unknown) => void) => void, milliseconds: number, message: string): Promise<T> {
  return new Promise((resolve, reject) => {
    let finished = false;
    const timer = setTimeout(() => { if (!finished) { finished = true; reject(new ApiError(-1, message)); } }, milliseconds);
    const done = (ok: boolean, value: unknown) => { if (finished) return; finished = true; clearTimeout(timer); if (ok) resolve(value as T); else reject(value); };
    try { start(value => done(true, value), reason => done(false, reason)); }
    catch (error) { done(false, error); }
  });
}

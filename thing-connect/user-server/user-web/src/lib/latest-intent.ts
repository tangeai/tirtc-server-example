// Coalesce SDK loading, but keep the latest valid user intent. Changing the
// form invalidates a pending intent; submitting again replaces it.
export class LatestIntent<T> {
  private revision = 0;
  private pending?: Promise<T>;
  private readonly load: () => Promise<T>;
  constructor(load: () => Promise<T>) { this.load = load; }
  cancel() { this.revision++; }
  async run(action: (value: T) => void | Promise<void>) {
    const revision = ++this.revision;
    const pending = this.pending ??= this.load();
    try {
      const value = await pending;
      if (revision === this.revision) await action(value);
    } finally { if (this.pending === pending) this.pending = undefined; }
  }
}

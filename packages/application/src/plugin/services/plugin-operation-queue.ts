type Task<T> = () => Promise<T>;

interface QueueItem<T> {
  task: Task<T>;
  resolve: (value: T) => void;
  reject: (error: unknown) => void;
}

export class PluginOperationQueue {
  private chain: Promise<void> = Promise.resolve();

  /** Serial/exclusive: run after all previously enqueued exclusive tasks. */
  enqueue<T>(task: Task<T>): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const item: QueueItem<T> = { task, resolve, reject };
      this.chain = this.chain.then(() => this.runItem(item));
    });
  }

  /**
   * Concurrent/shared: run immediately, not serialized against other
   * concurrent tasks. Used for read-heavy operations (tool calls) that must
   * not wait on each other, while still running under the same runtime-state
   * guard as everything else (callers check runtime state before
   * dispatching, and lifecycle `stopLocked` cancels in-flight calls).
   */
  enqueueConcurrent<T>(task: Task<T>): Promise<T> {
    return task();
  }

  private async runItem<T>(item: QueueItem<T>): Promise<void> {
    try {
      const result = await item.task();
      item.resolve(result);
    } catch (error) {
      item.reject(error);
    }
  }
}

type Task<T> = () => Promise<T>;

interface QueueItem<T> {
  task: Task<T>;
  resolve: (value: T) => void;
  reject: (error: unknown) => void;
}

export class PluginOperationQueue {
  private chain: Promise<void> = Promise.resolve();

  enqueue<T>(task: Task<T>): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const item: QueueItem<T> = { task, resolve, reject };
      this.chain = this.chain.then(() => this.runItem(item));
    });
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

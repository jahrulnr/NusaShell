// Queue is a bounded task queue.
export class Queue {
  constructor(limit) {
    this.limit = limit;
    this.items = [];
  }
  push(item) {
    this.items.push(item);
    return this;
  }
}

// drain empties the queue.
export function drain(q) {
  return q.items.splice(0);
}

const retry = async (task) => {
  return task.run();
};

function internal() {
  return null;
}

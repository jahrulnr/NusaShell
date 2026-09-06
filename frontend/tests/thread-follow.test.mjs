import assert from 'node:assert/strict';
import test from 'node:test';

import { createThreadFollow, stickThreadToBottom } from '../js/thread-follow.js';

function fakeFrameScheduler() {
  const queued = [];
  return {
    queued,
    schedule(_thread, callback) {
      queued.push(callback);
      return {
        cancel() {
          const index = queued.indexOf(callback);
          if (index >= 0) queued.splice(index, 1);
        },
      };
    },
    flush() {
      const batch = queued.splice(0, queued.length);
      for (const callback of batch) callback();
    },
  };
}

function fakeResizeObserver() {
  const observers = [];
  class ResizeObserver {
    constructor(callback) {
      this.callback = callback;
      this.targets = new Set();
      observers.push(this);
    }
    observe(target) {
      this.targets.add(target);
    }
    unobserve(target) {
      this.targets.delete(target);
    }
    disconnect() {
      this.targets.clear();
    }
    fire() {
      this.callback([...this.targets].map((target) => ({ target })));
    }
  }
  return { ResizeObserver, observers };
}

function fakeMutationObserver() {
  const observers = [];
  class MutationObserver {
    constructor(callback) {
      this.callback = callback;
      this.target = null;
      observers.push(this);
    }
    observe(target) {
      this.target = target;
    }
    disconnect() {
      this.target = null;
    }
    fire() {
      this.callback([]);
    }
  }
  return { MutationObserver, observers };
}

function makeThread(scrollHeight = 800) {
  const children = [];
  return {
    scrollHeight,
    clientHeight: 400,
    scrollTop: 0,
    children,
    ownerDocument: { defaultView: {} },
  };
}

test('stickThreadToBottom pins scrollTop to the content end', () => {
  const thread = makeThread(1200);
  thread.scrollTop = 10;
  assert.equal(stickThreadToBottom(thread), 1200);
  assert.equal(thread.scrollTop, 1200);
});

test('follow coalesces burst nudges into one frame and ignores a detached reader', () => {
  const thread = makeThread(1000);
  const frames = fakeFrameScheduler();
  const resize = fakeResizeObserver();
  const mutations = fakeMutationObserver();
  let following = true;
  const follow = createThreadFollow({
    getThread: () => thread,
    isFollowing: () => following,
    scheduleFrame: frames.schedule,
    ResizeObserver: resize.ResizeObserver,
    MutationObserver: mutations.MutationObserver,
  });

  follow.attach(thread);
  follow.nudge();
  follow.nudge();
  assert.equal(frames.queued.length, 1, 'burst tool/reason/activity nudges share one frame');
  frames.flush();
  assert.equal(thread.scrollTop, 1000);

  following = false;
  thread.scrollTop = 20;
  thread.scrollHeight = 1800;
  follow.nudge();
  assert.equal(frames.queued.length, 0);
  assert.equal(thread.scrollTop, 20, 'a reader who scrolled up is not yanked back');
});

test('activity-status growth on the last message keeps follow after live deltas stop', () => {
  const thread = makeThread(900);
  const lastMessage = { id: 'live-bubble' };
  thread.children.push(lastMessage);
  const frames = fakeFrameScheduler();
  const resize = fakeResizeObserver();
  const mutations = fakeMutationObserver();
  const follow = createThreadFollow({
    getThread: () => thread,
    isFollowing: () => true,
    scheduleFrame: frames.schedule,
    ResizeObserver: resize.ResizeObserver,
    MutationObserver: mutations.MutationObserver,
  });

  follow.attach(thread);
  frames.flush();
  assert.equal(thread.scrollTop, 900);
  assert.ok(resize.observers[0].targets.has(lastMessage), 'live message size is observed');

  // High-TPS settle used to stop after a few rAF frames. Showing
  // .agent-activity-status then grew the last bubble and shoved the old
  // flex end-marker toward the composer with no further follow tick.
  thread.scrollHeight = 1400;
  resize.observers[0].fire();
  assert.equal(frames.queued.length, 1);
  frames.flush();
  assert.equal(thread.scrollTop, 1400);
});

test('cancel drops a pending follow frame so user-up wins the race', () => {
  const thread = makeThread(1100);
  const frames = fakeFrameScheduler();
  const resize = fakeResizeObserver();
  const mutations = fakeMutationObserver();
  const follow = createThreadFollow({
    getThread: () => thread,
    isFollowing: () => true,
    scheduleFrame: frames.schedule,
    ResizeObserver: resize.ResizeObserver,
    MutationObserver: mutations.MutationObserver,
  });

  follow.attach(thread);
  assert.equal(frames.queued.length, 1);
  follow.cancel();
  assert.equal(frames.queued.length, 0);
  assert.equal(thread.scrollTop, 0);
});

test('a new thread child is observed so the next activity row can drive follow', () => {
  const thread = makeThread(500);
  const frames = fakeFrameScheduler();
  const resize = fakeResizeObserver();
  const mutations = fakeMutationObserver();
  const follow = createThreadFollow({
    getThread: () => thread,
    isFollowing: () => true,
    scheduleFrame: frames.schedule,
    ResizeObserver: resize.ResizeObserver,
    MutationObserver: mutations.MutationObserver,
  });

  follow.attach(thread);
  frames.flush();
  const nextMessage = { id: 'next-round' };
  thread.children.push(nextMessage);
  mutations.observers[0].fire();
  assert.ok(resize.observers[0].targets.has(nextMessage));
  thread.scrollHeight = 760;
  frames.flush();
  assert.equal(thread.scrollTop, 760);
});

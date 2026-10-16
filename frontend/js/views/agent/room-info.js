// Room info chrome — the `.agent-room-info` section now carries only the
// mini-window control. Room diagnostics (ID, tool calls, message count) are
// owned by the Context dock's context-list rows; the old (i) trigger and its
// popover were removed as a duplicate surface.

export function updateRoomInfo(conversation) {
  const info = document.getElementById('agent-room-info');
  if (!info) return;
  // The section stays hidden until a room is active — a mini window only
  // makes sense once there is a live thread to detach.
  info.hidden = !conversation?.id;
}

// Mini window (picture-in-picture) support.
//
// Two strategies behind one title-bar button:
//
// 1. Document Picture-in-Picture (Chromium): an always-on-top mini window.
//    The live #agent-thread AND #agent-composer-stack elements MOVE into
//    the PiP window, so the mini window is a full chat surface: you can
//    read the conversation and type/send from it. Moving (not cloning)
//    keeps every JS reference in views/agent alive, so streaming,
//    markdown, scroll pinning, and the composer handlers keep working on
//    the very same nodes. When the PiP window closes, both elements move
//    back into the shell.
//
// 2. Popup fallback (Firefox/Safari/everything else): a small independent
//    browser window running the full app at #agent with ?mini=1 (shell
//    chrome hidden via body.mini-mode). Not always-on-top, but fully
//    functional on every engine.

const PIP_SIZE = { width: 460, height: 680 };
const PLACEHOLDER_ID = 'agent-thread-pip-placeholder';

export function pipSupported() {
  return typeof window !== 'undefined' && 'documentPictureInPicture' in window;
}

export function miniWindowOpen() {
  return Boolean(document.getElementById(PLACEHOLDER_ID));
}

export async function openMiniWindow() {
  if (pipSupported()) {
    if (miniWindowOpen()) return 'pip';
    await openDocumentPip();
    return 'pip';
  }
  window.open(
    `${window.location.pathname}?mini=1#agent`,
    'nusashell-mini',
    `popup=yes,width=${PIP_SIZE.width},height=${PIP_SIZE.height}`,
  );
  return 'popup';
}

async function openDocumentPip() {
  const pipWin = await window.documentPictureInPicture.requestWindow(PIP_SIZE);
  copyStyleSheets(pipWin);

  const thread = document.getElementById('agent-thread');
  const composer = document.getElementById('agent-composer-stack');
  const placeholder = document.createElement('div');
  placeholder.id = PLACEHOLDER_ID;
  placeholder.className = 'agent-thread-pip-note';
  placeholder.textContent = 'Conversation and composer live in the mini window.';
  thread.replaceWith(placeholder);
  pipWin.document.body.classList.add('is-pip-window');
  pipWin.document.body.appendChild(thread);
  if (composer) pipWin.document.body.appendChild(composer);
  document.body.classList.add('is-pip-source');

  const restore = () => {
    placeholder.replaceWith(thread);
    if (composer) thread.insertAdjacentElement('afterend', composer);
    document.body.classList.remove('is-pip-source');
    pipWin.document.body.classList.remove('is-pip-window');
  };
  pipWin.addEventListener('pagehide', restore);
  // Put the caret in the composer so the user can type straight away.
  pipWin.document.getElementById('composer-input')?.focus();
}

function copyStyleSheets(pipWin) {
  for (const node of document.querySelectorAll('link[rel="stylesheet"], style')) {
    // Links re-fetch through the service worker cache, so this works even
    // when the local server is temporarily unreachable.
    pipWin.document.head.appendChild(node.cloneNode(true));
  }
}

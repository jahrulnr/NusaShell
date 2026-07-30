import {
  fileIcon,
  formatSize,
  formatDate,
  buildBreadcrumbs,
  joinPath,
  parentPath,
  baseName,
} from "./files-ui-state.js";

const pluginId = new URLSearchParams(location.search).get("pluginId") || "nusashell.files";

const state = {
  currentPath: "/",
  items: [],
  selectedItem: null,
  tree: null,
  expandedDirs: new Set(["/"]),
  searchQuery: "",
  searchResults: null,
};

const elements = {};
let modalMode = null;
let modalResolve = null;

function $(id) {
  return document.getElementById(id);
}

function initElements() {
  const ids = [
    "root-caption", "search-form", "search-input", "refresh-button", "up-button",
    "new-file-button", "tree-container", "collapse-tree-button",
    "breadcrumbs", "listing-count", "listing-body",
    "new-folder-button", "rename-button", "delete-button",
    "preview-pane", "preview-title", "preview-meta", "preview-content",
    "close-preview-button",
    "modal-overlay", "modal-card", "modal-title", "modal-form",
    "modal-input", "modal-textarea", "modal-content-field", "modal-field-label",
    "modal-error", "modal-cancel-button", "modal-save-button", "modal-close-button",
    "toast-container",
  ];
  for (const id of ids) elements[id] = $(id);
}

async function callTool(name, args = {}) {
  const result = await window.shell.callTool(pluginId, name, args);
  if (result?.isError) {
    throw new Error(result.content?.[0]?.text ?? "Tool error");
  }
  const text = result?.content?.[0]?.text;
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

function toast(message, type = "") {
  const el = document.createElement("div");
  el.className = `toast ${type}`;
  el.textContent = message;
  elements["toast-container"].appendChild(el);
  setTimeout(() => el.remove(), 3000);
}

async function loadListing() {
  elements["listing-body"].innerHTML = '<p class="listing-empty">Loading…</p>';
  try {
    const data = await callTool("files_list", { path: state.currentPath });
    state.items = data.items ?? [];
    state.selectedItem = null;
    renderListing();
    renderBreadcrumbs();
    updateActions();
    closePreview();
  } catch (error) {
    elements["listing-body"].innerHTML = `<p class="listing-empty">${error.message}</p>`;
  }
}

async function loadTree() {
  try {
    const data = await callTool("files_tree", { path: "/", depth: 3 });
    state.tree = data.tree;
    renderTree();
  } catch {
    elements["tree-container"].innerHTML = '<p class="tree-empty">Tree unavailable</p>';
  }
}

function renderBreadcrumbs() {
  const crumbs = buildBreadcrumbs(state.currentPath);
  const frag = document.createDocumentFragment();
  for (const crumb of crumbs) {
    const el = document.createElement("span");
    el.className = `breadcrumb-item${crumb.current ? " current" : ""}`;
    el.textContent = crumb.name;
    el.addEventListener("click", () => navigateTo(crumb.path));
    frag.appendChild(el);
    if (!crumb.current) {
      const sep = document.createElement("span");
      sep.className = "breadcrumb-sep";
      sep.textContent = "›";
      frag.appendChild(sep);
    }
  }
  elements["breadcrumbs"].replaceChildren(frag);
}

function renderListing() {
  const items = state.searchResults ?? state.items;
  if (!items || items.length === 0) {
    const msg = state.searchQuery ? "No files match the search." : "This folder is empty.";
    elements["listing-body"].innerHTML = `<p class="listing-empty">${msg}</p>`;
    elements["listing-count"].textContent = state.searchQuery
      ? `${items.length} results`
      : "Empty";
    return;
  }

  elements["listing-count"].textContent = state.searchQuery
    ? `${items.length} results`
    : `${items.length} item${items.length !== 1 ? "s" : ""}`;

  const frag = document.createDocumentFragment();
  for (const item of items) {
    const row = document.createElement("div");
    row.className = "listing-item";
    row.setAttribute("role", "listitem");
    if (state.selectedItem?.path === item.path) {
      row.classList.add("selected");
    }

    row.innerHTML = `
      <span class="listing-item-icon">${fileIcon(item)}</span>
      <span class="listing-item-name">${item.name}</span>
      <span class="listing-item-size">${item.isDir ? "—" : formatSize(item.size)}</span>
      <span class="listing-item-modified">${formatDate(item.modified)}</span>
      <span class="listing-item-action"><button title="More">⋯</button></span>
    `;

    row.addEventListener("click", (e) => {
      if (e.target.closest("button")) return;
      selectItem(item);
    });

    row.addEventListener("dblclick", () => {
      if (item.isDir) {
        navigateTo(joinPath(state.currentPath, item.name));
      } else {
        openPreview(item);
      }
    });

    frag.appendChild(row);
  }
  elements["listing-body"].replaceChildren(frag);
}

function renderTree() {
  if (!state.tree) {
    elements["tree-container"].innerHTML = '<p class="tree-empty">No tree data</p>';
    return;
  }
  const frag = renderTreeNodes(state.tree, "/");
  elements["tree-container"].replaceChildren(frag);
}

function renderTreeNodes(nodes, basePath) {
  const frag = document.createDocumentFragment();
  for (const node of nodes) {
    if (!node.isDir) continue;
    const nodePath = node.path;
    const isExpanded = state.expandedDirs.has(nodePath);
    const isActive = state.currentPath === nodePath;

    const row = document.createElement("div");
    row.className = `tree-node${isActive ? " active" : ""}`;
    row.innerHTML = `
      <span class="tree-toggle${isExpanded ? " expanded" : ""}">▶</span>
      <span class="tree-icon">${fileIcon(node)}</span>
      <span class="tree-label">${node.name}</span>
    `;

    row.addEventListener("click", () => {
      if (state.expandedDirs.has(nodePath)) {
        state.expandedDirs.delete(nodePath);
      } else {
        state.expandedDirs.add(nodePath);
      }
      navigateTo(nodePath);
    });

    frag.appendChild(row);

    if (isExpanded && node.children) {
      const childContainer = document.createElement("div");
      childContainer.className = `tree-children${isExpanded ? "" : " collapsed"}`;
      childContainer.appendChild(renderTreeNodes(node.children, nodePath));
      frag.appendChild(childContainer);
    }
  }
  return frag;
}

function selectItem(item) {
  state.selectedItem = item;
  renderListing();
  updateActions();
}

function updateActions() {
  const hasSelection = !!state.selectedItem;
  elements["rename-button"].disabled = !hasSelection;
  elements["delete-button"].disabled = !hasSelection;
}

function navigateTo(path) {
  state.currentPath = path;
  state.searchQuery = "";
  state.searchResults = null;
  elements["search-input"].value = "";
  loadListing();
  renderTree();
}

async function openPreview(item) {
  const filePath = joinPath(state.currentPath, item.name);
  elements["preview-pane"].hidden = false;
  elements["preview-title"].textContent = item.name;
  elements["preview-meta"].innerHTML = `
    <div class="preview-meta-row"><span>Type</span><span>${item.type}</span></div>
    <div class="preview-meta-row"><span>Size</span><span>${formatSize(item.size)}</span></div>
    <div class="preview-meta-row"><span>Modified</span><span>${formatDate(item.modified)}</span></div>
  `;

  if (item.type === "text") {
    elements["preview-content"].textContent = "Loading…";
    try {
      const result = await callTool("files_read", { path: filePath });
      elements["preview-content"].textContent = result.content;
      elements["preview-content"].className = "preview-content";
    } catch (error) {
      elements["preview-content"].textContent = error.message;
    }
  } else if (item.type === "image") {
    elements["preview-content"].className = "preview-content";
    elements["preview-content"].innerHTML = `<p>Image preview not available in plugin sandbox.</p>`;
  } else {
    elements["preview-content"].className = "preview-content binary-note";
    elements["preview-content"].textContent = `Binary file (${item.type}) — preview not available.`;
  }
}

function closePreview() {
  elements["preview-pane"].hidden = true;
}

function openModal(title, fieldLabel, defaultValue = "", showContent = false) {
  elements["modal-title"].textContent = title;
  elements["modal-field-label"].textContent = fieldLabel;
  elements["modal-input"].value = defaultValue;
  elements["modal-content-field"].hidden = !showContent;
  if (showContent) {
    elements["modal-textarea"].value = "";
  }
  elements["modal-error"].textContent = "";
  elements["modal-overlay"].hidden = false;
  elements["modal-input"].focus();

  return new Promise((resolve) => {
    modalResolve = resolve;
  });
}

function closeModal(result) {
  elements["modal-overlay"].hidden = true;
  if (modalResolve) {
    modalResolve(result);
    modalResolve = null;
  }
}

async function handleNewFile() {
  const name = await openModal("New file", "File name");
  if (!name) return;
  try {
    const filePath = joinPath(state.currentPath, name);
    const content = elements["modal-textarea"].value || "";
    await callTool("files_write", { path: filePath, content });
    toast("File created", "success");
    loadListing();
    loadTree();
  } catch (error) {
    toast(error.message, "error");
  }
}

async function handleNewFolder() {
  const name = await openModal("New folder", "Folder name");
  if (!name) return;
  try {
    const folderPath = joinPath(state.currentPath, name);
    await callTool("files_write", { path: folderPath + "/.gitkeep", content: "" });
    toast("Folder created", "success");
    loadListing();
    loadTree();
  } catch (error) {
    toast(error.message, "error");
  }
}

async function handleRename() {
  if (!state.selectedItem) return;
  const oldName = state.selectedItem.name;
  const newName = await openModal("Rename", "New name", oldName);
  if (!newName || newName === oldName) return;
  try {
    const source = joinPath(state.currentPath, oldName);
    const destination = joinPath(state.currentPath, newName);
    await callTool("files_move", { source, destination });
    toast("Renamed", "success");
    loadListing();
    loadTree();
  } catch (error) {
    toast(error.message, "error");
  }
}

async function handleDelete() {
  if (!state.selectedItem) return;
  const item = state.selectedItem;
  const itemPath = joinPath(state.currentPath, item.name);
  try {
    await callTool("files_delete", { path: itemPath, recursive: item.isDir });
    toast("Deleted", "success");
    state.selectedItem = null;
    loadListing();
    loadTree();
  } catch (error) {
    toast(error.message, "error");
  }
}

async function handleSearch(query) {
  if (!query.trim()) {
    state.searchQuery = "";
    state.searchResults = null;
    renderListing();
    return;
  }
  state.searchQuery = query;
  try {
    const result = await callTool("files_search", {
      path: state.currentPath,
      pattern: query,
    });
    state.searchResults = result.results ?? [];
    renderListing();
  } catch (error) {
    toast(error.message, "error");
  }
}

function goUp() {
  const parent = parentPath(state.currentPath);
  if (parent !== state.currentPath) {
    navigateTo(parent);
  }
}

function collapseTree() {
  state.expandedDirs.clear();
  state.expandedDirs.add("/");
  renderTree();
}

function init() {
  initElements();
  elements["root-caption"].textContent = "File manager";

  elements["search-form"].addEventListener("submit", (e) => {
    e.preventDefault();
    handleSearch(elements["search-input"].value);
  });

  elements["refresh-button"].addEventListener("click", () => {
    loadListing();
    loadTree();
  });

  elements["up-button"].addEventListener("click", goUp);
  elements["new-file-button"].addEventListener("click", handleNewFile);
  elements["new-folder-button"].addEventListener("click", handleNewFolder);
  elements["rename-button"].addEventListener("click", handleRename);
  elements["delete-button"].addEventListener("click", handleDelete);
  elements["close-preview-button"].addEventListener("click", closePreview);
  elements["collapse-tree-button"].addEventListener("click", collapseTree);

  elements["modal-form"].addEventListener("submit", (e) => {
    e.preventDefault();
    const value = elements["modal-input"].value.trim();
    if (!value) {
      elements["modal-error"].textContent = "Name is required";
      return;
    }
    closeModal(value);
  });

  elements["modal-cancel-button"].addEventListener("click", () => closeModal(null));
  elements["modal-close-button"].addEventListener("click", () => closeModal(null));
  elements["modal-overlay"].addEventListener("click", (e) => {
    if (e.target === elements["modal-overlay"]) closeModal(null);
  });

  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      if (!elements["modal-overlay"].hidden) {
        closeModal(null);
      } else if (!elements["preview-pane"].hidden) {
        closePreview();
      }
    }
  });

  loadListing();
  loadTree();
}

init();

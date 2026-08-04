let activeDialog = null;

function closeDialog(result) {
  if (!activeDialog) return;
  const { overlay, resolve, restoreFocus } = activeDialog;
  overlay.remove();
  activeDialog = null;
  restoreFocus?.focus?.();
  resolve(result);
}

function openDialog({ title, message, label, defaultValue = "", confirmLabel = "Confirm", danger = false, prompt = false, allowEmpty = false }) {
  if (activeDialog) closeDialog(null);
  const restoreFocus = document.activeElement;
  const overlay = document.createElement("div");
  overlay.className = "ui-dialog-overlay";
  overlay.innerHTML = `
    <section class="ui-dialog" role="dialog" aria-modal="true" aria-labelledby="ui-dialog-title">
      <div class="ui-dialog-header">
        <h2 id="ui-dialog-title"></h2>
        <button class="ui-dialog-close" type="button" aria-label="Close">×</button>
      </div>
      <div class="ui-dialog-body">
        <p class="ui-dialog-message"></p>
        <label class="ui-dialog-field" hidden><span></span><input type="text"></label>
      </div>
      <div class="ui-dialog-actions">
        <button class="mini-btn ui-dialog-cancel" type="button">Cancel</button>
        <button class="mini-btn ui-dialog-confirm" type="button"></button>
      </div>
    </section>`;
  document.body.appendChild(overlay);
  const dialog = overlay.querySelector(".ui-dialog");
  const input = overlay.querySelector("input");
  overlay.querySelector("#ui-dialog-title").textContent = title;
  overlay.querySelector(".ui-dialog-message").textContent = message;
  const field = overlay.querySelector(".ui-dialog-field");
  if (prompt) {
    field.hidden = false;
    field.querySelector("span").textContent = label || "Value";
    input.value = defaultValue;
    input.placeholder = label || "";
  }
  const confirm = overlay.querySelector(".ui-dialog-confirm");
  confirm.textContent = confirmLabel;
  confirm.classList.toggle("danger", danger);

  return new Promise((resolve) => {
    activeDialog = { overlay, resolve, restoreFocus };
    const submit = () => {
      if (prompt) {
        const value = input.value.trim();
        if (!value && !allowEmpty) {
          input.focus();
          return;
        }
        closeDialog(value);
      } else closeDialog(true);
    };
    confirm.addEventListener("click", submit);
    overlay.querySelector(".ui-dialog-cancel").addEventListener("click", () => closeDialog(prompt ? null : false));
    overlay.querySelector(".ui-dialog-close").addEventListener("click", () => closeDialog(prompt ? null : false));
    overlay.addEventListener("click", (event) => {
      if (event.target === overlay) closeDialog(prompt ? null : false);
    });
    dialog.addEventListener("keydown", (event) => {
      if (event.key === "Escape") closeDialog(prompt ? null : false);
      if (event.key === "Enter" && prompt) submit();
    });
    (prompt ? input : overlay.querySelector(".ui-dialog-cancel")).focus();
  });
}

export function confirmDialog(options) {
  return openDialog({ ...options, prompt: false });
}

export function promptDialog(options) {
  return openDialog({ ...options, prompt: true });
}

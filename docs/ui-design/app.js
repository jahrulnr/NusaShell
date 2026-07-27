// All interactions here are cosmetic / visual state only - no real MCP
// (or any other) logic. Just enough to make the mockup feel alive.

document.addEventListener("DOMContentLoaded", () => {

  // --- Sidebar nav item switching ---
  document.querySelectorAll("[data-nav]").forEach((item) => {
    item.addEventListener("click", () => {
      document.querySelectorAll("[data-nav]").forEach((i) => i.classList.remove("active"));
      item.classList.add("active");
    });
  });

  // --- Filter tabs switching ---
  document.querySelectorAll("[data-tab]").forEach((tab) => {
    tab.addEventListener("click", () => {
      if (tab.classList.contains("tab-add")) return;
      document.querySelectorAll("[data-tab]").forEach((t) => t.classList.remove("active"));
      tab.classList.add("active");
    });
  });

  // --- App icon click: small bounce only; does not open anything ---
  document.querySelectorAll("[data-app]").forEach((cell) => {
    cell.addEventListener("click", () => {
      const icon = cell.querySelector(".app-icon");
      icon.style.transform = "scale(0.9)";
      setTimeout(() => { icon.style.transform = "scale(1)"; }, 120);
    });
  });

  // --- Search focus: placeholder shift (glow already handled by CSS :focus-within) ---
  const searchInput = document.querySelector(".search input");
  if (searchInput) {
    searchInput.addEventListener("focus", () => {
      searchInput.placeholder = "Type to search...";
    });
    searchInput.addEventListener("blur", () => {
      searchInput.placeholder = "Search apps...";
    });
  }

});

import { describe, expect, it, vi } from "vitest";
import { buildTrayMenuTemplate } from "../src/main/tray.js";

describe("buildTrayMenuTemplate", () => {
  it("builds Open and Quit entries and calls Quit callback", () => {
    const onOpen = vi.fn();
    const onQuit = vi.fn();
    const template = buildTrayMenuTemplate({
      statusLabel: "NusaShell — running",
      onOpen,
      onQuit,
    });

    expect(template[0]).toMatchObject({
      label: "NusaShell — running",
      enabled: false,
    });
    expect(template[1]).toMatchObject({ label: "Open NusaShell" });
    expect(template[2]).toMatchObject({ type: "separator" });
    expect(template[3]).toMatchObject({ label: "Quit" });

    const open = template[1];
    const quit = template[3];
    if (open && typeof open === "object" && "click" in open && typeof open.click === "function") {
      open.click(undefined as never, undefined as never, undefined as never);
    }
    if (quit && typeof quit === "object" && "click" in quit && typeof quit.click === "function") {
      quit.click(undefined as never, undefined as never, undefined as never);
    }
    expect(onOpen).toHaveBeenCalledOnce();
    expect(onQuit).toHaveBeenCalledOnce();
  });
});

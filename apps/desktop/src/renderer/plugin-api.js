// Plugin API calls — extracted from launcher.js.

import { sendRequest } from "./ws-client.js";

export async function fetchPlugins() {
  try {
    const result = await sendRequest("plugin.list", {});
    return [...result.plugins];
  } catch (e) {
    console.error("Failed to fetch plugins:", e);
    return [];
  }
}

export async function startPlugin(pluginId) {
  try { await sendRequest("plugin.start", { pluginId }); } catch (e) { console.error(e); }
}

export async function stopPlugin(pluginId) {
  try { await sendRequest("plugin.stop", { pluginId }); } catch (e) { console.error(e); }
}

export async function restartPlugin(pluginId) {
  try { await sendRequest("plugin.restart", { pluginId }); } catch (e) { console.error(e); }
}

export async function getPluginDetail(pluginId) {
  try { return await sendRequest("plugin.get", { pluginId }); } catch (e) { console.error(e); return null; }
}

export async function listTools(pluginId) {
  // Do not swallow errors as an empty tool list (finding 3a): surface the
  // failure so the drawer can distinguish "listing failed" from "no tools".
  try {
    return await sendRequest("tool.list", { pluginId });
  } catch (e) {
    return { tools: [], error: { message: e?.message || "tool.list failed" } };
  }
}

export async function callTool(pluginId, toolName, args) {
  const requestId = `req_${crypto.randomUUID()}`;
  try { return await sendRequest("tool.call", { pluginId, requestId, toolName, args }); }
  catch (e) { return { error: e.message }; }
}

export async function pingSystem() {
  try { return await sendRequest("system.ping", {}); } catch (e) { return { error: e.message }; }
}

export async function getVersion() {
  try { return await sendRequest("system.version", {}); } catch (e) { return { error: e.message }; }
}

export async function installPlugin(source, path) {
  try { return await sendRequest("plugin.install", { source, path }, 30000); }
  catch (e) { return { error: e.message }; }
}

export async function uninstallPlugin(pluginId) {
  try { return await sendRequest("plugin.uninstall", { pluginId }); }
  catch (e) { return { error: e.message }; }
}

export async function setPluginAutostart(pluginId, autostart) {
  try { return await sendRequest("plugin.autostart", { pluginId, autostart }); }
  catch (e) { return { error: e.message }; }
}

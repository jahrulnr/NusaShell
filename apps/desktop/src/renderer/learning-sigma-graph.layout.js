/**
 * Re-export of the chunked ForceAtlas2 layout helpers used by
 * LearningSigmaGraph. Kept as a stable named entry point so the mount path
 * stays decoupled from the layout internals.
 */
export {
  computeFaLayoutSettings,
  createFALayoutState,
  runNextFaChunk,
  applyFaLayout,
  forceAtlas2Chunked,
  createNoverlapState,
  runNextNoverlapChunk,
  applyNoverlapLayout,
  noverlapChunked,
  runFaLayoutChunked,
  layoutGraphChunked,
  fa2SettingsForOrder,
  noverlapSettings,
} from "./learning-layout-chunker.js";

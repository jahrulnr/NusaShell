// Package logs owns the log store surface: dispatch and handlers
// for the Logs view (chronological, oldest-first, level filters). It must not
// be confused with structured internal logging, which stays on the App logger.
// Feature packages never receive *App; wiring is a narrow Deps struct.
package logs

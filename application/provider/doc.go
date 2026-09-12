// Package provider owns chat providers: provider CRUD and
// import, model catalogs and overrides, endpoint cache, environment
// credentials, retry/rate-limit/slowdown policy, and the application-to-core
// request translation. It is the only consumer of the core wire contract.
// MCP calls explicitly disable strict tool schemas on Responses and Codex
// so their free-form argument objects retain the discovered MCP fields.
package provider

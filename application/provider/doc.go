// Package provider owns chat providers: provider CRUD and
// import, model catalogs and overrides, endpoint cache, environment
// credentials, retry/rate-limit/slowdown policy, and the application-to-core
// request translation. It is the only consumer of the core wire contract.
package provider

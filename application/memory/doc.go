// Package memory owns durable memory records: the memory
// service, record dispatch/handlers, retrieval, and task-memory announcements
// into conversations. The learner pipeline (learn) writes through this
// package's ports; memory never imports learn.
package memory

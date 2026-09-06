// Package tools owns tool surface assembly: the toolbox factory,
// tool contracts, op-style dispatcher families (skill, memory, docs,
// memory_project, automation, automation_schedule), and the agent-exposed
// model override tool. Tools are thin dispatchers: they validate input, call
// the owning subsystem through ports, and return contract DTOs.
package tools

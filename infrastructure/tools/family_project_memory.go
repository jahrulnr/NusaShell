package tools

import "context"

// executeProjectMemoryFamily routes the memory_project root to the project
// memory handlers in project_memory.go. It is reached only through
// executeFamily, which resolves and validates the op via
// application.DispatchOp before delegating here.
func (t *Toolbox) executeProjectMemoryFamily(ctx context.Context, op string, argsJSON []byte) (string, error) {
	return t.executeProjectMemory(ctx, op, argsJSON)
}

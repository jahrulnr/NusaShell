package repository

import (
	"context"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
)

// MCPServer stores the owner's MCP servers. Names are unique.
type MCPServer interface {
	Create(ctx context.Context, s *aggregate.MCPServer) error
	// Update saves s when the stored copy is still at readAt; otherwise
	// a conflict, reported with a stable error identity.
	Update(ctx context.Context, s *aggregate.MCPServer, readAt time.Time) error
	Get(ctx context.Context, id string) (*aggregate.MCPServer, error)
	List(ctx context.Context) ([]aggregate.MCPServer, error)
	Delete(ctx context.Context, id string) error
}

package download

import (
	"context"

	domain "github.com/vaisah/internal/domain/download"
)

// DownloadPort is the engine capability required by the application
// service. *engine.Manager satisfies it; tests use fakes.
type DownloadPort interface {
	Recover(ctx context.Context) error
	Shutdown(ctx context.Context) error
	Create(ctx context.Context, req domain.CreateRequest) (*domain.Download, error)
	Start(ctx context.Context, id string) error
	Resume(ctx context.Context, id string) error
	Pause(ctx context.Context, id string) error
	Cancel(ctx context.Context, id string) error
	Retry(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (*domain.Download, error)
	List(ctx context.Context) ([]domain.Download, error)
}

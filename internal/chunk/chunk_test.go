package chunk

import (
	"context"
	"errors"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func TestStubReturnsErrNotImplemented(t *testing.T) {
	if _, err := New().Chunk(context.Background(), &model.File{}, nil, nil); !errors.Is(err, model.ErrNotImplemented) {
		t.Errorf("Chunk() err = %v, want ErrNotImplemented", err)
	}
}

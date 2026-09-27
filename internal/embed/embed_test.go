package embed

import (
	"context"
	"errors"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func TestStubReturnsErrNotImplemented(t *testing.T) {
	if _, err := New().Embed(context.Background(), []string{"a"}); !errors.Is(err, model.ErrNotImplemented) {
		t.Errorf("Embed() err = %v, want ErrNotImplemented", err)
	}
}

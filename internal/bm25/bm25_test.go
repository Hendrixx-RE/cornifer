package bm25

import (
	"context"
	"errors"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func TestStubReturnsErrNotImplemented(t *testing.T) {
	if err := New().Index(context.Background(), nil); !errors.Is(err, model.ErrNotImplemented) {
		t.Errorf("Index() err = %v, want ErrNotImplemented", err)
	}
	if _, err := New().Search(context.Background(), "q", 5); !errors.Is(err, model.ErrNotImplemented) {
		t.Errorf("Search() err = %v, want ErrNotImplemented", err)
	}
}

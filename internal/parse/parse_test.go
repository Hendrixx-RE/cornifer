package parse

import (
	"context"
	"errors"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func TestStubReturnsErrNotImplemented(t *testing.T) {
	if _, err := New().Parse(context.Background(), "f.py", nil); !errors.Is(err, model.ErrNotImplemented) {
		t.Errorf("Parse() err = %v, want ErrNotImplemented", err)
	}
}

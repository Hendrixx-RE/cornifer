package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

func TestStubReturnsErrNotImplemented(t *testing.T) {
	s := New()
	ctx := context.Background()

	if _, err := s.CreateRepo(ctx, &model.Repo{}); !errors.Is(err, model.ErrNotImplemented) {
		t.Errorf("CreateRepo() err = %v, want ErrNotImplemented", err)
	}
	if _, err := s.VectorSearch(ctx, nil, 5); !errors.Is(err, model.ErrNotImplemented) {
		t.Errorf("VectorSearch() err = %v, want ErrNotImplemented", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close() err = %v, want nil", err)
	}
}

func TestVectorRoundTrip(t *testing.T) {
	want := []float32{0.1, 0.2, 0.3}
	got := DecodeVector(EncodeVector(want))
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DecodeVector(EncodeVector(%v)) = %v, want %v", want, got, want)
	}
}

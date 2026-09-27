package embed

import (
	"context"
	"reflect"
	"testing"
)

func TestFakeEmbedderHonoursCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := newFakeEmbedder(4)
	if _, err := f.Embed(ctx, []string{"a"}); err == nil {
		t.Fatal("Embed() err = nil, want context.Canceled")
	}
}

func TestFakeVectorDeterministic(t *testing.T) {
	a := fakeVector("hello world", 16)
	b := fakeVector("hello world", 16)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("fakeVector not deterministic: %v != %v", a, b)
	}
}

func TestFakeVectorDiffersByText(t *testing.T) {
	a := fakeVector("hello", 16)
	b := fakeVector("world", 16)
	if reflect.DeepEqual(a, b) {
		t.Fatal("fakeVector(\"hello\") == fakeVector(\"world\"), want different vectors")
	}
}

func TestFakeVectorDiffersByDimension(t *testing.T) {
	a := fakeVector("hello", 8)
	b := fakeVector("hello", 16)
	if len(a) != 8 || len(b) != 16 {
		t.Fatalf("len(a)=%d len(b)=%d, want 8 and 16", len(a), len(b))
	}
}

func TestFakeVectorEmptyText(t *testing.T) {
	v := fakeVector("", 4)
	if len(v) != 4 {
		t.Fatalf("len(v) = %d, want 4", len(v))
	}
}
